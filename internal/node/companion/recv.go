package companion

import (
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
)

const dmAckDelay = 200 * time.Millisecond

// txtTypePlain (TXT_TYPE_PLAIN) is an ordinary chat message — the type carried
// in the upper 6 bits of the flags byte (plaintext[4]>>2 for DMs, payload.Flags>>2
// for channel text) when it is neither CLI data nor a signed room post.
const txtTypePlain = 0

// txtTypeCliData is the DM plaintext type byte for CLI data (TXT_TYPE_CLI_DATA),
// extracted from a received DM via plaintext[4]>>2 to detect repeater CLI replies.
const txtTypeCliData = 1

// txtTypeSignedPlain (TXT_TYPE_SIGNED_PLAIN) marks a post pushed by a room server.
const txtTypeSignedPlain = 2

// sendDMAck sends an ACK back to the DM sender. For flood-routed messages,
// it builds a PathReturn (with ACK as extra data) so the sender learns the
// path to us. For direct-routed messages, it sends a plain ACK packet using
// the peer's known out_path (or floods if no path known).
//
// ackHashKey is the pubkey hashed into the ACK CRC — the sender's for DMs,
// OUR own for room post pushes.
func (c *Companion) sendDMAck(pkt *meshcore.Packet, senderPubKey []byte, sharedSecret []byte, ackPlaintext []byte, ackHashKey []byte, attemptByte byte) {
	var randomByte [1]byte
	rand.Read(randomByte[:])
	ackPayload := meshcore.BuildAckPayload(ackPlaintext, ackHashKey, attemptByte, randomByte[0])

	if pkt.IsRouteFlood() {
		// Build PathReturn with ACK as extra data — tells the sender the path to us
		pathReturn, err := c.buildPathReturn(senderPubKey, sharedSecret, pkt.Path, pkt.PathLength, meshcore.PayloadTypeAck, ackPayload)
		if err != nil {
			c.log.Debug("failed to build path return for DM ACK", "error", err)
			return
		}
		if err := c.node.SendPacketDelayed(pathReturn, node.PriorityFloodRelay, dmAckDelay); err != nil {
			c.log.Debug("failed to send DM ACK (path return)", "error", err)
		}
	} else {
		// Send the ACK along the peer's known out_path; flood only when the route
		// is genuinely unknown (nil out_path). A direct neighbour is stored as an
		// empty but non-nil path, so route it direct with 0 hops rather than
		// flooding a node we can reach directly (which would pollute the mesh).
		ackPkt := &meshcore.Packet{
			Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAck, 0),
			Payload: ackPayload,
		}

		if len(senderPubKey) == 32 {
			var pubkey [32]byte
			copy(pubkey[:], senderPubKey)
			peer := c.node.Peers().Lookup(pubkey)
			if peer != nil && peer.OutPath != nil {
				ackPkt.Header = meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeAck, 0)
				ackPkt.Path = peer.OutPath
				ackPkt.PathLength = byte(len(peer.OutPath) / int(meshcore.PathHashSize))
			}
		}

		ackPkt.PathLength |= (meshcore.PathHashSize - 1) << 6
		if err := c.node.SendPacketDelayed(ackPkt, node.PriorityFloodRelay, dmAckDelay); err != nil {
			c.log.Debug("failed to send DM ACK", "error", err)
		}
	}
}

