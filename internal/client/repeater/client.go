// Package repeater is the client for talking to remote repeater nodes over the
// mesh: login (ANON_REQ), status, CLI, path get/reset/set, neighbours,
// telemetry and access-list (ACL) administration. It drives repeaters; it does
// not emulate one. The local node it sends through is supplied at construction.
package repeater

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/meshcore-bot/internal/store"
	"github.com/meshcore-go/meshcore-bot/internal/telemetry"
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

type Neighbor struct {
	PubkeyPrefix string `json:"pubkeyPrefix"`
	SecsAgo      uint32 `json:"secsAgo"`
	SNR          int    `json:"snr"`
}

type Neighbors struct {
	TotalCount   int        `json:"totalCount"`
	ResultsCount int        `json:"resultsCount"`
	Neighbors    []Neighbor `json:"neighbors"`
}

type OwnerInfo struct {
	FirmwareVersion string `json:"firmwareVersion"`
	NodeName        string `json:"nodeName"`
	OwnerInfo       string `json:"ownerInfo"`
}

type AccessListEntry struct {
	PubkeyPrefix string `json:"pubkeyPrefix"`
	Permissions  uint8  `json:"permissions"`
}

type AccessList struct {
	Entries []AccessListEntry `json:"entries"`
}

type Status struct {
	BatteryMV   uint16  `json:"batteryMv"`
	QueueLen    uint16  `json:"queueLen"`
	NoiseFloor  int16   `json:"noiseFloor"`
	LastRSSI    int16   `json:"lastRssi"`
	PacketsRecv uint32  `json:"packetsRecv"`
	PacketsSent uint32  `json:"packetsSent"`
	TxAirSecs   uint32  `json:"txAirSecs"`
	RxAirSecs   uint32  `json:"rxAirSecs"`
	UptimeSecs  uint32  `json:"uptimeSecs"`
	FloodTx     uint32  `json:"floodTx"`
	DirectTx    uint32  `json:"directTx"`
	FloodRx     uint32  `json:"floodRx"`
	DirectRx    uint32  `json:"directRx"`
	ErrEvents   uint16  `json:"errEvents"`
	LastSNR     float64 `json:"lastSnr"`
	DirectDups  uint16  `json:"directDups"`
	FloodDups   uint16  `json:"floodDups"`
	RecvErrors  uint32  `json:"recvErrors"`
	ChanUtil    float64 `json:"chanUtil"`
}

type Session struct {
	PubKeyHex    string    `json:"pubkeyHex"`
	IsAdmin      bool      `json:"isAdmin"`
	LoggedInAt   time.Time `json:"loggedInAt"`
	sharedSecret []byte
	localPubKey  [32]byte
}

type LoginResult struct {
	Success bool `json:"success"`
	IsAdmin bool `json:"isAdmin"`
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
		if err := rm.store.Peers.UpdateOutPath(pubkey, path, hashSize); err != nil {
			rm.log.Error("failed to persist out_path", "error", err)
		}
	})
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

type PeerPathInfo struct {
	OutPath        string `json:"outPath"`
	Hops           int    `json:"hops"`
	HasPath        bool   `json:"hasPath"`
	DirectNeighbor bool   `json:"directNeighbor"`
	PathHashSize   int    `json:"pathHashSize"`
}

func (rm *Client) GetPeerPath(pubkeyHex string) (*PeerPathInfo, error) {
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

	hashSize := int(peer.OutPathHashSize)
	if hashSize == 0 {
		hashSize = int(meshcore.PathHashSize)
	}

	info := &PeerPathInfo{PathHashSize: hashSize}
	if peer.OutPath != nil && len(peer.OutPath) == 0 {
		info.DirectNeighbor = true
		info.HasPath = true
		info.Hops = 0
	} else if len(peer.OutPath) > 0 {
		info.HasPath = true
		info.Hops = len(peer.OutPath) / hashSize
		info.OutPath = hex.EncodeToString(peer.OutPath)
	}
	return info, nil
}

