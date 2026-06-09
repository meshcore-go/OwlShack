// Package app wires the bot's subsystems together and runs the supervisor
// loop: it owns the database, modem, HTTP server and companion lifecycle, and
// handles config reloads (SIGHUP) and modem reconnects. It is the only place
// that depends on every other internal package.
package app

import (
	"context"
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
	"github.com/meshcore-go/meshcore-bot/internal/modem"
	"github.com/meshcore-go/meshcore-bot/internal/node/companion"
	"github.com/meshcore-go/meshcore-bot/internal/store"
	"github.com/meshcore-go/meshcore-bot/web"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

const defaultListenAddr = ":8080"

// Run starts the bot with the given config and blocks until ctx is cancelled.
// It returns a non-nil error only on a fatal startup or unrecoverable failure;
// a clean shutdown via ctx returns nil.
func Run(ctx context.Context, cfg *config.Config, configPath string) error {
	db, err := store.Open("meshcore.db")
	if err != nil {
		return fmt.Errorf("database open: %w", err)
	}
	defer db.Close()

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

	companions, err := startCompanions(ctx, cfg, ms, mux, db, srv.Hub(), echoTracker)
	if err != nil {
		ms.Close()
		return fmt.Errorf("companion startup: %w", err)
	}
	srv.SetBackend(newBackend(companions, cfg, configPath, reload))

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

			newCfg, _, err := config.Load(configPath)
			if err != nil {
				slog.Error("config reload failed, keeping current config", "error", err)
				continue
			}
			if len(newCfg.Companions) == 0 {
				slog.Error("reloaded config has no companions, keeping current config")
				continue
			}

			stopCompanions(companions)

			if config.ModemSettingsChanged(cfg, newCfg) {
				slog.Info("modem config changed, reconnecting...")
				ms.Close()
				ms, mux, err = reconnectModem(ctx, newCfg, db, srv, reconnectCh)
				if err != nil {
					return fmt.Errorf("modem reconnect after reload: %w", err)
				}
			}

			companions, err = startCompanions(ctx, newCfg, ms, mux, db, srv.Hub(), echoTracker)
			if err != nil {
				ms.Close()
				return fmt.Errorf("companion restart after reload: %w", err)
			}
			cfg = newCfg
			srv.SetBackend(newBackend(companions, cfg, configPath, reload))
			slog.Info("config reloaded successfully")

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
			srv.SetBackend(newBackend(companions, cfg, configPath, reload))
			slog.Info("modem reconnected")
		}
	}
}

func startCompanions(ctx context.Context, cfg *config.Config, ms *modem.State, mux *node.RadioMux, db *store.Store, hub *api.Hub, echoTracker *echo.Tracker) ([]*companion.Companion, error) {
	var companions []*companion.Companion

	for _, compCfg := range cfg.Companions {
		c, err := companion.NewCompanion(compCfg, mux, db, hub, echoTracker, ms.Stats, ms.RecvErrors)
		if err != nil {
			stopCompanions(companions)
			return nil, fmt.Errorf("creating companion %q: %w", compCfg.Name, err)
		}
		if err := c.Start(ctx); err != nil {
			stopCompanions(companions)
			return nil, fmt.Errorf("starting companion %q: %w", compCfg.Name, err)
		}
		companions = append(companions, c)
		slog.Info("started companion", "companion", compCfg.Name)
	}

	hydratePeerTables(db, companions)

	return companions, nil
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
