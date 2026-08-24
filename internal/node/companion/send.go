package companion

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/store"
)

func (c *Companion) SendChannelMessage(channelName, text string) error {
	ch := c.findChannel(channelName)
	if ch == nil {
		return fmt.Errorf("channel %q not found", channelName)
	}
	return c.sendGroupReply(ch, text, meshcore.PathHashSize, 5*time.Second, 3)
}

// sendGroupReply persists an outgoing group-text message, broadcasts it to the
// UI over the "messages" topic, tracks it for tx-echo correlation, and sends it
// on the mesh. Shared by the chat API (SendChannelMessage) and the trigger
// reply path so bot replies appear in the Messages page exactly like manual
// sends — previously triggers called node.SendGroupText directly and their
// replies were never persisted or broadcast.
func (c *Companion) sendGroupReply(ch *meshcore.ChannelEntry, text string, hashSize uint8, retryTimeout time.Duration, maxRetries int) error {
	payload := &meshcore.GroupTextPayload{
		Timestamp: uint32(time.Now().Unix()),
		Sender:    c.cfg.Name,
		Text:      text,
	}

	msg := &store.Message{
		CompanionID: c.cfg.ID,
		Channel:     ch.Name,
		ChannelHash: ch.Hash,
		Sender:      c.cfg.Name,
		Text:        text,
		Direction:   "tx",
		Timestamp:   time.Now(),
	}

	c.store.WriteSync(func() {
		if insertErr := c.store.Messages.Insert(c.runCtx, msg); insertErr != nil {
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

	return c.node.SendGroupText(
		ch, payload, hashSize, retryTimeout, maxRetries,
		func(gsr node.GroupSendResult) {
			c.log.Debug("group reply result", "channel", ch.Name, "confirmed", gsr.Confirmed)
		},
	)
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

	// Route from the contact's own stored path (per companion). Fall back to the
	// in-memory peer table for a non-contact send, then to flood (nil path) when
	// neither knows a route — SendTextMessage treats a nil path as a flood, so a
	// contact whose discovered peer was swept still sends fine.
	var outPath []byte
	var hashSize uint8
	if ct, cerr := c.store.Contacts.Get(c.runCtx, c.cfg.ID, pubkeyBytes); cerr == nil && ct != nil {
		outPath, hashSize = ct.OutPath, ct.OutPathHashSize
	} else if peer := c.node.Peers().Lookup(peerIdentity.PublicKey()); peer != nil {
		outPath, hashSize = peer.OutPath, peer.OutPathHashSize
	}
	// A direct-routed packet needs a real hop-hash width; a stored 0 (e.g. a
	// migrated row) would otherwise frame the path wrong. Empty/nil path floods,
	// where the size is irrelevant.
	if len(outPath) > 0 && hashSize == 0 {
		hashSize = meshcore.PathHashSize
	}

	channelKey := "dm:" + pubkeyHex
	statusSending := "sending"

	msg := &store.Message{
		CompanionID: c.cfg.ID,
		Channel:     channelKey,
		ChannelHash: 0,
		Sender:      c.cfg.Name,
		Text:        text,
		Direction:   "tx",
		Timestamp:   time.Now(),
		Status:      &statusSending,
	}

	c.store.WriteSync(func() {
		if insertErr := c.store.Messages.Insert(c.runCtx, msg); insertErr != nil {
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
		outPath,
		hashSize,
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
				if err := c.store.Messages.UpdateStatus(c.runCtx, msgID, status); err != nil {
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

	if err := c.sendTracePacket(tag, auth, path, pathHashSize); err != nil {
		return 0, err
	}
	return tag, nil
}

// sendTracePacket builds and transmits a Trace packet for an already-chosen
// tag/auth pair. Split out of SendTrace so RunTrace (trace.go) can register
// its result waiter under the tag before the packet hits the radio.
func (c *Companion) sendTracePacket(tag, auth uint32, path []byte, pathHashSize uint8) error {
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
		return fmt.Errorf("encoding trace: %w", err)
	}

	pkt := meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeTrace, 0),
		PathLength: 0,
		Payload:    payload,
	}

	if err := c.node.SendPacket(&pkt); err != nil {
		return fmt.Errorf("sending trace: %w", err)
	}

	c.log.Debug("trace sent", "tag", fmt.Sprintf("%08x", tag), "hops", len(path)/int(pathHashSize), "pathHashSize", pathHashSize)
	return nil
}

func (c *Companion) findChannel(name string) *meshcore.ChannelEntry {
	for _, ch := range c.node.Channels() {
		if ch.Name == name {
			return ch
		}
	}
	return nil
}