func (rm *Client) ResetPeerPath(pubkeyHex string) error {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}

	if !rm.node.Peers().ResetOutPath(peerIdentity.PublicKey()) {
		return fmt.Errorf("peer not found in peer table")
	}
	return nil
}

func (rm *Client) SetPeerPath(pubkeyHex, pathHex string, pathHashSize int) error {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}

	pathBytes, err := hex.DecodeString(pathHex)
	if err != nil {
		return fmt.Errorf("invalid path hex: %w", err)
	}

	if pathHashSize <= 0 {
		pathHashSize = int(meshcore.PathHashSize)
	}

	if !rm.node.Peers().SetOutPath(peerIdentity.PublicKey(), pathBytes, uint8(pathHashSize)) {
		return fmt.Errorf("peer not found in peer table")
	}
	return nil
}

func (rm *Client) SendLogin(pubkeyHex, password string, timeout time.Duration) (*LoginResult, error) {
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

	// Use the companion's static identity rather than a throwaway ephemeral key
	// so the repeater registers us in its ACL under our real pubkey. This lets
	// the firmware accept blank-password reauth on subsequent logins (its
	// `getClient(sender.pub_key)` lookup) and avoids accumulating orphan
	// contact entries on the repeater.
	selfIdentity := rm.node.Identity()
	selfSeed := selfIdentity.Seed()

	sharedSecret, err := meshcore.DeriveSharedSecret(selfSeed[:], peerIdentity.PublicKeyBytes())
	if err != nil {
		return nil, fmt.Errorf("deriving shared secret: %w", err)
	}

	plaintext := make([]byte, 4+len(password))
	binary.LittleEndian.PutUint32(plaintext[:4], uint32(time.Now().Unix()))
	copy(plaintext[4:], password)

	encrypted, err := meshcore.EncryptThenMAC(sharedSecret, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypting login: %w", err)
	}

	var mac [2]byte
	copy(mac[:], encrypted[:2])

	anonReq := &meshcore.AnonReq{
		Destination:      peerIdentity.PublicKey()[0],
		EphemeralPubKey:  selfIdentity.PublicKey(),
		MAC:              mac,
		EncryptedPayload: encrypted[2:],
	}

	payload, err := anonReq.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encoding anon req: %w", err)
	}

	resultCh := make(chan []byte, 1)
	pl := &pendingLogin{
		ch:             resultCh,
		created:        time.Now(),
		sharedSecret:   sharedSecret,
		peerPubKeyByte: peerIdentity.PublicKey()[0],
		peerPubKey:     peerIdentity.PublicKey(),
	}
	rm.loginMu.Lock()
	rm.pendingLogins = append(rm.pendingLogins, pl)
	rm.loginMu.Unlock()

	defer func() {
		rm.loginMu.Lock()
		for i, p := range rm.pendingLogins {
			if p == pl {
				rm.pendingLogins = append(rm.pendingLogins[:i], rm.pendingLogins[i+1:]...)
				break
			}
		}
		rm.loginMu.Unlock()
	}()

	routeType := meshcore.RouteTypeFlood
	var pathLen uint8
	if len(peer.OutPath) > 0 {
		routeType = meshcore.RouteTypeDirect
		hashSize := int(peer.OutPathHashSize)
		if hashSize == 0 {
			hashSize = int(meshcore.PathHashSize)
		}
		pathLen = uint8(hashSize-1)<<6 | uint8(len(peer.OutPath)/hashSize)
	}

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeAnonReq, 0),
		PathLength: pathLen,
		Path:       peer.OutPath,
		Payload:    payload,
	}

	if err := rm.node.SendPacket(pkt); err != nil {
		return nil, fmt.Errorf("sending login: %w", err)
	}

	rm.log.Debug("login sent", "peer", pubkeyHex[:12])

	select {
	case data := <-resultCh:
		isAdmin := len(data) > 6 && data[6] == 1
		rm.mu.Lock()
		rm.sessions[pubkeyHex] = &Session{
			PubKeyHex:    pubkeyHex,
			IsAdmin:      isAdmin,
			LoggedInAt:   time.Now(),
			sharedSecret: sharedSecret,
			localPubKey:  selfIdentity.PublicKey(),
		}
		rm.mu.Unlock()
		return &LoginResult{Success: true, IsAdmin: isAdmin}, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("login timed out")
	}
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

	routeType := meshcore.RouteTypeFlood
	var pathLen uint8
	if len(peer.OutPath) > 0 {
		routeType = meshcore.RouteTypeDirect
		hashSize := int(peer.OutPathHashSize)
		if hashSize == 0 {
			hashSize = int(meshcore.PathHashSize)
		}
		pathLen = uint8(hashSize-1)<<6 | uint8(len(peer.OutPath)/hashSize)
	}

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

