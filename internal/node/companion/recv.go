package companion

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/store"
	"github.com/meshcore-go/OwlShack/internal/trigger"
)

const dmAckDelay = 200 * time.Millisecond

// txtTypePlain (TXT_TYPE_PLAIN): the text type is the upper 6 bits of the flags byte — plaintext[4]>>2 for DMs, payload.Flags>>2 for channel text.
const txtTypePlain = 0

// txtTypeCliData (TXT_TYPE_CLI_DATA) marks a repeater CLI reply.
const txtTypeCliData = 1

// txtTypeSignedPlain (TXT_TYPE_SIGNED_PLAIN) marks a post pushed by a room server.
const txtTypeSignedPlain = 2

// sendDMAck's ackPayload is the firmware's ack bytes: 6 ([crc][attempt][random]) for a plain DM, a bare 4-byte CRC for a room post push.
func (c *Companion) sendDMAck(pkt *meshcore.Packet, senderPubKey []byte, sharedSecret []byte, ackPayload []byte) {
	if pkt.IsRouteFlood() {
		pathReturn, err := c.buildPathReturn(senderPubKey, sharedSecret, pkt.Path, pkt.PathLength, meshcore.PayloadTypeAck, ackPayload)
		if err != nil {
			c.log.Debug("failed to build path return for DM ACK", "error", err)
			return
		}
		if err := c.node.SendPacketDelayed(pathReturn, node.PriorityFloodRelay, dmAckDelay); err != nil {
			c.log.Debug("failed to send DM ACK (path return)", "error", err)
		}
	} else {
		// An empty but non-nil out_path is a direct neighbour: route it direct at 0 hops; only a nil path floods.
		ackPkt := &meshcore.Packet{
			Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAck, 0),
			Payload: ackPayload,
		}

		if len(senderPubKey) == 32 {
			var pubkey [32]byte
			copy(pubkey[:], senderPubKey)
			peer := c.node.Peers().Lookup(pubkey)
			if peer != nil && peer.OutPath != nil {
				hs := max(peer.OutPathHashSize, 1)
				ackPkt.Header = meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeAck, 0)
				ackPkt.Path = peer.OutPath
				ackPkt.PathLength = (hs-1)<<6 | byte(len(peer.OutPath)/int(hs))
			}
		}

		if err := c.node.SendPacketDelayed(ackPkt, node.PriorityFloodRelay, dmAckDelay); err != nil {
			c.log.Debug("failed to send DM ACK", "error", err)
		}
	}
}

// buildPathReturn matches the firmware's Path payload: [dest_hash:1][src_hash:1][MAC:2][encrypted([pathLenByte][path_data][extra_type][extra_data])].
func (c *Companion) buildPathReturn(destPubKey []byte, sharedSecret []byte, inPath []byte, pathLenByte byte, extraType byte, extraData []byte) (*meshcore.Packet, error) {
	pathHashSize := int((pathLenByte>>6)&3) + 1
	pathHashCount := int(pathLenByte & 63)
	pathDataLen := pathHashCount * pathHashSize
	if pathDataLen > len(inPath) {
		pathDataLen = len(inPath)
	}

	plain := make([]byte, 0, 1+pathDataLen+1+len(extraData))
	plain = append(plain, pathLenByte)
	plain = append(plain, inPath[:pathDataLen]...)
	plain = append(plain, extraType)
	plain = append(plain, extraData...)

	encrypted, err := meshcore.EncryptThenMAC(sharedSecret, plain)
	if err != nil {
		return nil, fmt.Errorf("encrypting path return: %w", err)
	}

	selfPubKey := c.node.Identity().PublicKey()
	payload := make([]byte, 0, meshcore.PathHashSize+meshcore.PathHashSize+len(encrypted))
	payload = append(payload, destPubKey[:meshcore.PathHashSize]...)
	payload = append(payload, selfPubKey[:meshcore.PathHashSize]...)
	payload = append(payload, encrypted...)

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0),
		PathLength: (meshcore.PathHashSize - 1) << 6,
		Payload:    payload,
	}
	return pkt, nil
}

