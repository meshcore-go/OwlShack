// Package app wires the bot's subsystems together and runs the supervisor
// loop: it owns the database, modem, HTTP server and companion lifecycle, and
// handles config reloads (SIGHUP) and modem reconnects. It is the only place
// that depends on every other internal package.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/echo"
	"github.com/meshcore-go/meshcore-bot/internal/logging"
	"github.com/meshcore-go/meshcore-bot/internal/modem"
	"github.com/meshcore-go/meshcore-bot/internal/monitor"
	"github.com/meshcore-go/meshcore-bot/internal/node/companion"
	"github.com/meshcore-go/meshcore-bot/internal/store"
	"github.com/meshcore-go/meshcore-bot/web"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

const defaultListenAddr = ":8080"

// Run starts the bot and blocks until ctx is cancelled. The config lives in
// the database; importPath (the -config flag) imports a config file into it.
// It returns a non-nil error only on a fatal startup or unrecoverable failure;
// a clean shutdown via ctx returns nil.
func Run(ctx context.Context, importPath string, verbosity int) error {
	db, err := store.Open("meshcore.db")
	if err != nil {
		return fmt.Errorf("database open: %w", err)
	}
	defer db.Close()

	cfg, err := resolveConfig(db, importPath)
	if err != nil {
		return fmt.Errorf("resolving config: %w", err)
	}

	logLevel := ""
	if cfg.LogLevel != nil {
		logLevel = *cfg.LogLevel
	}
	logging.Configure(verbosity, logLevel)

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	reconnectCh := make(chan struct{}, 1)

	ms, err := modem.Setup(ctx, cfg)
	if err != nil {
		return fmt.Errorf("modem setup: %w", err)
	}
	ms.StartDeadWatcher(reconnectCh)
	mux := node.NewRadioMux(ms.Modem, modem.MuxOptions(ms)...)

	srv := api.NewServer(db, web.Assets(), slog.Default())
	reload := func() error {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return p.Signal(syscall.SIGHUP)
	}

	listenAddr := defaultListenAddr
	if cfg.ListenAddr != nil && *cfg.ListenAddr != "" {
		listenAddr = *cfg.ListenAddr
	}
	httpServer := &http.Server{Addr: listenAddr, Handler: srv}
	go func() {
		slog.Info("web UI listening", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
		}
	}()

	wirePacketLogger(mux, db, srv)

	echoTracker := echo.NewTracker(db, srv.Hub(), slog.Default())

	// The monitor service is started once and lives for the whole process: it
	// persists across config reloads / modem reconnects (which recreate the
	// companion set). It resolves the live companions through compReg, which we
	// re-point on every reload, and derives its targets from contact metadata.
	compReg := newCompanionRegistry()
	mon := monitor.New(db, srv.Hub(), newContactLister(compReg, db), slog.Default())
	mon.RegisterCollector("repeater", newRepeaterCollector(compReg, db, slog.Default()))
	mon.RegisterCollector("companion", newCompanionCollector(compReg, slog.Default()))
	mon.Start(ctx)
	srv.SetPoller(mon) // long-lived: set once, never swapped on reload

	companions, err := startCompanions(ctx, cfg, ms, mux, db, srv.Hub(), echoTracker)
	if err != nil {
		ms.Close()
		return fmt.Errorf("companion startup: %w", err)
	}
	compReg.set(companions)
	srv.SetBackend(newBackend(companions, cfg, db, reload))

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down...")
			httpServer.Close()
			stopCompanions(companions)
			ms.Close()
			return nil

		case <-sighup:
			slog.Info("SIGHUP received, reloading config...")

			newCfg, err := loadConfigFromDB(db)
			if err != nil {
				slog.Error("config reload failed, keeping current config", "error", err)
				continue
			}
			if len(newCfg.Companions) == 0 {
				slog.Error("reloaded config has no companions, keeping current config")
				continue
			}

			newLogLevel := ""
			if newCfg.LogLevel != nil {
				newLogLevel = *newCfg.LogLevel
			}
			logging.Configure(verbosity, newLogLevel)

			var stats reloadStats
			if config.ModemSettingsChanged(cfg, newCfg) {
				slog.Info("modem config changed, reconnecting...")
				stats.stopped = len(companions)
				stopCompanions(companions)
				ms.Close()
				ms, mux, err = reconnectModem(ctx, newCfg, db, srv, reconnectCh)
				if err != nil {
					return fmt.Errorf("modem reconnect after reload: %w", err)
				}
				companions, err = startCompanions(ctx, newCfg, ms, mux, db, srv.Hub(), echoTracker)
				if err != nil {
					ms.Close()
					return fmt.Errorf("companion restart after reload: %w", err)
				}
				stats.started = len(companions)
			} else {
				companions, stats, err = reloadCompanions(ctx, cfg, newCfg, companions, ms, mux, db, srv.Hub(), echoTracker)
				if err != nil {
					ms.Close()
					return fmt.Errorf("companion restart after reload: %w", err)
				}
			}
			cfg = newCfg
			compReg.set(companions)
			srv.SetBackend(newBackend(companions, cfg, db, reload))
			slog.Info("config reloaded", "started", stats.started, "stopped", stats.stopped, "kept", stats.kept)

		case <-reconnectCh:
			slog.Warn("modem read loop exited, reconnecting...")

			stopCompanions(companions)
			ms.Close()

			ms, mux, err = reconnectModemWithBackoff(ctx, cfg, db, srv, reconnectCh)
			if err != nil {
				slog.Error("modem reconnect aborted", "error", err)
				return nil
			}

			companions, err = startCompanions(ctx, cfg, ms, mux, db, srv.Hub(), echoTracker)
			if err != nil {
				ms.Close()
				return fmt.Errorf("companion restart after reconnect: %w", err)
			}
			compReg.set(companions)
			srv.SetBackend(newBackend(companions, cfg, db, reload))
			slog.Info("modem reconnected")
		}
	}
}

