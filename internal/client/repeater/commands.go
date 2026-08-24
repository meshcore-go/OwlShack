package repeater

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/telemetry"
)

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

	routeType, pathLen := routeForPeer(peer)

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
