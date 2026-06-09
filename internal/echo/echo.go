package echo

import (
	"sync"
	"time"

	"log/slog"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/store"
	meshcore "github.com/meshcore-go/meshcore-go"
)

type entry struct {
	messageID    int64
	companion    string
	channel      string
	registeredAt time.Time
}

type Tracker struct {
	mu      sync.Mutex
	pending map[[meshcore.PacketHashSize]byte]*entry
	store   *store.Store
	hub     *api.Hub
	log     *slog.Logger
	ttl     time.Duration
}

func NewTracker(st *store.Store, hub *api.Hub, log *slog.Logger) *Tracker {
	return &Tracker{
		pending: make(map[[meshcore.PacketHashSize]byte]*entry),
		store:   st,
		hub:     hub,
		log:     log,
		ttl:     30 * time.Second,
	}
}

func (t *Tracker) Track(hash [meshcore.PacketHashSize]byte, msgID int64, companion, channel string) {
	t.mu.Lock()
	t.pending[hash] = &entry{
		messageID:    msgID,
		companion:    companion,
		channel:      channel,
		registeredAt: time.Now(),
	}
	t.mu.Unlock()
	t.log.Debug("echo tracked", "messageID", msgID, "hash", hash, "companion", companion, "channel", channel)
}

func (t *Tracker) OnRawPacket(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
	pkt, err := meshcore.PacketFromBytes(data)
	if err != nil {
		return
	}

	hash := pkt.PacketHash()

	t.mu.Lock()
	entry, ok := t.pending[hash]
	if !ok {
		t.mu.Unlock()
		return
	}

	if time.Since(entry.registeredAt) > t.ttl {
		delete(t.pending, hash)
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()

	t.log.Debug("echo matched", "messageID", entry.messageID, "hash", hash, "hops", pkt.PathHashCount())

	var snrPtr *float64
	var rssiPtr *int8
	if hasSignalInfo {
		s := float64(snr)
		snrPtr = &s
		rssiPtr = &rssi
	}

	echo := &store.MessageEcho{
		MessageID:    entry.messageID,
		ReceivedAt:   time.Now(),
		PathHashes:   pkt.Path,
		PathHashSize: int(pkt.PathHashSize()),
		Hops:         int(pkt.PathHashCount()),
		SNR:          snrPtr,
		RSSI:         rssiPtr,
	}

	t.store.WriteAsync(func() {
		if err := t.store.Echoes.Insert(echo); err != nil {
			t.log.Error("failed to insert echo", "error", err, "messageID", entry.messageID)
			return
		}

		if echo.ID == 0 {
			return
		}

		count, err := t.store.Messages.IncrementRepeatCount(entry.messageID)
		if err != nil {
			t.log.Error("failed to increment repeat count", "error", err, "messageID", entry.messageID)
			return
		}

		if t.hub != nil {
			t.hub.Broadcast("messages", map[string]any{
				"action":      "repeatCount",
				"companion":   entry.companion,
				"channel":     entry.channel,
				"id":          entry.messageID,
				"repeatCount": count,
			})
		}
	})
}

func (t *Tracker) Prune() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for hash, entry := range t.pending {
		if now.Sub(entry.registeredAt) > t.ttl {
			delete(t.pending, hash)
		}
	}
}