func (rm *Client) SendStatusReq(pubkeyHex string, timeout time.Duration) (*Status, error) {
	body := make([]byte, 5)
	body[0] = reqTypeGetStatus
	data, err := rm.sendBinaryRequest(pubkeyHex, body, timeout, "status")
	if err != nil {
		return nil, err
	}
	return parseRepeaterStatus(data)
}

func (rm *Client) SendNeighborsReq(pubkeyHex string, count uint8, offset uint16, timeout time.Duration) (*Neighbors, error) {
	// payload: type(1) request_version(1) count(1) offset(2) order_by(1) prefix_len(1) random(4)
	body := make([]byte, 11)
	body[0] = reqTypeGetNeighbors
	body[1] = 0 // request version
	body[2] = count
	binary.LittleEndian.PutUint16(body[3:5], offset)
	body[5] = 2 // order_by: strongest_to_weakest
	body[6] = 6 // 6-byte pubkey prefix (matches CLI `neighbors` output)
	if _, err := rand.Read(body[7:11]); err != nil {
		return nil, fmt.Errorf("generating random: %w", err)
	}
	data, err := rm.sendBinaryRequest(pubkeyHex, body, timeout, "neighbors")
	if err != nil {
		return nil, err
	}
	return parseRepeaterNeighbors(data, 6)
}

func (rm *Client) SendOwnerInfoReq(pubkeyHex string, timeout time.Duration) (*OwnerInfo, error) {
	body := make([]byte, 5)
	body[0] = reqTypeGetOwnerInfo
	data, err := rm.sendBinaryRequest(pubkeyHex, body, timeout, "owner")
	if err != nil {
		return nil, err
	}
	return parseRepeaterOwnerInfo(data), nil
}

func (rm *Client) SendAccessListReq(pubkeyHex string, timeout time.Duration) (*AccessList, error) {
	// payload: type(1) reserved(2) reserved(2) random(4)
	body := make([]byte, 9)
	body[0] = reqTypeGetAccessList
	if _, err := rand.Read(body[5:9]); err != nil {
		return nil, fmt.Errorf("generating random: %w", err)
	}
	data, err := rm.sendBinaryRequest(pubkeyHex, body, timeout, "access list")
	if err != nil {
		return nil, err
	}
	return parseRepeaterAccessList(data), nil
}

// SetAccessPerm changes the ACL permission byte for a pubkey on the repeater.
// Sets perms=0 to remove. Requires admin session. The pubkey is the full
// 32-byte hex (firmware setperm command requires it).
func (rm *Client) SetAccessPerm(pubkeyHex, targetPubkeyHex string, perms uint8, timeout time.Duration) error {
	cmd := fmt.Sprintf("setperm %s %d", targetPubkeyHex, perms)
	resp, err := rm.SendCLI(pubkeyHex, cmd, timeout)
	if err != nil {
		return err
	}
	resp = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(resp), ">"))
	resp = strings.TrimSpace(resp)
	if !strings.HasPrefix(resp, "OK") {
		return fmt.Errorf("setperm rejected: %s", resp)
	}
	return nil
}

