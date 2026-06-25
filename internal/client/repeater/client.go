// Package repeater is the client for talking to remote repeater nodes over the
// mesh: login (ANON_REQ), status, CLI, path get/reset/set, neighbours,
// telemetry and access-list (ACL) administration. It drives repeaters; it does
// not emulate one. The local node it sends through is supplied at construction.
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

	"github.com/meshcore-go/meshcore-bot/internal/store"
)

const (
	reqTypeGetStatus        = 0x01
	reqTypeGetTelemetryData = 0x03
	reqTypeGetAccessList    = 0x05
	reqTypeGetNeighbors     = 0x06
	reqTypeGetOwnerInfo     = 0x07
	txtTypeCliData          = 1
	cliPrefixLen            = 3
)

type Session struct {
	PubKeyHex    string    `json:"pubkeyHex"`
	IsAdmin      bool      `json:"isAdmin"`
	Role         string    `json:"role,omitempty"` // room sessions: "admin" | "read-write" | "read-only"
	IsRoom       bool      `json:"isRoom,omitempty"`
	LoggedInAt   time.Time `json:"loggedInAt"`
	sharedSecret []byte
	localPubKey  [32]byte
}

type pendingRequest struct {
	ch      chan []byte
	created time.Time

	// Set for sessionless requests (contact telemetry) so the response — plain
	// Response or flood PathReturn — can be matched/decrypted without a session.
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
	node  *node.Node
	store *store.Store
	log   *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session

	pendingMu sync.Mutex
	pending   map[uint32]*pendingRequest

	loginMu       sync.Mutex
	pendingLogins []*pendingLogin

	cliMu      sync.Mutex
	cliPending map[string]chan string
}

func NewClient(n *node.Node, st *store.Store, log *slog.Logger) *Client {
	return &Client{
		node:       n,
		store:      st,
		log:        log,
		sessions:   make(map[string]*Session),
		pending:    make(map[uint32]*pendingRequest),
		cliPending: make(map[string]chan string),
	}
}

func (rm *Client) persistOutPath(pubkey []byte, path []byte, hashSize uint8) {
	rm.store.WriteAsync(func() {
		if err := rm.store.Peers.UpdateOutPath(context.Background(), pubkey, path, hashSize); err != nil {
			rm.log.Error("failed to persist out_path", "error", err)
		}
	})
}

// routeForPeer chooses the send route for a packet to peer, mirroring the
// node.Peer OutPath contract:
//   - nil           -> route unknown, FLOOD
//   - []byte{}      -> direct neighbour (0 hops), RouteTypeDirect with empty path
//   - []byte{...}   -> multi-hop, RouteTypeDirect along the stored path
//
// A node we can reach directly (including a 0-hop neighbour) must NOT be
// flooded — flooding rebroadcasts across the whole mesh and pollutes it. Only a
// genuinely unknown route (nil) floods. Returns the header route-type byte and
// the encoded path-length byte (upper 2 bits = hashSize-1, lower 6 = hop count).
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

// sendBinaryRequest builds and sends a PAYLOAD_TYPE_REQ to a logged-in repeater
// with a custom payload body (placed at offset 4 onward; tag is prepended), and
// awaits the matching response. Returns the response data (without the tag).
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

	// Session decrypts the response, so the pending entry needn't carry the secret.
	return rm.roundtripRequest(peerIdentity.PublicKey(), peer, sess.sharedSecret, sess.localPubKey[0], body, timeout, label, false)
}

// roundtripRequest encrypts body into a PayloadTypeReq, sends it (direct if an
// OutPath is known, else flood), and awaits the tagged response (tag stripped).
// storeSecret carries the secret on the pending entry for sessionless matching.
func (rm *Client) roundtripRequest(peerPub [32]byte, peer *node.Peer, sharedSecret []byte, localPubByte byte, body []byte, timeout time.Duration, label string, storeSecret bool) ([]byte, error) {
	tag := uint32(time.Now().Unix())

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
