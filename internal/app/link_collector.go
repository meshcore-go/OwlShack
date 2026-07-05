package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/meshcore-go/meshcore-bot/internal/monitor"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// linkMonitorDefaultIntervalSecs is the poll cadence for a link monitor that
// doesn't override it — one trace round-trip is a few seconds of airtime, so
// 15 minutes is far more frequent than the 6h node-monitoring default without
// being excessive.
const linkMonitorDefaultIntervalSecs = 900

// newMergedLister concatenates multiple Listers into one, so monitor.Service
// (which only takes a single Lister) can poll both contact-derived targets
// and link-monitor targets on the same schedule.
func newMergedLister(ls ...monitor.Lister) monitor.ListerFunc {
	return func(ctx context.Context) ([]monitor.Target, error) {
		var out []monitor.Target
		for _, l := range ls {
			t, err := l.Targets(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, t...)
		}
		return out, nil
	}
}

// newLinkLister builds a monitor.Lister from enabled link_monitors rows. Each
// row's synthetic key stands in for Target.Pubkey, letting the existing
// node_metrics/node_state/history plumbing carry link data unchanged. Links
// whose companion no longer resolves in the registry (mid-reload) are skipped
// for the cycle rather than erroring the whole listing.
func newLinkLister(reg *companionRegistry, db *store.Store) monitor.ListerFunc {
	return func(ctx context.Context) ([]monitor.Target, error) {
		links, err := db.LinkMonitors.ListEnabled(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing enabled link monitors: %w", err)
		}
		targets := make([]monitor.Target, 0, len(links))
		for _, l := range links {
			companion, err := db.Companions.Get(ctx, l.CompanionID)
			if err != nil || companion == nil {
				continue
			}
			interval := l.IntervalSecs
			if interval <= 0 {
				interval = linkMonitorDefaultIntervalSecs
			}
			targets = append(targets, monitor.Target{
				Pubkey:       l.Key,
				CompanionID:  companion.Name,
				Kind:         "link",
				IntervalSecs: int64(interval),
				RetrySecs:    int64(l.RetrySecs),
				MaxRetries:   l.MaxRetries,
			})
		}
		return targets, nil
	}
}

// linkCollector polls a monitored link by running one trace over its saved
// path and mapping the result onto per-hop SNR readings plus a delivery flag.
// A timed-out trace is a measurement (packet loss), not a failed poll — only
// an inability to send (companion down, encoding failure) fails the poll.
type linkCollector struct {
	reg *companionRegistry
	db  *store.Store
	log *slog.Logger
}

func newLinkCollector(reg *companionRegistry, db *store.Store, log *slog.Logger) *linkCollector {
	if log == nil {
		log = slog.Default()
	}
	return &linkCollector{reg: reg, db: db, log: log.With("component", "link-collector")}
}

// Collect runs under monitor.Service's pollMu (the scheduler already holds it
// for the whole poll call) — it must NOT also acquire the signal-test
// runner's airtime lock (the same mutex), or a link poll would deadlock
// against itself.
func (lc *linkCollector) Collect(ctx context.Context, t monitor.Target) (*monitor.CollectResult, error) {
	lm, err := lc.db.LinkMonitors.GetByKey(ctx, t.Pubkey)
	if err != nil {
		return nil, fmt.Errorf("loading link monitor: %w", err)
	}
	if lm == nil {
		return nil, fmt.Errorf("link monitor not found")
	}
	c, ok := lc.reg.find(t.CompanionID)
	if !ok {
		return nil, fmt.Errorf("companion %q is not running", t.CompanionID)
	}

	out, err := c.RunTrace(ctx, lm.Path, uint8(lm.PathHashSize))
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}

	name := lm.Label
	if name == "" {
		name = "link"
	}
	// A timed-out trace is valid data (packet loss) and always gets recorded
	// like any other poll. It only asks the scheduler for a fast retry when
	// the user has explicitly opted in (MaxRetries > 0) — links that never
	// touch the retry settings (MaxRetries == 0, "use default") keep today's
	// behavior of one independent reading per normal interval, so enabling
	// retries on one link doesn't silently add extra airtime to every other
	// one.
	res := &monitor.CollectResult{Name: name, RetryFailure: lm.MaxRetries > 0 && !out.Complete}

	ok01 := 0.0
	if out.Complete {
		ok01 = 1
	}
	res.Readings = append(res.Readings, monitor.Reading{Metric: "success", Value: ok01})
	for i, snr := range out.HopSNRs {
		res.Readings = append(res.Readings, monitor.Reading{Metric: fmt.Sprintf("snr_hop%d", i+1), Value: snr})
	}
	if out.Complete {
		res.Readings = append(res.Readings, monitor.Reading{Metric: "elapsed_ms", Value: float64(out.Elapsed.Milliseconds())})
		if out.SNR != nil {
			res.Readings = append(res.Readings, monitor.Reading{Metric: "last_snr", Value: *out.SNR})
		}
	}
	return res, nil
}

var _ monitor.Collector = (*linkCollector)(nil)