// telemetryReqBody builds the GET_TELEMETRY_DATA payload: type(1) mask(1)
// reserved(3) random(4). Mask 0x00 asks for all; the firmware filters by ACL.
func telemetryReqBody() ([]byte, error) {
	body := make([]byte, 9)
	body[0] = reqTypeGetTelemetryData
	if _, err := rand.Read(body[5:9]); err != nil {
		return nil, fmt.Errorf("generating random: %w", err)
	}
	return body, nil
}

func (rm *Client) SendTelemetryReq(pubkeyHex string, timeout time.Duration) (*telemetry.Telemetry, error) {
	body, err := telemetryReqBody()
	if err != nil {
		return nil, err
	}
	data, err := rm.sendBinaryRequest(pubkeyHex, body, timeout, "telemetry")
	if err != nil {
		return nil, err
	}
	return telemetry.Parse(data)
}

// SendContactTelemetryReq requests telemetry from any contact without a login,
// encrypting with the ECDH secret between this companion and the contact.
func (rm *Client) SendContactTelemetryReq(pubkeyHex string, timeout time.Duration) (*telemetry.Telemetry, error) {
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

	self := rm.node.Identity()
	selfSeed := self.Seed()
	sharedSecret, err := meshcore.DeriveSharedSecret(selfSeed[:], peerIdentity.PublicKeyBytes())
	if err != nil {
		return nil, fmt.Errorf("deriving shared secret: %w", err)
	}

	body, err := telemetryReqBody()
	if err != nil {
		return nil, err
	}
	data, err := rm.roundtripRequest(peerIdentity.PublicKey(), peer, sharedSecret, self.PublicKey()[0], body, timeout, "contact-telemetry", true)
	if err != nil {
		return nil, err
	}
	return telemetry.Parse(data)
}

func (rm *Client) SendCLI(pubkeyHex, command string, timeout time.Duration) (string, error) {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return "", fmt.Errorf("invalid pubkey: %w", err)
	}

	peer := rm.node.Peers().Lookup(peerIdentity.PublicKey())
	if peer == nil {
		return "", fmt.Errorf("peer not found in peer table")
	}

	rm.mu.Lock()
	sess := rm.sessions[pubkeyHex]
	rm.mu.Unlock()
	if sess == nil || sess.sharedSecret == nil {
		return "", fmt.Errorf("not logged in to this repeater")
	}

	var prefixBytes [1]byte
	rand.Read(prefixBytes[:])
	prefix := fmt.Sprintf("%02X", prefixBytes[0])
	framedCommand := prefix + "|" + command

	resultCh := make(chan string, 1)
	rm.cliMu.Lock()
	rm.cliPending[prefix] = resultCh
	rm.cliMu.Unlock()

	defer func() {
		rm.cliMu.Lock()
		delete(rm.cliPending, prefix)
		rm.cliMu.Unlock()
	}()

	plaintext := meshcore.BuildTextPlaintext(time.Now(), txtTypeCliData<<2, []byte(framedCommand))

	encrypted, err := meshcore.EncryptThenMAC(sess.sharedSecret, plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypting CLI: %w", err)
	}

	var mac [2]byte
	copy(mac[:], encrypted[:2])

	txtMsg := &meshcore.TextMessage{
		Destination:      peerIdentity.PublicKey()[0],
		Source:           sess.localPubKey[0],
		MAC:              mac,
		EncryptedPayload: encrypted[2:],
	}

	msgBytes, err := txtMsg.ToBytes()
	if err != nil {
		return "", fmt.Errorf("encoding text message: %w", err)
	}

	routeType := meshcore.RouteTypeFlood
	var pathLen uint8
	if len(peer.OutPath) > 0 {
		routeType = meshcore.RouteTypeDirect
		hashSize := int(peer.OutPathHashSize)
		if hashSize == 0 {
			hashSize = int(meshcore.PathHashSize)
		}
		pathLen = uint8(hashSize-1)<<6 | uint8(len(peer.OutPath)/hashSize)
	}

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeTxtMsg, 0),
		PathLength: pathLen,
		Path:       peer.OutPath,
		Payload:    msgBytes,
	}

	if err := rm.node.SendPacket(pkt); err != nil {
		return "", fmt.Errorf("sending CLI: %w", err)
	}

	rm.log.Debug("CLI sent", "peer", pubkeyHex[:12], "prefix", prefix, "command", command)

	select {
	case response := <-resultCh:
		return response, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("CLI command timed out")
	}
}