// handleRoomPush handles a TXT_TYPE_SIGNED_PLAIN room post: [post_timestamp:4][flags:1][author_pubkey_prefix:4][text:N].
func (c *Companion) handleRoomPush(pkt *meshcore.Packet, roomPubKey []byte, roomPubKeyHex string, sharedSecret []byte, plaintext []byte) {
	if len(plaintext) < 9 {
		c.log.Debug("room push plaintext too short", "room", roomPubKeyHex[:12])
		return
	}

	postTs := binary.LittleEndian.Uint32(plaintext[:4])
	authorPrefix := plaintext[5:9]
	text := strings.TrimRight(string(plaintext[9:]), "\x00")

	// Pushes are ACK-gated: the server sends no further post until it sees a CRC hashed with OUR pubkey, and each retry needs its own ACK.
	selfPubKey := c.node.Identity().PublicKey()
	ack := make([]byte, 4)
	binary.LittleEndian.PutUint32(ack, meshcore.CalcAckHash(plaintext[:9+len(text)], selfPubKey[:]))
	c.sendDMAck(pkt, roomPubKey, sharedSecret, ack)

	channelKey := "dm:" + roomPubKeyHex

	// The server's backlog holds at most 32 posts, so 50 recent rows cover the resync window.
	recent, err := c.store.Messages.List(c.runCtx, c.cfg.ID, channelKey, 50, 0)
	if err == nil {
		for _, m := range recent {
			if m.Direction == "rx" && m.Timestamp.Unix() == int64(postTs) && m.Text == text {
				c.log.Debug("room push duplicate ignored", "room", roomPubKeyHex[:12], "postTs", postTs)
				return
			}
		}
	}

	authorName := hex.EncodeToString(authorPrefix) + "…"
	if names, lookupErr := c.store.Peers.LookupByHash(c.runCtx, authorPrefix); lookupErr == nil && len(names) > 0 {
		authorName = names[0]
	}

	msg := &store.Message{
		CompanionID: c.cfg.ID,
		Channel:     channelKey,
		ChannelHash: 0,
		Sender:      authorName,
		Text:        text,
		Direction:   "rx",
		Timestamp:   time.Unix(int64(postTs), 0),
	}
	if pkt.HasSignalInfo {
		snr := float64(pkt.SNR)
		rssi := pkt.RSSI
		msg.SNR = &snr
		msg.RSSI = &rssi
	}

	c.store.WriteAsync(func() {
		if insertErr := c.store.Messages.Insert(context.Background(), msg); insertErr != nil {
			c.log.Error("failed to persist room post", "error", insertErr)
		}

		c.log.Info("room post received", "room", roomPubKeyHex[:12], "author", authorName, "text", text)

		if c.hub != nil {
			wsMsg := map[string]any{
				"companion": c.cfg.Name,
				"channel":   channelKey,
				"sender":    authorName,
				"text":      text,
				"direction": "rx",
				"timestamp": msg.Timestamp.UTC().Format(time.RFC3339),
				"id":        msg.ID,
			}
			if pkt.HasSignalInfo {
				wsMsg["snr"] = pkt.SNR
				wsMsg["rssi"] = pkt.RSSI
			}
			c.hub.Broadcast("messages", wsMsg)
		}
	})
}

