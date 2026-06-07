package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/meshcore-bot/api"
	"github.com/meshcore-go/meshcore-bot/store"
)

type triggerEntry struct {
	trigger  Trigger
	config   TriggerConfig
	channels []*meshcore.ChannelEntry
}

type Companion struct {
	cfg CompanionConfig

	node      *node.Node
	radio     *node.MuxRadio
	mux       *node.RadioMux
	templater *Templater
	log       *slog.Logger

	// Persistence & live updates
	store *store.Store
	hub   *api.Hub

	echoTracker *EchoTracker
	repeaters   *RepeaterManager

	pendingOutbound struct {
		sync.Mutex
		msgID   int64
		channel string
	}

	// -- Bot Triggers
	triggers []triggerEntry

	// MQTT Brokers - Out only
	obs *MqttObserver

	// Channel index tracking for standalone channels
	triggerChannelCount int

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewCompanion(cfg CompanionConfig, mux *node.RadioMux, st *store.Store, hub *api.Hub, echoTracker *EchoTracker, stats StatsProvider, recvErrors *atomic.Uint64, nodeOpts ...node.Option) (*Companion, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("companion name is required")
	}

	keyFile := strings.TrimSpace(cfg.KeyFile)
	if keyFile == "" {
		// Default keyfile is <Ascii name>_companion.key
		keyFile = strings.ToValidUTF8(name, "") + "_companion.key"
	}

	radio := mux.NewRadio()
	log := slog.Default().With("component", "companion", "name", name)
	opts := append([]node.Option{
		node.WithMaxPeers(100_000),
		node.WithErrorHandler(func(err error) {
			log.Error("node error", "error", err)
		}),
	}, nodeOpts...)

	id, err := loadOrCreateIdentity(keyFile)
	if err != nil {
		return nil, fmt.Errorf("companion identity: %w", err)
	}

	n := node.New(id, radio, opts...)

	companion := &Companion{
		cfg:         cfg,
		node:        n,
		radio:       &radio,
		mux:         mux,
		templater:   NewTemplater(),
		log:         log,
		store:       st,
		hub:         hub,
		echoTracker: echoTracker,
		repeaters:   NewRepeaterManager(n, st, log),
	}

	// Register/Create Triggers
	triggerChCount := 0
	if cfg.Triggers != nil {
		for _, trigCfg := range *cfg.Triggers {
			// Apply Trigger defaults
			if trigCfg.MaxRetries == nil {
				retries := 3
				trigCfg.MaxRetries = &retries
			}
			if trigCfg.RetryTimeout == nil {
				retry := int64(5)
				trigCfg.RetryTimeout = &retry
			}

			var channels []*meshcore.ChannelEntry
			if trigCfg.Channels != nil {
				for channelIdx, chRef := range *trigCfg.Channels {
					ch, err := channelFromRef(chRef)
					if err != nil {
						return nil, fmt.Errorf("invalid channel %q: %w", chRef.Name, err)
					}
					channels = append(channels, ch)
					n.SetChannel(channelIdx, ch)
					if channelIdx+1 > triggerChCount {
						triggerChCount = channelIdx + 1
					}
				}
			}

			entry, err := companion.buildTrigger(trigCfg, channels)
			if err != nil {
				return nil, fmt.Errorf("companion: %q trigger %q: %w", name, trigCfg.Type, err)
			}

			companion.triggers = append(companion.triggers, *entry)
		}
	}
	companion.triggerChannelCount = triggerChCount

	if cfg.Channels != nil {
		for i, chRef := range *cfg.Channels {
			ch, err := channelFromRef(chRef)
			if err != nil {
				return nil, fmt.Errorf("standalone channel %q: %w", chRef.Name, err)
			}
			n.SetChannel(triggerChCount+i, ch)
		}
	}

	// Register MQTT
	if companion.cfg.Mqtt != nil {
		mqtt := *companion.cfg.Mqtt

		obs, err := NewMqttObserver(mqtt, name, mux, companion.node.Identity(), stats, recvErrors)
		if err != nil {
			return nil, fmt.Errorf("creating mqtt observer: %w", err)
		}
		companion.obs = obs
	}

	companion.registerPacketHandlers()

	return companion, nil
}

