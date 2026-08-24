package companion

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/client/repeater"
	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/echo"
	"github.com/meshcore-go/OwlShack/internal/modem"
	"github.com/meshcore-go/OwlShack/internal/mqtt"
	"github.com/meshcore-go/OwlShack/internal/store"
	"github.com/meshcore-go/OwlShack/internal/trigger"
)

type triggerEntry struct {
	trigger  trigger.Trigger
	config   config.TriggerConfig
	channels []*meshcore.ChannelEntry
}

// groupTextHandler is implemented by triggers that react to group-text packets
// (channel triggers). The companion's single GrpTxt dispatcher fans messages
// out to these; non-implementers (e.g. cron triggers) are skipped.
type groupTextHandler interface {
	HandleGroupText(*meshcore.Packet)
}

type Companion struct {
	cfg config.CompanionConfig

	node      *node.Node
	radio     *node.MuxRadio
	mux       *node.RadioMux
	templater *trigger.Templater
	log       *slog.Logger

	// Persistence & live updates
	store *store.Store
	hub   *api.Hub

	echoTracker *echo.Tracker
	repeaters   *repeater.Client

	pendingOutbound struct {
		sync.Mutex
		msgID   int64
		channel string
	}

	traceWaiters traceWaiters

	// -- Bot Triggers
	triggers []triggerEntry

	// MQTT Brokers - Out only
	obs *mqtt.Observer

	mu     sync.Mutex
	cancel context.CancelFunc
	// runCtx is the context the triggers/observer were started with. Kept so
	// ReloadTriggers can start freshly-built triggers without a full restart.
	runCtx context.Context
}

func NewCompanion(cfg config.CompanionConfig, mux *node.RadioMux, st *store.Store, hub *api.Hub, echoTracker *echo.Tracker, stats modem.StatsProvider, recvErrors *atomic.Uint64, nodeOpts ...node.Option) (*Companion, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("companion name is required")
	}

	radio := mux.NewRadio()
	log := slog.Default().With("component", "companion", "name", name)
	opts := append([]node.Option{
		node.WithMaxPeers(100_000),
		node.WithErrorHandler(func(err error) {
			log.Error("node error", "error", err)
		}),
	}, nodeOpts...)
	// Out-paths learned via path-returns only; appended last to set on the final table.
	opts = append(opts, node.WithLearnedPathsOnly())

	// Identities are pinned in the config (EnsureCompanionKeys fills empty
	// ones before any config is persisted).
	id, err := identityFromHexSeed(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("companion identity: %w", err)
	}

	n := node.New(id, radio, opts...)

	companion := &Companion{
		cfg:         cfg,
		node:        n,
		radio:       &radio,
		mux:         mux,
		templater:   trigger.NewTemplater(),
		log:         log,
		store:       st,
		hub:         hub,
		echoTracker: echoTracker,
		repeaters:   repeater.NewClient(n, st, log),
	}

	// Register the companion's channels — the single source of truth for which
	// channels this node listens on. Triggers reference these by name; they do
	// not register channels themselves (ApplyDefaults guarantees every channel a
	// trigger names is also in the companion's channel list).
	if cfg.Channels != nil {
		for i, chRef := range *cfg.Channels {
			ch, err := channelFromRef(chRef)
			if err != nil {
				return nil, fmt.Errorf("channel %q: %w", chRef.Name, err)
			}
			n.SetChannel(i, ch)
		}
	}

	// Build triggers. A channel trigger keeps a name filter (the channels it
	// reacts to); decryption uses the companion channels registered above.
	companion.triggers, err = companion.buildTriggers(cfg)
	if err != nil {
		return nil, err
	}

	// Register MQTT
	if companion.cfg.Mqtt != nil {
		mqttCfg := *companion.cfg.Mqtt

		obs, err := mqtt.NewObserver(mqttCfg, name, mux, companion.node.Identity(), stats, recvErrors)
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
	c.runCtx = ctx
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

func (c *Companion) Name() string {
	return c.cfg.Name
}

// ID returns the companion's surrogate primary key, the stable key its history
// (messages, contacts, conversations, blocks) is stored under — so a rename
// (which only changes Name) keeps that history attached.
func (c *Companion) ID() int64 {
	return c.cfg.ID
}

// LatLon returns the companion's configured position (nil if unset).
func (c *Companion) LatLon() (*float64, *float64) {
	return c.cfg.Latitude, c.cfg.Longitude
}

func (c *Companion) Node() *node.Node {
	return c.node
}

// Repeaters exposes the companion's repeater manager for the API layer.
func (c *Companion) Repeaters() *repeater.Client {
	return c.repeaters
}