func (rm *Client) HandlePathPacket(pkt *meshcore.Packet) bool {
	path, err := meshcore.PathFromBytes(pkt.Payload)
	if err != nil {
		rm.log.Debug("HandlePathPacket: failed to parse", "error", err)
		return false
	}

	rm.loginMu.Lock()
	rm.log.Debug("HandlePathPacket: checking pending logins",
		"pathDest", fmt.Sprintf("%02x", path.Destination),
		"pathSrc", fmt.Sprintf("%02x", path.Source),
		"pendingCount", len(rm.pendingLogins))
	for i, pl := range rm.pendingLogins {
		if path.Source != pl.peerPubKeyByte {
			rm.log.Debug("HandlePathPacket: source mismatch",
				"pathSrc", fmt.Sprintf("%02x", path.Source),
				"expected", fmt.Sprintf("%02x", pl.peerPubKeyByte))
			continue
		}
		if !path.VerifyMAC(pl.sharedSecret) {
			rm.log.Debug("HandlePathPacket: MAC verify failed")
			continue
		}
		plaintext := path.Decrypt(pl.sharedSecret)
		rm.pendingLogins = append(rm.pendingLogins[:i], rm.pendingLogins[i+1:]...)
		rm.loginMu.Unlock()

		rm.log.Debug("HandlePathPacket: login path decrypted",
			"plaintextLen", len(plaintext),
			"plaintextHex", hex.EncodeToString(plaintext))

		if len(plaintext) < 2 {
			rm.log.Debug("HandlePathPacket: plaintext too short")
			return true
		}

		pathLenByte := plaintext[0]
		pathHashSize := int((pathLenByte>>6)&3) + 1
		hopCount := int(pathLenByte & 63)
		pathDataLen := hopCount * pathHashSize

		if len(plaintext) < 1+pathDataLen+1 {
			rm.log.Debug("HandlePathPacket: plaintext too short for path+extra",
				"need", 1+pathDataLen+1, "have", len(plaintext))
			return true
		}

		returnPath := plaintext[1 : 1+pathDataLen]
		extraType := plaintext[1+pathDataLen]
		extraData := plaintext[1+pathDataLen+1:]

		rm.log.Debug("HandlePathPacket: parsed path return",
			"hops", hopCount, "hashSize", pathHashSize,
			"pathHex", hex.EncodeToString(returnPath),
			"extraType", fmt.Sprintf("%02x", extraType),
			"extraDataLen", len(extraData))

		if len(returnPath) > 0 {
			rm.log.Debug("path return received", "hops", hopCount, "hashSize", pathHashSize, "pathHex", hex.EncodeToString(returnPath))
			rm.node.Peers().SetOutPath(pl.peerPubKey, returnPath, uint8(pathHashSize))
		} else {
			rm.log.Debug("path return received: direct neighbor (0 hops)")
			rm.node.Peers().SetOutPath(pl.peerPubKey, []byte{}, uint8(pathHashSize))
		}
		rm.persistOutPath(pl.peerPubKey[:], returnPath, uint8(pathHashSize))

		if extraType == meshcore.PayloadTypeResponse && len(extraData) > 0 {
			rm.log.Debug("HandlePathPacket: delivering response to login channel", "extraDataLen", len(extraData))
			select {
			case pl.ch <- extraData:
			default:
				rm.log.Debug("HandlePathPacket: login channel full, dropped response")
			}
		} else {
			rm.log.Debug("HandlePathPacket: no response extra data",
				"extraType", fmt.Sprintf("%02x", extraType),
				"wantType", fmt.Sprintf("%02x", meshcore.PayloadTypeResponse))
		}
		return true
	}
	rm.loginMu.Unlock()

	rm.mu.Lock()
	sessions := make([]*Session, 0, len(rm.sessions))
	for _, s := range rm.sessions {
		sessions = append(sessions, s)
	}
	rm.mu.Unlock()

	for _, sess := range sessions {
		if path.Destination != sess.localPubKey[0] {
			continue
		}
		if !path.VerifyMAC(sess.sharedSecret) {
			continue
		}
		plaintext := path.Decrypt(sess.sharedSecret)
		if len(plaintext) < 2 {
			return true
		}

		pathLenByte := plaintext[0]
		pathHashSize := int((pathLenByte>>6)&3) + 1
		hopCount := int(pathLenByte & 63)
		pathDataLen := hopCount * pathHashSize

		if len(plaintext) < 1+pathDataLen+1 {
			return true
		}

		returnPath := plaintext[1 : 1+pathDataLen]
		extraType := plaintext[1+pathDataLen]
		extraData := plaintext[1+pathDataLen+1:]

		if len(returnPath) > 0 {
			rm.log.Debug("path return received (session)", "hops", hopCount, "hashSize", pathHashSize, "pathHex", hex.EncodeToString(returnPath))
			pubkeyBytes, _ := hex.DecodeString(sess.PubKeyHex)
			if len(pubkeyBytes) == 32 {
				var pubkey [32]byte
				copy(pubkey[:], pubkeyBytes)
				rm.node.Peers().SetOutPath(pubkey, returnPath, uint8(pathHashSize))
				rm.persistOutPath(pubkeyBytes, returnPath, uint8(pathHashSize))
			}
		}

		if extraType == meshcore.PayloadTypeResponse && len(extraData) >= 4 {
			tag := binary.LittleEndian.Uint32(extraData[:4])
			data := extraData[4:]

			rm.pendingMu.Lock()
			pr, ok := rm.pending[tag]
			rm.pendingMu.Unlock()
			if ok {
				select {
				case pr.ch <- data:
				default:
				}
			}
		}
		return true
	}

	// Sessionless PATH-wrapped responses (flood contact telemetry): match by
	// carried secret, learn any return path, deliver by tag.
	rm.pendingMu.Lock()
	pendings := make([]*pendingRequest, 0, len(rm.pending))
	for _, pr := range rm.pending {
		if pr.sharedSecret != nil {
			pendings = append(pendings, pr)
		}
	}
	rm.pendingMu.Unlock()

	for _, pr := range pendings {
		if path.Source != pr.peerPubKeyByte {
			continue
		}
		if !path.VerifyMAC(pr.sharedSecret) {
			continue
		}
		plaintext := path.Decrypt(pr.sharedSecret)
		if len(plaintext) < 2 {
			return true
		}

		pathLenByte := plaintext[0]
		pathHashSize := int((pathLenByte>>6)&3) + 1
		hopCount := int(pathLenByte & 63)
		pathDataLen := hopCount * pathHashSize
		if len(plaintext) < 1+pathDataLen+1 {
			return true
		}

		returnPath := plaintext[1 : 1+pathDataLen]
		extraType := plaintext[1+pathDataLen]
		extraData := plaintext[1+pathDataLen+1:]

		if len(returnPath) > 0 {
			rm.node.Peers().SetOutPath(pr.peerPubKey, returnPath, uint8(pathHashSize))
			rm.persistOutPath(pr.peerPubKey[:], returnPath, uint8(pathHashSize))
		}

		if extraType == meshcore.PayloadTypeResponse && len(extraData) >= 4 {
			tag := binary.LittleEndian.Uint32(extraData[:4])
			data := extraData[4:]

			rm.pendingMu.Lock()
			target, ok := rm.pending[tag]
			rm.pendingMu.Unlock()
			if ok {
				select {
				case target.ch <- data:
				default:
				}
			}
		}
		return true
	}
	return false
}

