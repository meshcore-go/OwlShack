package repeater

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
)

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