func startCompanions(ctx context.Context, cfg *config.Config, ms *modem.State, mux *node.RadioMux, db *store.Store, hub *api.Hub, echoTracker *echo.Tracker) ([]*companion.Companion, error) {
	companions, _, err := reloadCompanions(ctx, nil, cfg, nil, ms, mux, db, hub, echoTracker)
	return companions, err
}

type reloadStats struct {
	started, stopped, kept int
}

// reloadCompanions reconciles the running companion set with newCfg: running
// instances whose effective block is unchanged are reused (no restart, no
// initial advert, repeater/room sessions survive); everything else is stopped
// and/or built fresh. The result follows newCfg's companion order. A nil
// oldCfg/running builds everything (startup, modem reconnect). On error all
// instances — including reused ones — are stopped, since the caller exits the
// process.
func reloadCompanions(ctx context.Context, oldCfg, newCfg *config.Config, running []*companion.Companion, ms *modem.State, mux *node.RadioMux, db *store.Store, hub *api.Hub, echoTracker *echo.Tracker) ([]*companion.Companion, reloadStats, error) {
	keep := unchangedCompanions(oldCfg, newCfg)

	var stats reloadStats
	reusable := make(map[string]*companion.Companion, len(running))
	for _, c := range running {
		if keep[c.Name()] {
			reusable[c.Name()] = c
			continue
		}
		c.Stop()
		stats.stopped++
	}

	var companions, fresh []*companion.Companion
	stopAll := func() {
		stopCompanions(fresh)
		for _, c := range reusable {
			c.Stop()
		}
	}

	for _, compCfg := range effectiveCompanionConfigs(newCfg) {
		if inst, ok := reusable[compCfg.Name]; ok {
			companions = append(companions, inst)
			stats.kept++
			continue
		}
		c, err := companion.NewCompanion(compCfg, mux, db, hub, echoTracker, ms.Stats, ms.RecvErrors)
		if err != nil {
			stopAll()
			return nil, stats, fmt.Errorf("creating companion %q: %w", compCfg.Name, err)
		}
		if err := c.Start(ctx); err != nil {
			stopAll()
			return nil, stats, fmt.Errorf("starting companion %q: %w", compCfg.Name, err)
		}
		companions = append(companions, c)
		fresh = append(fresh, c)
		stats.started++
		slog.Info("started companion", "companion", compCfg.Name)
	}

	hydratePeerTables(db, fresh)

	return companions, stats, nil
}

// effectiveCompanionConfigs returns the per-companion blocks exactly as
// NewCompanion receives them: the single top-level mqtt block feeds one node's
// observer — the companion named by mqtt.node (the first companion when
// unset) — so it is injected into that block here.
func effectiveCompanionConfigs(cfg *config.Config) []config.CompanionConfig {
	mqttNode := ""
	if cfg.Mqtt.IsEnabled() && len(cfg.Mqtt.Brokers) > 0 {
		if cfg.Mqtt.Node != nil && *cfg.Mqtt.Node != "" {
			mqttNode = *cfg.Mqtt.Node
		} else if len(cfg.Companions) > 0 {
			mqttNode = cfg.Companions[0].Name
		}
	}

	blocks := make([]config.CompanionConfig, len(cfg.Companions))
	copy(blocks, cfg.Companions)
	for i := range blocks {
		if blocks[i].Name == mqttNode && mqttNode != "" {
			blocks[i].Mqtt = cfg.Mqtt
		}
	}
	return blocks
}