func (rm *Client) HandleResponsePacket(pkt *meshcore.Packet) {
	resp, err := meshcore.ResponseFromBytes(pkt.Payload)
	if err != nil {
		rm.log.Debug("failed to parse response", "error", err)
		return
	}

	rm.loginMu.Lock()
	for i, pl := range rm.pendingLogins {
		if resp.Source != pl.peerPubKeyByte {
			continue
		}
		if !resp.VerifyMAC(pl.sharedSecret) {
			continue
		}
		plaintext := resp.Decrypt(pl.sharedSecret)
		rm.pendingLogins = append(rm.pendingLogins[:i], rm.pendingLogins[i+1:]...)
		rm.loginMu.Unlock()
		if plaintext != nil {
			select {
			case pl.ch <- plaintext:
			default:
			}
		}
		return
	}
	rm.loginMu.Unlock()

	// Sessionless requests: match by carried secret + source, confirm echoed tag.
	rm.pendingMu.Lock()
	for tag, pr := range rm.pending {
		if pr.sharedSecret == nil || resp.Source != pr.peerPubKeyByte {
			continue
		}
		if !resp.VerifyMAC(pr.sharedSecret) {
			continue
		}
		plaintext := resp.Decrypt(pr.sharedSecret)
		if len(plaintext) < 4 || binary.LittleEndian.Uint32(plaintext[:4]) != tag {
			continue
		}
		ch := pr.ch
		rm.pendingMu.Unlock()
		select {
		case ch <- plaintext[4:]:
		default:
		}
		return
	}
	rm.pendingMu.Unlock()

	rm.mu.Lock()
	sessions := make([]*Session, 0, len(rm.sessions))
	for _, s := range rm.sessions {
		sessions = append(sessions, s)
	}
	rm.mu.Unlock()

	for _, sess := range sessions {
		if resp.Source != sess.localPubKey[0] && resp.Destination != sess.localPubKey[0] {
			continue
		}
		if !resp.VerifyMAC(sess.sharedSecret) {
			continue
		}
		plaintext := resp.Decrypt(sess.sharedSecret)
		if len(plaintext) < 4 {
			continue
		}

		tag := binary.LittleEndian.Uint32(plaintext[:4])
		data := plaintext[4:]

		rm.pendingMu.Lock()
		pr, ok := rm.pending[tag]
		rm.pendingMu.Unlock()
		if ok {
			select {
			case pr.ch <- data:
			default:
			}
		}
		return
	}
}