func (c *Companion) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	// Start triggers
	for i, entry := range c.triggers {
		e := entry
		if err := e.trigger.Start(ctx, c.makeCallback(ctx, e)); err != nil {
			cancel()
			return fmt.Errorf("starting trigger %d (%s): %w", i, e.config.Type, err)
		}
	}

	// Start MQTT
	if c.obs != nil {
		obsErr := c.obs.Start(ctx)
		if obsErr != nil {
			return fmt.Errorf("starting mqtt observer: %w", obsErr)
		}
	}

	// Start Advertising
	if c.cfg.AdvertInterval == nil || *c.cfg.AdvertInterval != 0 {
		go c.advertLoop(ctx)
	}

	return nil
}

func (c *Companion) Stop() error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()

	// Stop Triggers
	for _, trig := range c.triggers {
		trig.trigger.Stop()
	}

	// Stop MQTT
	if c.obs != nil {
		c.obs.Stop()
	}

	c.node.Stop()
	return nil
}

func (c *Companion) buildTrigger(cfg TriggerConfig, channels []*meshcore.ChannelEntry) (*triggerEntry, error) {
	var t Trigger
	var err error

	switch cfg.Type {
	case "channel", "group":
		t, err = NewChannelTrigger(c.cfg.Name, cfg, c.node, channels, c.log)
	case "cron":
		t, err = NewCronTrigger(c.cfg.Name, cfg, c.log)
	default:
		return nil, fmt.Errorf("unknown trigger type %q", cfg.Type)
	}
	if err != nil {
		return nil, err
	}

	return &triggerEntry{
		trigger:  t,
		config:   cfg,
		channels: channels,
	}, nil
}

func (c *Companion) makeCallback(ctx context.Context, entry triggerEntry) TriggerCallback {
	return func(evt TriggerEvent) {
		rendered, err := c.templater.Render(&evt, entry.config.Template)
		if err != nil {
			c.log.Error("template error", "error", err)
			return
		}

		c.log.Log(ctx, LevelTrace, "template rendered",
			"trigger", evt.Type, "output", rendered)

		hashSize := resolvePathHashSize(entry.config.PathHashSize, evt)

		retryTimeout := time.Duration(*entry.config.RetryTimeout) * time.Second

		switch evt.Type {
		case "channel":
			ch, _ := evt.Data["ChannelEntry"].(*meshcore.ChannelEntry)
			c.log.Debug("sending group txt", "channel", ch.Name, "pathHashSize", hashSize)

			reply := &meshcore.GroupTextPayload{
				Timestamp: uint32(time.Now().Unix()),
				Sender:    c.cfg.Name,
				Text:      rendered,
			}

			err := c.node.SendGroupText(
				ch,
				reply,
				hashSize,
				retryTimeout,
				*entry.config.MaxRetries,
				func(gsr node.GroupSendResult) {
					c.log.Debug("GroupSendResult", "text", rendered, "Confimed", gsr.Confirmed)
				})

			if err != nil {
				c.log.Error("send error", "error", err)
			}

		case "cron":
			for _, ch := range entry.channels {
				c.log.Debug("sending group txt", "channel", ch.Name, "pathHashSize", hashSize)

				reply := &meshcore.GroupTextPayload{
					Timestamp: uint32(time.Now().Unix()),
					Sender:    c.cfg.Name,
					Text:      rendered,
				}

				err := c.node.SendGroupText(
					ch,
					reply,
					hashSize,
					retryTimeout,
					*entry.config.MaxRetries,
					func(gsr node.GroupSendResult) {
						c.log.Debug("GroupSendResult", "text", rendered, "Confimed", gsr.Confirmed)
					})

				if err != nil {
					c.log.Error("send error", "error", err)
				}
			}
		}
	}
}