// unchangedCompanions returns the names whose effective companion block is
// JSON-identical between the two configs. Marshal errors mark a block as
// changed, erring towards a restart.
func unchangedCompanions(old, new_ *config.Config) map[string]bool {
	keep := make(map[string]bool)
	if old == nil {
		return keep
	}

	oldJSON := make(map[string][]byte, len(old.Companions))
	for _, b := range effectiveCompanionConfigs(old) {
		if j, err := json.Marshal(b); err == nil {
			oldJSON[b.Name] = j
		}
	}
	for _, b := range effectiveCompanionConfigs(new_) {
		prev, ok := oldJSON[b.Name]
		if !ok {
			continue
		}
		if j, err := json.Marshal(b); err == nil && bytes.Equal(prev, j) {
			keep[b.Name] = true
		}
	}
	return keep
}

func stopCompanions(companions []*companion.Companion) {
	for _, comp := range companions {
		comp.Stop()
	}
}

// hydratePeerTables seeds the companions' in-memory peer tables from the
// database so a fresh process knows about previously-seen peers. OutPath is
// intentionally not seeded: send-paths are learned-only (flood first).
func hydratePeerTables(db *store.Store, companions []*companion.Companion) {
	if len(companions) == 0 {
		return
	}

	peers, err := db.Peers.LoadAll()
	if err != nil {
		slog.Error("failed to load peers for hydration", "error", err)
		return
	}
	if len(peers) == 0 {
		return
	}

	for _, sp := range peers {
		id, err := meshcore.NewIdentityFromBytes(sp.PubKey)
		if err != nil {
			slog.Debug("skipping peer with invalid pubkey", "error", err)
			continue
		}

		np := &node.Peer{
			Identity:            id,
			Name:                sp.Name,
			Type:                sp.Type,
			Lat:                 sp.Lat,
			Lon:                 sp.Lon,
			Feat1:               sp.Feat1,
			Feat2:               sp.Feat2,
			LastAdvertTimestamp: sp.LastAdvertTS,
			LastSeen:            sp.LastSeen,
			SNR:                 derefFloat32(sp.SNR),
			RSSI:                derefInt8(sp.RSSI),
		}

		for _, c := range companions {
			c.Node().Peers().Insert(np)
		}
	}

	slog.Info("hydrated peer tables from database", "peers", len(peers), "companions", len(companions))
}

// reconnectModem performs a single modem.Setup attempt and rebuilds the mux,
// dead-watcher, and packet logger. Returns the new state on success.
func reconnectModem(ctx context.Context, cfg *config.Config, db *store.Store, srv *api.Server, reconnectCh chan struct{}) (*modem.State, *node.RadioMux, error) {
	ms, err := modem.Setup(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	ms.StartDeadWatcher(reconnectCh)
	mux := node.NewRadioMux(ms.Modem, modem.MuxOptions(ms)...)
	wirePacketLogger(mux, db, srv)
	return ms, mux, nil
}

// reconnectModemWithBackoff retries reconnectModem with capped exponential
// backoff until the context is cancelled. Drains spurious reconnectCh sends
// (e.g. from a half-open transport flapping) so they don't queue up.
func reconnectModemWithBackoff(ctx context.Context, cfg *config.Config, db *store.Store, srv *api.Server, reconnectCh chan struct{}) (*modem.State, *node.RadioMux, error) {
	const (
		initialDelay = 1 * time.Second
		maxDelay     = 30 * time.Second
	)
	delay := initialDelay
	for {
		// Drain any reconnect signal queued during the previous lifetime.
		select {
		case <-reconnectCh:
		default:
		}
		ms, mux, err := reconnectModem(ctx, cfg, db, srv, reconnectCh)
		if err == nil {
			return ms, mux, nil
		}
		slog.Warn("modem reconnect attempt failed", "error", err, "retryIn", delay)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func derefInt8(p *int8) int8 {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat32(p *float64) float32 {
	if p == nil {
		return 0
	}
	return float32(*p)
}
