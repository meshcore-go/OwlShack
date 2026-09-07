// Package repeater drives remote repeater nodes over the mesh; it does not emulate one.
package repeater

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/store"
)

const (
	reqTypeGetStatus        = 0x01
	reqTypeKeepAlive        = 0x02 // rooms: resume the post push stream
	reqTypeGetTelemetryData = 0x03
	reqTypeGetAvgMinMax     = 0x04 // sensors only
	reqTypeGetAccessList    = 0x05
	reqTypeGetNeighbors     = 0x06
	reqTypeGetOwnerInfo     = 0x07
	txtTypeCliData          = 1
	cliPrefixLen            = 3
	respServerLoginOK       = 0 // login reply byte 4
)

// isLoginReply: byte 4 alone also matches a status body with a zero batt low byte, so check byte 5's always-zero legacy field too.
func isLoginReply(data []byte) bool {
	return len(data) >= 13 && data[4] == respServerLoginOK && data[5] == 0
}

type Session struct {
	PubKeyHex string `json:"pubkeyHex"`
	IsAdmin   bool   `json:"isAdmin"`
	// Permissions is login reply byte 7: low 2 bits role, and on sensors bits 6-7 the alert subscription.
	Permissions  int       `json:"permissions"`
	Role         string    `json:"role,omitempty"` // room sessions: "admin" | "read-write" | "read-only"
	IsRoom       bool      `json:"isRoom,omitempty"`
	LoggedInAt   time.Time `json:"loggedInAt"`
	sharedSecret []byte
	localPubKey  [32]byte
}

type pendingRequest struct {
	ch      chan []byte
	created time.Time

	// Set for sessionless requests so the response can be matched and decrypted without a session.
	sharedSecret   []byte
	peerPubKeyByte byte
	peerPubKey     [32]byte
}

type pendingLogin struct {
	ch             chan []byte
	created        time.Time
	sharedSecret   []byte
	peerPubKeyByte byte
	peerPubKey     [32]byte
}

type Client struct {
	node        *node.Node
	store       *store.Store
	companionID int64 // owner of the contact rows that persist learned routes
	log         *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session

	pendingMu sync.Mutex
	pending   map[uint32]*pendingRequest

	loginMu       sync.Mutex
	pendingLogins []*pendingLogin

	cliMu      sync.Mutex
	cliPending map[string]chan string

	tsMu   sync.Mutex
	lastTS uint32
}

// UniqueTimestamp mirrors the firmware's getCurrentTimeUnique(): a remote node drops a timestamp <= the last one it saw.
func (rm *Client) UniqueTimestamp() uint32 {
	rm.tsMu.Lock()
	defer rm.tsMu.Unlock()
	ts := uint32(time.Now().Unix())
	if ts <= rm.lastTS {
		ts = rm.lastTS + 1
	}
	rm.lastTS = ts
	return ts
}

func NewClient(n *node.Node, st *store.Store, companionID int64, log *slog.Logger) *Client {
	return &Client{
		node:        n,
		store:       st,
		companionID: companionID,
		log:         log,
		sessions:    make(map[string]*Session),
		pending:     make(map[uint32]*pendingRequest),
		cliPending:  make(map[string]chan string),
	}
}

func (rm *Client) persistOutPath(pubkey []byte, path []byte, hashSize uint8) {
	rm.store.WriteAsync(func() {
		if err := rm.store.Contacts.UpdateOutPath(context.Background(), rm.companionID, pubkey, path, hashSize); err != nil {
			rm.log.Error("failed to persist out_path", "error", err)
		}
	})
}

// routeForPeer follows the OutPath contract: only nil (unknown) floods, and the length byte is hashSize-1 in the upper 2 bits.
func routeForPeer(peer *node.Peer) (routeType byte, pathLen uint8) {
	if peer == nil || peer.OutPath == nil {
		return meshcore.RouteTypeFlood, 0
	}
	if len(peer.OutPath) == 0 {
		return meshcore.RouteTypeDirect, 0 // direct neighbour, no hops to encode
	}
	hashSize := int(peer.OutPathHashSize)
	if hashSize == 0 {
		hashSize = int(meshcore.PathHashSize)
	}
	return meshcore.RouteTypeDirect, uint8(hashSize-1)<<6 | uint8(len(peer.OutPath)/hashSize)
}

func (rm *Client) Session(pubkeyHex string) *Session {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.sessions[pubkeyHex]
}

func (rm *Client) Logout(pubkeyHex string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.sessions, pubkeyHex)
}

// sendBinaryRequest prepends the tag, placing body at offset 4, and returns the response without its tag.
func (rm *Client) sendBinaryRequest(pubkeyHex string, body []byte, timeout time.Duration, label string) ([]byte, error) {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey: %w", err)
	}

	peer := rm.node.Peers().Lookup(peerIdentity.PublicKey())
	if peer == nil {
		return nil, fmt.Errorf("peer not found in peer table")
	}

	rm.mu.Lock()
	sess := rm.sessions[pubkeyHex]
	rm.mu.Unlock()
	if sess == nil || sess.sharedSecret == nil {
		return nil, fmt.Errorf("not logged in to this repeater")
	}

	// The session decrypts the response, so the pending entry needn't carry the secret.
	return rm.roundtripRequest(peerIdentity.PublicKey(), peer, sess.sharedSecret, sess.localPubKey[0], body, timeout, label, false)
}

// roundtripRequest awaits the tagged response; storeSecret puts the secret on the pending entry for sessionless matching.
func (rm *Client) roundtripRequest(peerPub [32]byte, peer *node.Peer, sharedSecret []byte, localPubByte byte, body []byte, timeout time.Duration, label string, storeSecret bool) ([]byte, error) {
	tag := rm.UniqueTimestamp()

	plaintext := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(plaintext[:4], tag)
	copy(plaintext[4:], body)

	encrypted, err := meshcore.EncryptThenMAC(sharedSecret, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypting %s req: %w", label, err)
	}

	var mac [2]byte
	copy(mac[:], encrypted[:2])

	req := &meshcore.Request{
		Destination:      peerPub[0],
		Source:           localPubByte,
		MAC:              mac,
		EncryptedPayload: encrypted[2:],
	}

	reqBytes, err := req.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	resultCh := make(chan []byte, 1)
	pr := &pendingRequest{ch: resultCh, created: time.Now()}
	if storeSecret {
		pr.sharedSecret = sharedSecret
		pr.peerPubKeyByte = peerPub[0]
		pr.peerPubKey = peerPub
	}
	rm.pendingMu.Lock()
	rm.pending[tag] = pr
	rm.pendingMu.Unlock()

	defer func() {
		rm.pendingMu.Lock()
		delete(rm.pending, tag)
		rm.pendingMu.Unlock()
	}()

	routeType, pathLen := routeForPeer(peer)

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeReq, 0),
		PathLength: pathLen,
		Path:       peer.OutPath,
		Payload:    reqBytes,
	}

	if err := rm.node.SendPacket(pkt); err != nil {
		return nil, fmt.Errorf("sending %s req: %w", label, err)
	}

	rm.log.Debug(label+" req sent", "peer", fmt.Sprintf("%x", peerPub[:6]), "tag", fmt.Sprintf("%08x", tag))

	select {
	case data := <-resultCh:
		return data, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s request timed out", label)
	}
}