// handleDMPathReturn tries every contact's shared secret against a Path packet, then stores the return path and feeds any embedded ACK to the tracker.
func (c *Companion) handleDMPathReturn(pkt *meshcore.Packet) {
	path, err := meshcore.PathFromBytes(pkt.Payload)
	if err != nil {
		return
	}

	selfPubKey := c.node.Identity().PublicKey()
	if path.Destination != selfPubKey[0] {
		return
	}

	contacts, err := c.store.Contacts.List(c.runCtx, c.cfg.ID)
	if err != nil {
		return
	}

	for _, ct := range contacts {
		if len(ct.PeerPubKey) == 0 || ct.PeerPubKey[0] != path.Source {
			continue
		}
		peerID, err := meshcore.NewIdentityFromBytes(ct.PeerPubKey)
		if err != nil {
			continue
		}
		secret, err := c.node.SharedSecret(peerID)
		if err != nil {
			continue
		}
		if !path.VerifyMAC(secret) {
			continue
		}
		pp, perr := meshcore.ParsePathPayload(path.Decrypt(secret))
		if perr != nil {
			return
		}
		returnPath, extraType, extraData := pp.Path, pp.ExtraType, pp.Extra

		c.log.Debug("DM path return received",
			"peer", hex.EncodeToString(ct.PeerPubKey[:6]),
			"hops", pp.PathHashCount(),
			"pathHex", hex.EncodeToString(returnPath))
		var pubkey [32]byte
		copy(pubkey[:], ct.PeerPubKey)
		c.node.Peers().SetOutPath(pubkey, returnPath, pp.PathHashSize())
		hs := pp.PathHashSize()
		c.store.WriteAsync(func() {
			_ = c.store.Contacts.UpdateOutPath(context.Background(), c.cfg.ID, ct.PeerPubKey, returnPath, hs)
		})

		if extraType == meshcore.PayloadTypeAck && len(extraData) >= 4 {
			ackCRC := binary.LittleEndian.Uint32(extraData[:4])
			c.node.NotifyACK(ackCRC)
			c.log.Debug("DM ACK received via path return",
				"peer", hex.EncodeToString(ct.PeerPubKey[:6]),
				"ackCRC", fmt.Sprintf("%08x", ackCRC))
		}
		return
	}
}