func (rm *Client) HandleCLIResponse(senderPubKey [32]byte, text string) {
	if len(text) < cliPrefixLen || text[2] != '|' {
		return
	}
	prefix := text[:2]
	response := text[3:]

	rm.cliMu.Lock()
	ch, ok := rm.cliPending[prefix]
	rm.cliMu.Unlock()

	if ok {
		select {
		case ch <- response:
		default:
		}
	}
}

func (rm *Client) HandleTextPacket(pkt *meshcore.Packet) bool {
	txtMsg, err := meshcore.TextMessageFromBytes(pkt.Payload)
	if err != nil {
		return false
	}

	rm.mu.Lock()
	sessions := make([]*Session, 0, len(rm.sessions))
	for _, s := range rm.sessions {
		sessions = append(sessions, s)
	}
	rm.mu.Unlock()

	for _, sess := range sessions {
		if txtMsg.Destination != sess.localPubKey[0] {
			continue
		}
		if !txtMsg.VerifyMAC(sess.sharedSecret) {
			continue
		}
		plaintext := txtMsg.Decrypt(sess.sharedSecret)
		if len(plaintext) < 5 {
			continue
		}

		flags := plaintext[4] >> 2
		if flags != txtTypeCliData {
			continue
		}

		text := strings.TrimRight(string(plaintext[5:]), "\x00")
		rm.HandleCLIResponse([32]byte{}, text)
		return true
	}
	return false
}