func (c *Companion) advertLoop(ctx context.Context) {
	// Send initial advert
	err := c.advert()
	if err != nil {
		c.log.Error("initial advert error", "error", err)
	}

	advertInterval := c.cfg.AdvertInterval
	if advertInterval == nil || *advertInterval < 1 {
		oneDay := 86400
		advertInterval = &oneDay
	}

	// Get tick
	interval := time.Duration(*advertInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Start loop
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := c.advert()
			if err != nil {
				c.log.Error("advert error", "error", err)
			}
		}
	}
}

func (c *Companion) advert() error {
	appData := meshcore.AdvertAppData{
		Type: "CHAT",
		Name: c.cfg.Name,
		Lat:  0,
		Lon:  0,
	}

	if c.cfg.hasLatLon() {
		appData.Lat = int32(math.Round(*c.cfg.Latitude * 1_000_000.0))
		appData.Lon = int32(math.Round(*c.cfg.Longitude * 1_000_000.0))
	}

	rawAppData, err := appData.ToBytes()
	if err != nil {
		return err
	}

	advert := meshcore.Advert{
		PublicKey:  c.node.Identity().Identity,
		Timestamp:  uint32(time.Now().Unix()),
		RawAppData: rawAppData,
	}
	advert.Sign(c.node.Identity().PrivateKey())

	payload, err := advert.ToBytes()
	if err != nil {
		return err
	}

	pkt := meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAdvert, 0),
		PathLength: meshcore.PathHashSize - 1,
		Payload:    payload,
	}

	return c.node.SendPacket(&pkt)
}

const dmAckDelay = 200 * time.Millisecond