func (c *Companion) registerPacketHandlers() {
	radio := *c.radio

	radio.SetRawDataHandler(func(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
		if c.echoTracker != nil {
			c.echoTracker.OnRawPacket(data, snr, rssi, hasSignalInfo)
		}
	})

	radio.AddOutboundHandler(func(data []byte) {
		if c.echoTracker == nil {
			return
		}
		pkt, err := meshcore.PacketFromBytes(data)
		if err != nil || pkt.PayloadType() != meshcore.PayloadTypeGrpTxt {
			return
		}

		c.pendingOutbound.Lock()
		msgID := c.pendingOutbound.msgID
		channel := c.pendingOutbound.channel
		c.pendingOutbound.msgID = 0
		c.pendingOutbound.channel = ""
		c.pendingOutbound.Unlock()

		if msgID == 0 {
			return
		}
		c.echoTracker.Track(pkt.PacketHash(), msgID, c.cfg.Name, channel)
	})

	c.node.OnPacket(meshcore.PayloadTypeAdvert, func(pkt *meshcore.Packet) {
		adv, err := meshcore.AdvertFromBytes(pkt.Payload)
		if err != nil {
			return
		}
		if !adv.Verify() {
			return
		}

		appData := adv.AppData()
		p := &store.Peer{
			PubKey:          adv.PublicKey.PublicKeyBytes(),
			Name:            appData.Name,
			Type:            appData.Type,
			Lat:             appData.Lat,
			Lon:             appData.Lon,
			Feat1:           appData.Feat1,
			Feat2:           appData.Feat2,
			OutPath:         pkt.Path,
			OutPathHashSize: pkt.PathHashSize(),
			LastAdvertTS:    adv.Timestamp,
			LastSeen:        time.Now(),
		}
		if pkt.HasSignalInfo {
			snr := float64(pkt.SNR)
			p.SNR = &snr
			p.RSSI = &pkt.RSSI
		}

		c.store.WriteAsync(func() {
			if err := c.store.Peers.Upsert(context.Background(), p); err != nil {
				c.log.Error("failed to persist peer", "error", err)
				return
			}

			// Advert wins on location, but only when it carries one — a no-GPS advert leaves a hand-set location alone.
			hasLoc := p.HasLocation()
			if err := c.store.Contacts.RefreshFromAdvert(
				context.Background(), p.PubKey, appData.Name, appData.Type,
				p.Lat, p.Lon, p.Feat1, p.Feat2, p.LastSeen, p.LastAdvertTS, hasLoc,
			); err != nil {
				c.log.Error("failed to refresh contact from advert", "error", err)
			}

			c.log.Debug("peer persisted",
				"name", appData.Name,
				"type", appData.Type,
				"pubkey", hex.EncodeToString(p.PubKey[:8]),
			)

			if c.hub != nil {
				c.hub.Broadcast("peers", map[string]any{
					"pubkey":          hex.EncodeToString(p.PubKey),
					"name":            p.Name,
					"type":            p.Type,
					"lat":             p.Lat,
					"lon":             p.Lon,
					"snr":             p.SNR,
					"rssi":            p.RSSI,
					"outPath":         hex.EncodeToString(p.OutPath),
					"outPathHashSize": p.OutPathHashSize,
					"lastAdvertTs":    p.LastAdvertTS,
					"lastSeen":        p.LastSeen.Format(time.RFC3339),
				})
			}
		})
	})

	c.node.OnPacket(meshcore.PayloadTypeGrpTxt, func(pkt *meshcore.Packet) {
		payload, ch, err := c.node.DecryptGroupText(pkt)
		if err != nil {
			return
		}

		if payload.Sender == c.cfg.Name {
			return
		}

		// Firmware gate (BaseChatMesh::onGroupDataRecv): real chat is TXT_TYPE_PLAIN, dropping ~63/64 of the garbage a 1-byte-hash channel collision decrypts to.
		if txtType := payload.Flags >> 2; txtType != txtTypePlain {
			c.log.Debug("dropping non-plain channel message",
				"channel", ch.Name, "channelHash", ch.Hash, "txtType", txtType)
			return
		}
		// Beyond the firmware: a shared-PSK channel carries genuinely binary traffic too, so keep only text.
		if !utf8.ValidString(payload.Text) || !utf8.ValidString(payload.Sender) {
			c.log.Debug("dropping non-text channel message",
				"channel", ch.Name, "channelHash", ch.Hash, "bytes", len(payload.Text))
			return
		}

		var snrPtr *float64
		var rssiPtr *int8
		if pkt.HasSignalInfo {
			snr := float64(pkt.SNR)
			snrPtr = &snr
			rssiPtr = &pkt.RSSI
		}

		hops := int(pkt.PathHashCount())
		pathHashSize := int(pkt.PathHashSize())

		msg := &store.Message{
			CompanionID:  c.cfg.ID,
			Channel:      ch.Name,
			ChannelHash:  ch.Hash,
			Sender:       payload.Sender,
			Text:         payload.Text,
			Direction:    "rx",
			Timestamp:    time.Unix(int64(payload.Timestamp), 0),
			SNR:          snrPtr,
			RSSI:         rssiPtr,
			PathHashes:   pkt.Path,
			PathHashSize: &pathHashSize,
			Hops:         &hops,
		}

		convoID := "channel:" + ch.Name
		if blocked, err := c.store.BlockedSenders.IsBlocked(c.runCtx, c.cfg.ID, convoID, payload.Sender); err == nil && blocked {
			c.log.Debug("blocked sender filtered", "sender", payload.Sender, "channel", ch.Name)
			return
		}

		c.store.WriteAsync(func() {
			if err := c.store.Messages.Insert(context.Background(), msg); err != nil {
				c.log.Error("failed to persist message", "error", err)
				return
			}

			if c.echoTracker != nil && msg.ID != 0 {
				c.echoTracker.Track(pkt.PacketHash(), msg.ID, c.cfg.Name, ch.Name)
			}

			c.log.Debug("message received",
				"channel", ch.Name,
				"sender", payload.Sender,
			)

			if c.hub != nil {
				wsMsg := map[string]any{
					"companion":    c.cfg.Name,
					"channel":      ch.Name,
					"sender":       payload.Sender,
					"text":         payload.Text,
					"direction":    "rx",
					"timestamp":    msg.Timestamp.UTC().Format(time.RFC3339),
					"id":           msg.ID,
					"hops":         hops,
					"pathHashSize": pathHashSize,
				}
				if pkt.HasSignalInfo {
					wsMsg["snr"] = pkt.SNR
					wsMsg["rssi"] = pkt.RSSI
				}
				c.hub.Broadcast("messages", wsMsg)
			}
		})
	})

	// One persistent handler (node.OnPacket has no deregistration) registered after the rx-persist handler, so a trigger's reply gets a higher row id than the message it answers.
	c.node.OnPacket(meshcore.PayloadTypeGrpTxt, func(pkt *meshcore.Packet) {
		c.mu.Lock()
		entries := c.triggers
		c.mu.Unlock()
		for _, e := range entries {
			if h, ok := e.trigger.(groupTextHandler); ok {
				h.HandleGroupText(pkt)
			}
		}
	})

	c.node.OnPacket(meshcore.PayloadTypeTxtMsg, func(pkt *meshcore.Packet) {
		if c.repeaters.HandleTextPacket(pkt) {
			return
		}

		txtMsg, err := meshcore.TextMessageFromBytes(pkt.Payload)
		if err != nil {
			c.log.Debug("failed to parse text message", "error", err)
			return
		}

		selfPubKey := c.node.Identity().PublicKey()
		if txtMsg.Destination != selfPubKey[0] {
			return
		}

		var senderPubKeyHex string
		var senderPubKey []byte
		var senderName string
		var senderIsContact bool
		var sharedSecret []byte
		var plaintext []byte

		for _, cand := range c.dmCandidates(txtMsg.Source) {
			peerID, err := meshcore.NewIdentityFromBytes(cand.pubkey)
			if err != nil {
				continue
			}
			secret, err := c.node.SharedSecret(peerID)
			if err != nil {
				continue
			}
			if !txtMsg.VerifyMAC(secret) {
				continue
			}
			plaintext = txtMsg.Decrypt(secret)
			senderPubKey = cand.pubkey
			senderPubKeyHex = hex.EncodeToString(cand.pubkey)
			senderIsContact = cand.isContact
			sharedSecret = secret
			senderName = cand.name
			if senderName == "" {
				senderName = senderPubKeyHex[:12] + "…"
			}
			break
		}

		if plaintext == nil {
			c.log.Debug("could not decrypt DM from any known peer")
			return
		}

		if len(plaintext) < 5 {
			c.log.Debug("DM plaintext too short")
			return
		}

		flags := plaintext[4] >> 2
		text := strings.TrimRight(string(plaintext[5:]), "\x00")

		switch flags {
		case txtTypeCliData:
			var senderKey [32]byte
			copy(senderKey[:], senderPubKey)
			c.repeaters.HandleCLIResponse(senderKey, text)
			if pkt.IsRouteFlood() { // firmware: teach the sender our path (no ACK as extra)
				if pr, err := c.buildPathReturn(senderPubKey, sharedSecret, pkt.Path, pkt.PathLength, 0, nil); err == nil {
					if err := c.node.SendPacketDelayed(pr, node.PriorityFloodRelay, 0); err != nil {
						c.log.Debug("failed to send CLI path return", "error", err)
					}
				}
			}
			return
		case txtTypeSignedPlain:
			c.handleRoomPush(pkt, senderPubKey, senderPubKeyHex, sharedSecret, plaintext)
			return
		case txtTypePlain:
		default:
			c.log.Debug("unsupported DM text type", "flags", flags)
			return
		}

		// Gate before the ACK, so a sender the policy turns away sees a failed send rather than silence.
		if !c.cfg.AllowsDMFrom(senderPubKeyHex, senderIsContact) {
			c.log.Info("DM rejected", "from", senderName, "policy", c.cfg.DMPolicyOrDefault())
			return
		}

		// Mirrors the firmware, which files any sender it can decrypt in contacts[]; here it is also what puts the thread in the conversation list.
		if !senderIsContact {
			c.addDMSenderAsContact(senderPubKey, senderName)
		}

		// Plain-DM ack payload: [crc:4][attempt][random]
		ackPlaintext := plaintext[:5+len(text)]
		var attemptByte byte
		if 5+len(text)+1 < len(plaintext) {
			attemptByte = plaintext[5+len(text)+1]
		}
		var randomByte [1]byte
		rand.Read(randomByte[:])
		c.sendDMAck(pkt, senderPubKey, sharedSecret, meshcore.BuildAckPayload(ackPlaintext, senderPubKey, attemptByte, randomByte[0]))

		channelKey := "dm:" + senderPubKeyHex

		msg := &store.Message{
			CompanionID: c.cfg.ID,
			Channel:     channelKey,
			ChannelHash: 0,
			Sender:      senderName,
			Text:        text,
			Direction:   "rx",
			Timestamp:   time.Now(),
		}
		if pkt.HasSignalInfo {
			snr := float64(pkt.SNR)
			rssi := pkt.RSSI
			msg.SNR = &snr
			msg.RSSI = &rssi
		}

		c.store.WriteAsync(func() {
			if insertErr := c.store.Messages.Insert(context.Background(), msg); insertErr != nil {
				c.log.Error("failed to persist incoming DM", "error", insertErr)
			}

			c.log.Info("DM received", "from", senderName, "text", text)

			if c.hub != nil {
				wsMsg := map[string]any{
					"companion": c.cfg.Name,
					"channel":   channelKey,
					"sender":    senderName,
					"text":      text,
					"direction": "rx",
					"timestamp": msg.Timestamp.UTC().Format(time.RFC3339),
					"id":        msg.ID,
				}
				if pkt.HasSignalInfo {
					wsMsg["snr"] = pkt.SNR
					wsMsg["rssi"] = pkt.RSSI
				}
				c.hub.Broadcast("messages", wsMsg)
			}
		})

		// After the insert is queued, so a reply's WriteSync lands behind it and gets the higher row id chat ordering needs.
		c.dispatchDMTriggers(trigger.DirectMessage{
			SenderPubKey: senderPubKeyHex,
			SenderName:   senderName,
			Text:         text,
			Timestamp:    uint32(msg.Timestamp.Unix()),
		}, pkt)
	})

	c.node.OnPacket(meshcore.PayloadTypeTrace, func(pkt *meshcore.Packet) {
		tr, err := meshcore.TraceFromBytes(pkt.Payload)
		if err != nil {
			return
		}

		hashSize := int(tr.PathHashSize())
		hops := 0
		if hashSize > 0 && len(tr.PathHashes) > 0 {
			hops = len(tr.PathHashes) / hashSize
		}

		pathHexes := make([]string, 0, hops)
		for i := 0; i < hops; i++ {
			start := i * hashSize
			end := start + hashSize
			if end > len(tr.PathHashes) {
				break
			}
			pathHexes = append(pathHexes, hex.EncodeToString(tr.PathHashes[start:end]))
		}

		hopSNRs := make([]float64, 0, len(pkt.Path))
		for _, b := range pkt.Path {
			hopSNRs = append(hopSNRs, float64(int8(b))/4.0)
		}

		var snrPtr *float64
		if pkt.HasSignalInfo {
			snr := float64(pkt.SNR)
			snrPtr = &snr
		}
		c.notifyTraceWaiter(tr.Tag, traceEcho{hops: hops, pathHex: pathHexes, hopSNRs: hopSNRs, snr: snrPtr})

		if c.hub != nil {
			wsMsg := map[string]any{
				"companion": c.cfg.Name,
				"tag":       tr.Tag,
				"hops":      hops,
				"path":      pathHexes,
				"hopSNRs":   hopSNRs,
			}
			if pkt.HasSignalInfo {
				wsMsg["snr"] = pkt.SNR
			}
			c.hub.Broadcast("traces", wsMsg)
		}

		c.log.Debug("trace received",
			"tag", fmt.Sprintf("%08x", tr.Tag),
			"hops", hops,
		)
	})

	c.node.OnPacket(meshcore.PayloadTypeResponse, func(pkt *meshcore.Packet) {
		c.repeaters.HandleResponsePacket(pkt)
	})

	c.node.OnPacket(meshcore.PayloadTypePath, func(pkt *meshcore.Packet) {
		if c.repeaters.HandlePathPacket(pkt) {
			return
		}
		c.handleDMPathReturn(pkt)
	})
}