// buildPathReturn constructs a flood-routed Path packet (PathReturn) matching
// the C++ firmware format. The payload is:
//
//	[dest_hash:1][src_hash:1][MAC:2][encrypted([pathLenByte][path_data][extra_type][extra_data])]
func (c *Companion) buildPathReturn(destPubKey []byte, sharedSecret []byte, inPath []byte, pathLenByte byte, extraType byte, extraData []byte) (*meshcore.Packet, error) {
	pathHashSize := int((pathLenByte>>6)&3) + 1
	pathHashCount := int(pathLenByte & 63)
	pathDataLen := pathHashCount * pathHashSize
	if pathDataLen > len(inPath) {
		pathDataLen = len(inPath)
	}

	// Build plaintext: [pathLenByte][path_data][extra_type][extra_data]
	plain := make([]byte, 0, 1+pathDataLen+1+len(extraData))
	plain = append(plain, pathLenByte)
	plain = append(plain, inPath[:pathDataLen]...)
	plain = append(plain, extraType)
	plain = append(plain, extraData...)

	encrypted, err := meshcore.EncryptThenMAC(sharedSecret, plain)
	if err != nil {
		return nil, fmt.Errorf("encrypting path return: %w", err)
	}

	// Build full Path payload: [dest_hash][src_hash][MAC+encrypted]
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

// handleRoomPush handles a room-server post pushed to us as a TXT_MSG flagged
// TXT_TYPE_SIGNED_PLAIN: [post_timestamp:4][flags:1][author_pubkey_prefix:4][text:N].
// Pushes are ACK-gated — the server won't advance our sync_since or send the
// next post until it sees our ACK (hashed with OUR pubkey, not the sender's).
func (c *Companion) handleRoomPush(pkt *meshcore.Packet, roomPubKey []byte, roomPubKeyHex string, sharedSecret []byte, plaintext []byte) {
	if len(plaintext) < 9 {
		c.log.Debug("room push plaintext too short", "room", roomPubKeyHex[:12])
		return
	}

	postTs := binary.LittleEndian.Uint32(plaintext[:4])
	authorPrefix := plaintext[5:9]
	text := strings.TrimRight(string(plaintext[9:]), "\x00")

	// ACK even duplicates — each retry carries a fresh attempt byte, so a
	// previous ACK can't satisfy it.
	selfPubKey := c.node.Identity().PublicKey()
	c.sendDMAck(pkt, roomPubKey, sharedSecret, plaintext[:9+len(text)], selfPubKey[:], 0)

	channelKey := "dm:" + roomPubKeyHex

	// Dedup re-pushed posts: the server's backlog holds at most 32, so 50
	// recent rows cover the resync window.
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
		if insertErr := c.store.Messages.Insert(c.runCtx, msg); insertErr != nil {
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

// handleDMPathReturn processes incoming Path packets as potential DM ACK
// responses. It tries each contact's shared secret to decrypt the payload.
// On success it extracts the return path (updating the peer's out_path) and
// any embedded ACK (feeding it into the node's ack tracker).
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
		plaintext := path.Decrypt(secret)
		if len(plaintext) < 2 {
			return
		}

		pathLenByte := plaintext[0]
		pathHashSize := int((pathLenByte>>6)&3) + 1
		hopCount := int(pathLenByte & 63)
		pathDataLen := hopCount * pathHashSize

		if len(plaintext) < 1+pathDataLen+1 {
			return
		}

		returnPath := plaintext[1 : 1+pathDataLen]
		extraType := plaintext[1+pathDataLen]
		extraData := plaintext[1+pathDataLen+1:]

		// Update the peer's out_path so future sends can use direct routing
		if len(returnPath) > 0 {
			c.log.Debug("DM path return received",
				"peer", hex.EncodeToString(ct.PeerPubKey[:6]),
				"hops", hopCount,
				"pathHex", hex.EncodeToString(returnPath))
			var pubkey [32]byte
			copy(pubkey[:], ct.PeerPubKey)
			c.node.Peers().SetOutPath(pubkey, returnPath, uint8(pathHashSize))
			hs := uint8(pathHashSize)
			c.store.WriteAsync(func() {
				_ = c.store.Peers.UpdateOutPath(c.runCtx, ct.PeerPubKey, returnPath, hs)
				// The contact owns its path, scoped to this companion — DMs route
				// from it. Companions never share a route to a peer.
				_ = c.store.Contacts.UpdateOutPath(c.runCtx, c.cfg.ID, ct.PeerPubKey, returnPath, hs)
			})
		}

		// Extract embedded ACK and feed it into the ack tracker
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
		c.pendingOutbound.Lock()
		msgID := c.pendingOutbound.msgID
		channel := c.pendingOutbound.channel
		c.pendingOutbound.msgID = 0
		c.pendingOutbound.channel = ""
		c.pendingOutbound.Unlock()

		if msgID == 0 || c.echoTracker == nil {
			return
		}

		pkt, err := meshcore.PacketFromBytes(data)
		if err != nil {
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
			if err := c.store.Peers.Upsert(c.runCtx, p); err != nil {
				c.log.Error("failed to persist peer", "error", err)
				return
			}

			// Keep any saved contact's cached record current. Contacts own their
			// identity/location/feat/last-seen now (decoupled from
			// discovered_peers), so this is what refreshes them on re-advert.
			// Location only updates when the advert carries one ("advert wins"
			// over a hand-set location, but a no-GPS advert leaves it alone).
			hasLoc := p.HasLocation()
			if err := c.store.Contacts.RefreshFromAdvert(
				c.runCtx, p.PubKey, appData.Name, appData.Type,
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

		// Channel messages are matched by a 1-byte channel hash and authenticated
		// by a 2-byte HMAC, both of which collide cheaply on a busy mesh — and the
		// cipher is AES-ECB, so foreign or binary traffic on a colliding channel
		// "decrypts" to byte-noise (a repeating 16-byte block is the tell-tale).
		//
		// First gate matches the firmware (BaseChatMesh::onGroupDataRecv): a real
		// channel chat message is TXT_TYPE_PLAIN, so the upper 6 bits of the flags
		// byte are 0. On a wrong-key decrypt that slips the 2-byte MAC the flags
		// byte is random, so this drops ~63/64 of collision garbage — the
		// firmware's primary defence.
		if txtType := payload.Flags >> 2; txtType != txtTypePlain {
			c.log.Debug("dropping non-plain channel message",
				"channel", ch.Name, "channelHash", ch.Hash, "txtType", txtType)
			return
		}
		// Second gate goes beyond the firmware, which hands raw decrypted bytes to
		// its UI: an authentic message on a shared-PSK channel (e.g. Public) can
		// carry genuinely binary content. Real chat is text; drop anything that
		// isn't valid UTF-8 so garbage never reaches the store or the chat UI.
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
			if err := c.store.Messages.Insert(c.runCtx, msg); err != nil {
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

	// One persistent group-text handler dispatches to the current trigger set.
	// Triggers register here (instead of each calling node.OnPacket) so
	// ReloadTriggers can swap them at runtime — node.OnPacket has no
	// deregistration, so per-trigger handlers would leak on every reload.
	//
	// Registered AFTER the rx-persist handler above on purpose: a matched
	// trigger replies via WriteSync, which must be enqueued to the store writer
	// *after* the inbound message's WriteAsync. Otherwise the reply gets a lower
	// id and sorts before the message that triggered it.
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

		contacts, err := c.store.Contacts.List(c.runCtx, c.cfg.ID)
		if err != nil {
			c.log.Error("failed to list contacts for DM decryption", "error", err)
			return
		}

		var senderPubKeyHex string
		var senderPubKey []byte
		var senderName string
		var sharedSecret []byte
		var plaintext []byte

		for _, ct := range contacts {
			if len(ct.PeerPubKey) == 0 || ct.PeerPubKey[0] != txtMsg.Source {
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
			if !txtMsg.VerifyMAC(secret) {
				continue
			}
			plaintext = txtMsg.Decrypt(secret)
			senderPubKey = ct.PeerPubKey
			senderPubKeyHex = hex.EncodeToString(ct.PeerPubKey)
			sharedSecret = secret
			peer := c.node.Peers().Lookup(peerID.PublicKey())
			if peer != nil && peer.Name != "" {
				senderName = peer.Name
			} else {
				senderName = senderPubKeyHex[:12] + "…"
			}
			break
		}

		if plaintext == nil {
			c.log.Debug("could not decrypt DM from any contact")
			return
		}

		if len(plaintext) < 5 {
			c.log.Debug("DM plaintext too short")
			return
		}

		flags := plaintext[4] >> 2
		text := strings.TrimRight(string(plaintext[5:]), "\x00")

		if flags == txtTypeCliData {
			var senderKey [32]byte
			pubBytes, _ := hex.DecodeString(senderPubKeyHex)
			copy(senderKey[:], pubBytes)
			c.repeaters.HandleCLIResponse(senderKey, text)
			return
		}

		if flags == txtTypeSignedPlain {
			c.handleRoomPush(pkt, senderPubKey, senderPubKeyHex, sharedSecret, plaintext)
			return
		}

		// Send ACK for plain text DMs
		ackPlaintext := plaintext[:5+len(text)]
		var attemptByte byte
		if 5+len(text)+1 < len(plaintext) {
			attemptByte = plaintext[5+len(text)+1]
		}
		c.sendDMAck(pkt, senderPubKey, sharedSecret, ackPlaintext, senderPubKey, attemptByte)

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
			if insertErr := c.store.Messages.Insert(c.runCtx, msg); insertErr != nil {
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
