package trigger

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/logging"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

type ChannelTrigger struct {
	cfg      config.TriggerConfig
	botName  string
	node     *node.Node
	patterns []*regexp.Regexp
	channels map[string]bool // channel names this trigger listens on; nil = all
	log      *slog.Logger

	mu       sync.Mutex
	callback Callback
}

func NewChannelTrigger(botName string, cfg config.TriggerConfig, n *node.Node, channels []*meshcore.ChannelEntry, log *slog.Logger) (*ChannelTrigger, error) {
	var patterns []*regexp.Regexp
	if cfg.Match != nil {
		patterns = make([]*regexp.Regexp, 0, len(*cfg.Match))
		for _, m := range *cfg.Match {
			re, err := regexp.Compile(m)
			if err != nil {
				return nil, fmt.Errorf("invalid match pattern %q: %w", m, err)
			}
			patterns = append(patterns, re)
		}
	}

	var channelFilter map[string]bool
	if len(channels) > 0 {
		channelFilter = make(map[string]bool, len(channels))
		for _, ch := range channels {
			channelFilter[ch.Name] = true
		}
	}

	return &ChannelTrigger{
		cfg:      cfg,
		botName:  botName,
		node:     n,
		patterns: patterns,
		channels: channelFilter,
		log:      log.With("trigger", "channel"),
	}, nil
}

// Start stores the callback. Group-text packets are delivered via the
// companion's single persistent GrpTxt handler (which calls HandleGroupText),
// not a per-trigger node.OnPacket registration — that lets triggers be swapped
// at runtime without leaking node packet handlers (node.OnPacket cannot
// deregister).
func (t *ChannelTrigger) Start(_ context.Context, callback Callback) error {
	t.mu.Lock()
	t.callback = callback
	t.mu.Unlock()
	return nil
}

// Stop clears the callback so HandleGroupText becomes a no-op even if the
// companion's dispatcher still holds a reference to this (now removed) trigger.
func (t *ChannelTrigger) Stop() error {
	t.mu.Lock()
	t.callback = nil
	t.mu.Unlock()
	return nil
}

// HandleGroupText is invoked by the companion's persistent GrpTxt handler for
// every received group message. It decrypts, applies this trigger's channel
// and pattern filters, and fires the callback on a match.
func (t *ChannelTrigger) HandleGroupText(pkt *meshcore.Packet) {
	t.mu.Lock()
	cb := t.callback
	t.mu.Unlock()
	if cb == nil {
		return // stopped or not yet started
	}

	msg, ch, err := t.node.DecryptGroupText(pkt)
	if err != nil {
		t.log.Log(context.Background(), logging.LevelTrace, "group decrypt failed", "error", err)
		return
	}

	t.log.Log(context.Background(), logging.LevelTrace, "group message received",
		"channel", ch.Name, "sender", msg.Sender,
		"text", msg.Text, "snr", pkt.SNR, "rssi", pkt.RSSI)

	// Never react to our own companion's traffic — a manual channel send or a
	// bot reply is heard back over the air, and matching it would let a bot
	// trigger on itself (and loop). Mirrors the rx persistence handler's skip.
	if msg.Sender == t.botName {
		t.log.Log(context.Background(), logging.LevelTrace, "own message, skipping trigger",
			"channel", ch.Name)
		return
	}

	if t.channels != nil && !t.channels[ch.Name] {
		t.log.Log(context.Background(), logging.LevelTrace, "channel not matched, skipping",
			"received", ch.Name, "listening", t.channelNames())
		return
	}

	captures := t.matchesAny(msg.Text)
	if captures == nil {
		t.log.Log(context.Background(), logging.LevelTrace, "no pattern matched",
			"channel", ch.Name, "text", msg.Text, "patterns", t.patternStrings())
		return
	}

	t.log.Log(context.Background(), logging.LevelTrace, "trigger matched", "captures", captures)

	cb(Event{
		Type:    "channel",
		BotName: t.botName,
		Data: map[string]any{
			"Sender":       msg.Sender,
			"Channel":      ch.Name,
			"ChannelEntry": ch,
			"Message":      msg.Text,
			"Match":        captures,
			"Timestamp":    msg.Timestamp,
			"SNR":          pkt.SNR,
			"RSSI":         pkt.RSSI,
			"Hops":         pkt.PathHashCount(),
			"PathHashes":   pkt.PathHashes(),
			"PathHashSize": pkt.PathHashSize(),
		},
	})
}

// matchesAny returns the first matching pattern's named capture groups, or nil
// if no pattern matches. When there are no patterns, it returns an empty
// (non-nil) map to indicate a match-all.
func (t *ChannelTrigger) matchesAny(text string) map[string]string {
	if len(t.patterns) == 0 {
		return map[string]string{} // no patterns = match everything
	}
	for _, re := range t.patterns {
		m := re.FindStringSubmatch(text)
		t.log.Log(context.Background(), logging.LevelTrace, "regex check",
			"pattern", re.String(),
			"text", text, "matched", m != nil)
		if m == nil {
			continue
		}
		captures := make(map[string]string)
		for i, name := range re.SubexpNames() {
			if i == 0 || name == "" {
				continue
			}
			captures[name] = m[i]
		}
		return captures
	}
	return nil
}

func (t *ChannelTrigger) channelNames() []string {
	names := make([]string, 0, len(t.channels))
	for name := range t.channels {
		names = append(names, name)
	}
	return names
}

func (t *ChannelTrigger) patternStrings() []string {
	strs := make([]string, len(t.patterns))
	for i, re := range t.patterns {
		strs[i] = re.String()
	}
	return strs
}

var _ Trigger = (*ChannelTrigger)(nil)