// sendDMAck sends an ACK back to the DM sender. For flood-routed messages,
// it builds a PathReturn (with ACK as extra data) so the sender learns the
// path to us. For direct-routed messages, it sends a plain ACK packet using
// the peer's known out_path (or floods if no path known).
func (c *Companion) sendDMAck(pkt *meshcore.Packet, senderPubKey []byte, sharedSecret []byte, ackPlaintext []byte, attemptByte byte) {
	var randomByte [1]byte
	rand.Read(randomByte[:])
	ackPayload := meshcore.BuildAckPayload(ackPlaintext, senderPubKey, attemptByte, randomByte[0])

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
		// Direct-routed: send ACK via known out_path or flood
		ackPkt := &meshcore.Packet{
			Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeAck, 0),
			Payload: ackPayload,
		}

		// Try to use the peer's known out_path for direct send
		if len(senderPubKey) == 32 {
			var pubkey [32]byte
			copy(pubkey[:], senderPubKey)
			peer := c.node.Peers().Lookup(pubkey)
			if peer != nil && len(peer.OutPath) > 0 {
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

	contacts, err := c.store.Contacts.List(c.cfg.Name)
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
				_ = c.store.Peers.UpdateOutPath(ct.PeerPubKey, returnPath, hs)
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
			if err := c.store.Peers.Upsert(p); err != nil {
				c.log.Error("failed to persist peer", "error", err)
				return
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
			CompanionID:  c.cfg.Name,
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
		if blocked, err := c.store.BlockedSenders.IsBlocked(c.cfg.Name, convoID, payload.Sender); err == nil && blocked {
			c.log.Debug("blocked sender filtered", "sender", payload.Sender, "channel", ch.Name)
			return
		}

		c.store.WriteAsync(func() {
			if err := c.store.Messages.Insert(msg); err != nil {
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

		contacts, err := c.store.Contacts.List(c.cfg.Name)
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

		// Send ACK for plain text DMs
		ackPlaintext := plaintext[:5+len(text)]
		var attemptByte byte
		if 5+len(text)+1 < len(plaintext) {
			attemptByte = plaintext[5+len(text)+1]
		}
		c.sendDMAck(pkt, senderPubKey, sharedSecret, ackPlaintext, attemptByte)

		channelKey := "dm:" + senderPubKeyHex

		msg := &store.Message{
			CompanionID: c.cfg.Name,
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
			if insertErr := c.store.Messages.Insert(msg); insertErr != nil {
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

func (c *Companion) Name() string {
	return c.cfg.Name
}

func (c *Companion) AddChannel(ref ChannelRef) error {
	ch, err := channelFromRef(ref)
	if err != nil {
		return fmt.Errorf("invalid channel %q: %w", ref.Name, err)
	}

	for i := range node.DefaultMaxChannels {
		if existing := c.node.Channel(i); existing != nil && existing.Name == ch.Name {
			return fmt.Errorf("channel %q already exists", ch.Name)
		}
	}

	idx := c.nextFreeChannelIndex()
	if idx < 0 {
		return fmt.Errorf("no free channel slots")
	}

	if !c.node.SetChannel(idx, ch) {
		return fmt.Errorf("failed to set channel at index %d", idx)
	}

	c.log.Info("channel added", "channel", ch.Name, "index", idx)
	return nil
}

func (c *Companion) RemoveChannel(name string) error {
	for i := range node.DefaultMaxChannels {
		ch := c.node.Channel(i)
		if ch != nil && ch.Name == name {
			if i < c.triggerChannelCount {
				return fmt.Errorf("channel %q is bound to a trigger and cannot be removed", name)
			}
			c.node.RemoveChannel(i)
			c.log.Info("channel removed", "channel", name, "index", i)
			return nil
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func (c *Companion) RenameChannel(oldName, newName string) error {
	for i := range node.DefaultMaxChannels {
		ch := c.node.Channel(i)
		if ch != nil && ch.Name == oldName {
			ch.Name = newName
			c.log.Info("channel renamed", "old", oldName, "new", newName, "index", i)
			return nil
		}
	}
	return fmt.Errorf("channel %q not found", oldName)
}

func (c *Companion) nextFreeChannelIndex() int {
	for i := c.triggerChannelCount; i < node.DefaultMaxChannels; i++ {
		if c.node.Channel(i) == nil {
			return i
		}
	}
	return -1
}

func (c *Companion) Node() *node.Node {
	return c.node
}

func (c *Companion) SendChannelMessage(channelName, text string) error {
	ch := c.findChannel(channelName)
	if ch == nil {
		return fmt.Errorf("channel %q not found", channelName)
	}

	payload := &meshcore.GroupTextPayload{
		Timestamp: uint32(time.Now().Unix()),
		Sender:    c.cfg.Name,
		Text:      text,
	}

	msg := &store.Message{
		CompanionID: c.cfg.Name,
		Channel:     ch.Name,
		ChannelHash: ch.Hash,
		Sender:      c.cfg.Name,
		Text:        text,
		Direction:   "tx",
		Timestamp:   time.Now(),
	}

	c.store.WriteSync(func() {
		if insertErr := c.store.Messages.Insert(msg); insertErr != nil {
			c.log.Error("failed to persist outgoing message", "error", insertErr)
		}
	})

	msgID := msg.ID

	if c.hub != nil {
		c.hub.Broadcast("messages", map[string]any{
			"companion": c.cfg.Name,
			"channel":   ch.Name,
			"sender":    c.cfg.Name,
			"text":      text,
			"direction": "tx",
			"timestamp": msg.Timestamp.UTC().Format(time.RFC3339),
			"id":        msgID,
		})
	}

	c.pendingOutbound.Lock()
	c.pendingOutbound.msgID = msgID
	c.pendingOutbound.channel = ch.Name
	c.pendingOutbound.Unlock()

	err := c.node.SendGroupText(
		ch, payload, meshcore.PathHashSize,
		5*time.Second, 3,
		func(gsr node.GroupSendResult) {
			c.log.Debug("SendChannelMessage result", "channel", channelName, "confirmed", gsr.Confirmed)
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *Companion) SendContactMessage(pubkeyHex, text string) error {
	pubkeyBytes, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex: %w", err)
	}

	peerIdentity, err := meshcore.NewIdentityFromBytes(pubkeyBytes)
	if err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}

	peer := c.node.Peers().Lookup(peerIdentity.PublicKey())
	if peer == nil {
		return fmt.Errorf("peer not found in peer table")
	}

	channelKey := "dm:" + pubkeyHex
	statusSending := "sending"

	msg := &store.Message{
		CompanionID: c.cfg.Name,
		Channel:     channelKey,
		ChannelHash: 0,
		Sender:      c.cfg.Name,
		Text:        text,
		Direction:   "tx",
		Timestamp:   time.Now(),
		Status:      &statusSending,
	}

	c.store.WriteSync(func() {
		if insertErr := c.store.Messages.Insert(msg); insertErr != nil {
			c.log.Error("failed to persist outgoing DM", "error", insertErr)
		}
	})

	if c.hub != nil {
		c.hub.Broadcast("messages", map[string]any{
			"companion": c.cfg.Name,
			"channel":   channelKey,
			"sender":    c.cfg.Name,
			"text":      text,
			"direction": "tx",
			"timestamp": msg.Timestamp.UTC().Format(time.RFC3339),
			"id":        msg.ID,
			"status":    "sending",
		})
	}

	msgID := msg.ID
	return c.node.SendTextMessage(
		peerIdentity,
		[]byte(text),
		0,
		time.Now(),
		peer.OutPath,
		meshcore.PathHashSize,
		5*time.Second,
		func(result node.DMSendResult) {
			var status string
			if result.Confirmed {
				status = "delivered"
				c.log.Debug("DM delivered", "peer", pubkeyHex[:12], "roundTrip", result.RoundTrip)
			} else {
				status = "failed"
				c.log.Warn("DM delivery failed", "peer", pubkeyHex[:12])
			}

			c.store.WriteAsync(func() {
				if err := c.store.Messages.UpdateStatus(msgID, status); err != nil {
					c.log.Error("failed to update message status", "id", msgID, "error", err)
				}
			})

			if c.hub != nil {
				c.hub.Broadcast("messages", map[string]any{
					"action":    "status",
					"companion": c.cfg.Name,
					"channel":   channelKey,
					"id":        msgID,
					"status":    status,
				})
			}
		},
	)
}

func (c *Companion) SendTrace(path []byte, pathHashSize uint8) (uint32, error) {
	if len(path) == 0 {
		return 0, fmt.Errorf("path is required")
	}
	if pathHashSize != 1 && pathHashSize != 2 && pathHashSize != 4 {
		return 0, fmt.Errorf("pathHashSize must be 1, 2, or 4")
	}
	if len(path)%int(pathHashSize) != 0 {
		return 0, fmt.Errorf("path length %d is not divisible by pathHashSize %d", len(path), pathHashSize)
	}

	var tagBytes [4]byte
	if _, err := rand.Read(tagBytes[:]); err != nil {
		return 0, fmt.Errorf("generating trace tag: %w", err)
	}
	tag := binary.LittleEndian.Uint32(tagBytes[:])

	var authBytes [4]byte
	if _, err := rand.Read(authBytes[:]); err != nil {
		return 0, fmt.Errorf("generating trace auth: %w", err)
	}
	auth := binary.LittleEndian.Uint32(authBytes[:])

	var flags byte
	switch pathHashSize {
	case 1:
		flags = 0
	case 2:
		flags = 1
	case 4:
		flags = 2
	}

	trace := &meshcore.Trace{
		Tag:        tag,
		AuthCode:   auth,
		Flags:      flags,
		PathHashes: path,
	}

	payload, err := trace.ToBytes()
	if err != nil {
		return 0, fmt.Errorf("encoding trace: %w", err)
	}

	pkt := meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeTrace, 0),
		PathLength: 0,
		Payload:    payload,
	}

	if err := c.node.SendPacket(&pkt); err != nil {
		return 0, fmt.Errorf("sending trace: %w", err)
	}

	c.log.Debug("trace sent", "tag", fmt.Sprintf("%08x", tag), "hops", len(path)/int(pathHashSize), "pathHashSize", pathHashSize)
	return tag, nil
}

func (c *Companion) findChannel(name string) *meshcore.ChannelEntry {
	for _, ch := range c.node.Channels() {
		if ch.Name == name {
			return ch
		}
	}
	return nil
}
