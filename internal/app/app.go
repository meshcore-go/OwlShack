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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

// applyListenEnvOverrides lets the HOST and PORT environment variables override
// the stored web listen address (precedence: env > DB config > default). Either
// may be set alone: an unset HOST binds all interfaces, an unset PORT keeps the
// configured one. This is for deployments (Docker, PaaS) that set the bind
// address out-of-band without editing the stored config.
func applyListenEnvOverrides(addr string) string {
	envHost, hasHost := os.LookupEnv("HOST")
	envPort, hasPort := os.LookupEnv("PORT")
	if !hasHost && !hasPort {
		return addr
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// addr wasn't a clean host:port (e.g. a bare ":4432" edge or malformed);
		// recover the port from a leading-colon form and leave host empty.
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	if hasHost {
		host = envHost
	}
	if hasPort && envPort != "" {
		port = envPort
	}

	overridden := net.JoinHostPort(host, port)
	slog.Info("web listen address overridden by environment", "from", addr, "to", overridden)
	return overridden
}

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
	listenAddr = applyListenEnvOverrides(listenAddr)
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
			// Zero companions is allowed (observer-only / first-run wizard skip);
			// reloadCompanions stops any running companions and starts none.

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
			slog.Info("config reloaded", "started", stats.started, "stopped", stats.stopped, "kept", stats.kept, "reloaded", stats.reloaded)

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
	started, stopped, kept, reloaded int
}

// reloadCompanions reconciles the running companion set with newCfg: running
// instances whose effective block is unchanged are reused (no restart, no
// initial advert, repeater/room sessions survive); everything else is stopped
// and/or built fresh. The result follows newCfg's companion order. A nil
// oldCfg/running builds everything (startup, modem reconnect). On error all
// instances — including reused ones — are stopped, since the caller exits the
// process.
func reloadCompanions(ctx context.Context, oldCfg, newCfg *config.Config, running []*companion.Companion, ms *modem.State, mux *node.RadioMux, db *store.Store, hub *api.Hub, echoTracker *echo.Tracker) ([]*companion.Companion, reloadStats, error) {
	oldBlocks := make(map[string]config.CompanionConfig)
	if oldCfg != nil {
		for _, b := range effectiveCompanionConfigs(oldCfg) {
			oldBlocks[b.Name] = b
		}
	}

	runningByName := make(map[string]*companion.Companion, len(running))
	for _, c := range running {
		runningByName[c.Name()] = c
	}

	// Classify each desired companion against the running set: keep as-is,
	// reload triggers in place, or (re)create fresh.
	type plan struct {
		block      config.CompanionConfig
		reuse      *companion.Companion // nil = build fresh
		reloadTrig bool
	}
	newBlocks := effectiveCompanionConfigs(newCfg)
	plans := make([]plan, 0, len(newBlocks))
	for _, nb := range newBlocks {
		inst, isRunning := runningByName[nb.Name]
		ob, hadOld := oldBlocks[nb.Name]
		switch {
		case isRunning && hadOld && blocksEqual(ob, nb):
			plans = append(plans, plan{block: nb, reuse: inst})
		case isRunning && hadOld && triggersOnlyChange(ob, nb):
			plans = append(plans, plan{block: nb, reuse: inst, reloadTrig: true})
		default:
			plans = append(plans, plan{block: nb}) // fresh build (new or full restart)
		}
	}

	// Instances we're keeping (reused as-is or trigger-reloaded in place);
	// every other running instance is stopped. Derived from plans so there's a
	// single source of truth.
	keep := make(map[*companion.Companion]bool)
	for _, p := range plans {
		if p.reuse != nil {
			keep[p.reuse] = true
		}
	}

	var stats reloadStats
	for _, c := range running {
		if !keep[c] {
			c.Stop()
			stats.stopped++
		}
	}

	var companions, fresh []*companion.Companion
	stopAll := func() {
		stopCompanions(fresh)
		for c := range keep {
			c.Stop()
		}
	}

	for _, p := range plans {
		if p.reuse != nil {
			if p.reloadTrig {
				if err := p.reuse.ReloadTriggers(p.block); err != nil {
					stopAll()
					return nil, stats, fmt.Errorf("reloading triggers for %q: %w", p.block.Name, err)
				}
				stats.reloaded++
			} else {
				stats.kept++
			}
			companions = append(companions, p.reuse)
			continue
		}
		c, err := companion.NewCompanion(p.block, mux, db, hub, echoTracker, ms.Stats, ms.RecvErrors)
		if err != nil {
			stopAll()
			return nil, stats, fmt.Errorf("creating companion %q: %w", p.block.Name, err)
		}
		if err := c.Start(ctx); err != nil {
			stopAll()
			return nil, stats, fmt.Errorf("starting companion %q: %w", p.block.Name, err)
		}
		companions = append(companions, c)
		fresh = append(fresh, c)
		stats.started++
		slog.Info("started companion", "companion", p.block.Name)
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

// blocksEqual reports whether two effective companion blocks are JSON-identical
// (so the running instance can be reused untouched). Marshal errors report
// "not equal", erring towards a restart.
func blocksEqual(a, b config.CompanionConfig) bool {
	aj, err1 := json.Marshal(a)
	bj, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(aj, bj)
}

// triggersOnlyChange reports whether two effective companion blocks differ
// solely in their Triggers field — the case ReloadTriggers can apply in place
// without a full restart (no re-advert, sessions survive). Everything else
// (identity, radio, position, mqtt, channels) must match exactly.
func triggersOnlyChange(a, b config.CompanionConfig) bool {
	a.Triggers = nil
	b.Triggers = nil
	return blocksEqual(a, b)
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