func parseRepeaterNeighbors(data []byte, prefixLen int) (*Neighbors, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("neighbors data too short: got %d", len(data))
	}
	out := &Neighbors{
		TotalCount:   int(int16(binary.LittleEndian.Uint16(data[0:2]))),
		ResultsCount: int(int16(binary.LittleEndian.Uint16(data[2:4]))),
	}
	entrySize := prefixLen + 4 + 1
	pos := 4
	for i := 0; i < out.ResultsCount; i++ {
		if pos+entrySize > len(data) {
			break
		}
		secsAgo := binary.LittleEndian.Uint32(data[pos+prefixLen : pos+prefixLen+4])
		snr := int(int8(data[pos+prefixLen+4]))
		out.Neighbors = append(out.Neighbors, Neighbor{
			PubkeyPrefix: hex.EncodeToString(data[pos : pos+prefixLen]),
			SecsAgo:      secsAgo,
			SNR:          snr,
		})
		pos += entrySize
	}
	return out, nil
}

func parseRepeaterAccessList(data []byte) *AccessList {
	out := &AccessList{Entries: []AccessListEntry{}}
	const stride = 7 // 6-byte prefix + 1-byte permissions
	for pos := 0; pos+stride <= len(data); pos += stride {
		out.Entries = append(out.Entries, AccessListEntry{
			PubkeyPrefix: hex.EncodeToString(data[pos : pos+6]),
			Permissions:  data[pos+6],
		})
	}
	return out
}

func parseRepeaterOwnerInfo(data []byte) *OwnerInfo {
	text := strings.TrimRight(string(data), "\x00")
	parts := strings.SplitN(text, "\n", 3)
	info := &OwnerInfo{}
	if len(parts) > 0 {
		info.FirmwareVersion = parts[0]
	}
	if len(parts) > 1 {
		info.NodeName = parts[1]
	}
	if len(parts) > 2 {
		info.OwnerInfo = parts[2]
	}
	return info
}

func parseRepeaterStatus(data []byte) (*Status, error) {
	if len(data) < 52 {
		return nil, fmt.Errorf("status data too short: got %d, need at least 52", len(data))
	}

	s := &Status{
		BatteryMV:   binary.LittleEndian.Uint16(data[0:2]),
		QueueLen:    binary.LittleEndian.Uint16(data[2:4]),
		NoiseFloor:  int16(binary.LittleEndian.Uint16(data[4:6])),
		LastRSSI:    int16(binary.LittleEndian.Uint16(data[6:8])),
		PacketsRecv: binary.LittleEndian.Uint32(data[8:12]),
		PacketsSent: binary.LittleEndian.Uint32(data[12:16]),
		TxAirSecs:   binary.LittleEndian.Uint32(data[16:20]),
		UptimeSecs:  binary.LittleEndian.Uint32(data[20:24]),
		FloodTx:     binary.LittleEndian.Uint32(data[24:28]),
		DirectTx:    binary.LittleEndian.Uint32(data[28:32]),
		FloodRx:     binary.LittleEndian.Uint32(data[32:36]),
		DirectRx:    binary.LittleEndian.Uint32(data[36:40]),
		ErrEvents:   binary.LittleEndian.Uint16(data[40:42]),
		LastSNR:     float64(int16(binary.LittleEndian.Uint16(data[42:44]))) / 4.0,
		DirectDups:  binary.LittleEndian.Uint16(data[44:46]),
		FloodDups:   binary.LittleEndian.Uint16(data[46:48]),
	}

	if len(data) >= 52 {
		rxAirSecs := binary.LittleEndian.Uint32(data[48:52])
		s.RxAirSecs = rxAirSecs
		if s.UptimeSecs > 0 {
			s.ChanUtil = float64(s.TxAirSecs+rxAirSecs) / float64(s.UptimeSecs) * 100
		}
	}
	if len(data) >= 56 {
		s.RecvErrors = binary.LittleEndian.Uint32(data[52:56])
	}

	return s, nil
}
