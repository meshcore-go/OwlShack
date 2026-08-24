package app

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/modem"
	"github.com/meshcore-go/meshcore-bot/internal/node/repeater"
	"github.com/meshcore-go/meshcore-bot/internal/store"
	"github.com/meshcore-go/meshcore-go/node"
)

// The repeater is a singleton (at most one runs — a radio hosts one relay
// identity), so its lifecycle is a simple start/stop/reconcile rather than the
// diff-based set reconciliation companions need.

func startRepeater(ctx context.Context, cfg *config.Config, mux *node.RadioMux, db *store.Store, hub *api.Hub, stats modem.StatsProvider, reload func() error) (*repeater.Repeater, error) {
	if cfg.Repeater == nil {
		return nil, nil
	}
	hooks := repeater.Hooks{
		Reconfigure: repeaterReconfigurer(db, reload),
		PollStats:   statsPoller(stats),
	}
	rep, err := repeater.NewRepeater(*cfg.Repeater, mux, db, hub, hooks)
	if err != nil {
		return nil, fmt.Errorf("creating repeater %q: %w", cfg.Repeater.Name, err)
	}
	if err := rep.Start(ctx); err != nil {
		return nil, fmt.Errorf("starting repeater %q: %w", cfg.Repeater.Name, err)
	}
	slog.Info("started repeater", "repeater", cfg.Repeater.Name)
	return rep, nil
}

func stopRepeater(rep *repeater.Repeater) {
	if rep != nil {
		rep.Stop()
	}
}

// reloadRepeater reconciles the running repeater with newCfg: an unchanged
// block keeps the running instance (no relay gap, no re-advert); otherwise the
// old one is stopped and the new one (if any) started.
func reloadRepeater(ctx context.Context, oldCfg, newCfg *config.Config, running *repeater.Repeater, mux *node.RadioMux, db *store.Store, hub *api.Hub, stats modem.StatsProvider, reload func() error) (*repeater.Repeater, error) {
	// reflect.DeepEqual handles nil/one-nil/deep on the two *RepeaterConfig.
	if running != nil && oldCfg != nil && reflect.DeepEqual(oldCfg.Repeater, newCfg.Repeater) {
		return running, nil
	}
	// Region-only change: apply it to the live node instead of restarting, so
	// the neighbour list, learned routes and relay counters survive (region
	// edits are frequent — every add/remove/deny-flood toggle from the UI).
	if running != nil && oldCfg != nil && onlyRegionsDiffer(oldCfg.Repeater, newCfg.Repeater) {
		running.ApplyRegions(newCfg.Repeater.Regions, newCfg.Repeater.DefaultRegion)
		return running, nil
	}
	stopRepeater(running)
	return startRepeater(ctx, newCfg, mux, db, hub, stats, reload)
}

// onlyRegionsDiffer reports whether a and b are identical apart from their
// Regions / DefaultRegion — the changes the reload path can apply live (no
// node restart).
func onlyRegionsDiffer(a, b *config.RepeaterConfig) bool {
	if a == nil || b == nil {
		return false
	}
	x, y := *a, *b
	x.Regions, y.Regions = nil, nil
	x.DefaultRegion, y.DefaultRegion = "", ""
	return reflect.DeepEqual(x, y)
}

// statsPoller adapts the modem's device-stats provider to the plain func the
// repeater node consumes (keeps the node package decoupled from modem). nil
// provider → nil poller (noise floor / battery stay 0).
func statsPoller(stats modem.StatsProvider) func(context.Context) (int16, uint16) {
	if stats == nil {
		return nil
	}
	return func(ctx context.Context) (int16, uint16) {
		ds := stats.Stats(ctx)
		return ds.NoiseFloor, ds.BatteryMV
	}
}

// repeaterReconfigurer returns the callback the repeater node uses to apply an
// over-mesh CLI `set`/`password`/`region` change: it loads the current config,
// applies the mutation to the repeater block, validates the WHOLE config (the
// crash guard — a bad value is rejected before it persists), writes it back,
// then triggers a reload so the running node picks it up. Mirrors configMutate
// but scoped to the repeater block. Runs on its own goroutine (the node calls
// it off its dispatch path), so the reload's node-restart can't deadlock.
func repeaterReconfigurer(db *store.Store, reload func() error) func(func(*config.RepeaterConfig)) error {
	return func(mutate func(*config.RepeaterConfig)) error {
		ctx := context.Background()
		return writeConfigTx(ctx, db, reload, func(rows *configRows) error {
			if rows.repeater == nil {
				return fmt.Errorf("no repeater configured")
			}
			cfg := assembleFromRows(rows)
			mutate(cfg.Repeater)
			if verr := cfg.Validate(); verr != nil {
				return verr
			}
			return writeRepeater(ctx, db, cfg)
		})
	}
}