// dmCandidate is one identity a DM's 1-byte source hash could belong to.
type dmCandidate struct {
	pubkey    []byte
	name      string
	isContact bool
}

// dmCandidates lists every key that could have sent this DM; the peer table is what the firmware decrypts against, its contacts[] auto-adding every advert heard.
func (c *Companion) dmCandidates(source byte) []dmCandidate {
	var out []dmCandidate
	seen := make(map[string]bool)

	contacts, err := c.store.Contacts.List(c.runCtx, c.cfg.ID)
	if err != nil {
		c.log.Error("failed to list contacts for DM decryption", "error", err)
	}
	for _, ct := range contacts {
		if len(ct.PeerPubKey) == 0 || ct.PeerPubKey[0] != source {
			continue
		}
		key := hex.EncodeToString(ct.PeerPubKey)
		seen[key] = true
		name := ct.Name
		if p := c.knownPeer(ct.PeerPubKey); p != nil && p.Name != "" {
			name = p.Name
		}
		out = append(out, dmCandidate{pubkey: ct.PeerPubKey, name: name, isContact: true})
	}

	for _, p := range c.node.Peers().LookupByHash([]byte{source}) {
		pub := p.Identity.PublicKey()
		if seen[hex.EncodeToString(pub[:])] {
			continue
		}
		out = append(out, dmCandidate{pubkey: append([]byte(nil), pub[:]...), name: p.Name})
	}
	return out
}

// knownPeer resolves a key against the peer table, nil when it was never heard advertising.
func (c *Companion) knownPeer(pubkey []byte) *node.Peer {
	id, err := meshcore.NewIdentityFromBytes(pubkey)
	if err != nil {
		return nil
	}
	return c.node.Peers().Lookup(id.PublicKey())
}

// addDMSenderAsContact files an accepted stranger, which is what surfaces the thread in the conversation list.
func (c *Companion) addDMSenderAsContact(pubkey []byte, name string) {
	var peerType string
	var stored *store.Peer
	if p := c.knownPeer(pubkey); p != nil {
		peerType = p.Type
	}
	if p, err := c.store.Peers.GetByPubKey(c.runCtx, pubkey); err == nil {
		stored = p
	}

	c.store.WriteAsync(func() {
		ctx := context.Background()
		if err := c.store.Contacts.Add(ctx, c.cfg.ID, pubkey, name, peerType); err != nil {
			c.log.Error("failed to add DM sender as contact", "error", err)
			return
		}
		if stored != nil {
			_ = c.store.Contacts.RefreshFromAdvert(ctx, pubkey, stored.Name, stored.Type,
				stored.Lat, stored.Lon, stored.Feat1, stored.Feat2,
				stored.LastSeen, stored.LastAdvertTS, stored.HasLocation())
		}
		c.log.Info("added DM sender as contact", "peer", name)
	})
}

// dispatchDMTriggers fans an accepted DM out to the trigger set; non-implementers (channel, cron) are skipped.
func (c *Companion) dispatchDMTriggers(dm trigger.DirectMessage, pkt *meshcore.Packet) {
	c.mu.Lock()
	entries := c.triggers
	c.mu.Unlock()
	for _, e := range entries {
		if h, ok := e.trigger.(dmTextHandler); ok {
			h.HandleDirectMessage(dm, pkt)
		}
	}
}
