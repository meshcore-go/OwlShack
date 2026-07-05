// Package monitor runs the background node-monitoring poller: on a staggered
// schedule it asks each registered Collector for the current readings of the
// nodes it owns, persists them to the generic time-series store (node_metrics),
// updates each node's latest snapshot (node_state), and broadcasts live updates
// over the "metrics" WebSocket topic. It also prunes old time-series rows.
//
// The engine is type-agnostic on two seams:
//   - a Lister supplies the current set of monitor Targets (the app implements
//     it by scanning companions' contacts for the per-node monitor flag — which
//     nodes are monitored is interactive state, not config);
//   - a Collector (one per node kind — repeater, sensor, …) performs the actual
//     protocol round-trip.
//
// So the monitor package never imports the companion runtime or the repeater
// client, keeping the dependency arrow pointing inward (app -> monitor -> store).
package monitor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// WSTopicMetrics is the WebSocket topic live readings are broadcast on.
const WSTopicMetrics = "metrics"

// Global scheduler tuning. These are infrastructure constants, not user config:
// per-node cadence is set per monitor (Target.IntervalSecs); everything below is
// poller-wide behaviour that isn't meaningful to vary per node.
const (
	// pollTick is how often the scheduler wakes to check which monitors are due.
	pollTick = 30 * time.Second
	// requestTimeout bounds a single Collector.Collect call (a repeater poll
	// chains login + status + telemetry + neighbours, each with its own tighter
	// per-request timeout, so this backstop is generous).
	requestTimeout = 2 * time.Minute
	// defaultInterval is the poll cadence for a monitor that doesn't set its own.
	defaultInterval = 6 * time.Hour
	// stagger is the gap inserted between polling consecutive nodes in a cycle,
	// to avoid bursts of RF collisions.
	stagger = 30 * time.Second
	// retention bounds raw time-series storage; older rows are pruned.
	retention = 90 * 24 * time.Hour
	// pruneInterval is how often the retention prune runs.
	pruneInterval = 6 * time.Hour
	// retryInterval is when to re-attempt after a failed poll, instead of
	// waiting the full (potentially 6h) interval — transient login/RF failures
	// (common right after restart, before paths are re-learned) shouldn't blank
	// a node for hours.
	retryInterval = 5 * time.Minute
	// defaultMaxRetries bounds consecutive fast re-attempts after a failed poll
	// before the node falls back to its normal interval, so a permanently
	// unreachable node isn't polled on the retry cadence forever.
	defaultMaxRetries = 3
)

// DefaultIntervalSecs is defaultInterval in seconds. Exported so the API can
// report each node's effective poll cadence to the UI, which derives its
// staleness threshold from it rather than hardcoding one — keeping the default
// in a single place.
const DefaultIntervalSecs = int64(defaultInterval / time.Second)

// DefaultRetrySecs/DefaultMaxRetries are retryInterval/defaultMaxRetries in
// seconds/count, exported so the API can validate per-node retry overrides
// against the poller's actual defaults without duplicating the values.
const DefaultRetrySecs = int64(retryInterval / time.Second)
const DefaultMaxRetries = defaultMaxRetries

// Broadcaster is the subset of *api.Hub the monitor needs. Declared here so the
// package stays decoupled and unit-testable.
type Broadcaster interface {
	Broadcast(topic string, data any)
}

// Target identifies one node to poll and how. Supplied by the Lister each cycle.
type Target struct {
	Pubkey       []byte
	CompanionID  string   // which companion sends the requests
	Kind         string   // selects the Collector ("repeater", "sensor", …)
	IntervalSecs int64    // per-node cadence; 0 = use defaultInterval
	RetrySecs    int64    // re-attempt delay after a failed poll; 0 = default
	MaxRetries   int      // consecutive failed re-attempts before normal cadence; 0 = default, <0 = no retries
	Probes       []string // request bundles to run; nil/empty = all
}

// Lister supplies the current set of monitor targets. Called once per cycle, so
// adding/removing a monitor takes effect on the next tick with no restart.
type Lister interface {
	Targets(ctx context.Context) ([]Target, error)
}

// ListerFunc adapts a function to the Lister interface.
type ListerFunc func(ctx context.Context) ([]Target, error)

func (f ListerFunc) Targets(ctx context.Context) ([]Target, error) { return f(ctx) }

// Reading is one decoded value a Collector reports for a node. Channel is the
// CayenneLPP channel for sensor readings (0 for status fields with no channel).
type Reading struct {
	Metric  string
	Channel int
	Value   float64
}

// NeighborSample is one observed neighbour SNR a Collector reports.
type NeighborSample struct {
	Pubkey []byte
	SNR    *float64
}

// CollectResult is what a Collector returns for one poll of one node.
type CollectResult struct {
	// Name, if non-empty, updates the node's display name in node_state.
	Name string
	// Readings are the time-series values to persist.
	Readings []Reading
	// Neighbors are optional neighbour SNR samples (topology over time).
	Neighbors []NeighborSample
	// RetryFailure marks a poll that got valid data (and is persisted
	// normally) but should still be treated as a failed poll for retry
	// scheduling — e.g. a link monitor's trace timed out (no reply is itself
	// a measurement worth graphing), but the caller wants a fast re-attempt
	// per Target.RetrySecs/MaxRetries to tell an ephemeral drop from a
	// consistent one, rather than waiting out the full poll interval.
	RetryFailure bool
}

// Collector knows how to poll one kind of node and report its current readings.
// Implementations live in the app layer (so they may use the companion runtime
// and protocol clients) and are registered via Service.RegisterCollector.
type Collector interface {
	// Collect performs one poll of the node described by t and returns its
	// readings. It must honour ctx (which carries a per-request timeout) and not
	// block indefinitely. A returned error marks the poll failed; the node's
	// last_error is recorded and history is left untouched.
	Collect(ctx context.Context, t Target) (*CollectResult, error)
}

// Service is the poller engine.
type Service struct {
	st     *store.Store
	bc     Broadcaster
	log    *slog.Logger
	lister Lister

	mu         sync.RWMutex
	collectors map[string]Collector
	nextDue    map[string]time.Time // keyed by pubkey hex
	failures   map[string]int       // consecutive failed polls, keyed by pubkey hex

	// pollMu serializes the actual collector round-trips so a manual PollNow
	// can't run concurrently with a scheduled poll (which would put two RF
	// conversations on the air at once — the whole stagger design avoids that).
	pollMu sync.Mutex
}

// New builds a Service. Register a Collector per node kind, then call Start.
func New(st *store.Store, bc Broadcaster, lister Lister, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		st:         st,
		bc:         bc,
		log:        log.With("component", "monitor"),
		lister:     lister,
		collectors: make(map[string]Collector),
		nextDue:    make(map[string]time.Time),
		failures:   make(map[string]int),
	}
}

// rescheduleAfter sets the node's next due time following a poll. A failed poll
// re-attempts at retryDelay up to maxRetries consecutive times, then falls back
// to the normal interval (resetting the counter) so an unreachable node isn't
// polled on the short retry cadence forever. A success clears the counter.
func (s *Service) rescheduleAfter(key string, t Target, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	interval := defaultInterval
	if t.IntervalSecs > 0 {
		interval = time.Duration(t.IntervalSecs) * time.Second
	}
	if ok {
		delete(s.failures, key)
	} else {
		s.failures[key]++
		// MaxRetries resolution: 0 = poller default; negative (the UI's
		// "None" option sends -1) = explicitly no retries, so any failure
		// goes straight back to the normal interval instead of a fast
		// re-attempt.
		maxRetries := defaultMaxRetries
		if t.MaxRetries < 0 {
			maxRetries = 0
		} else if t.MaxRetries > 0 {
			maxRetries = t.MaxRetries
		}
		if s.failures[key] <= maxRetries {
			interval = retryInterval
			if t.RetrySecs > 0 {
				interval = time.Duration(t.RetrySecs) * time.Second
			}
		} else {
			if maxRetries > 0 {
				s.log.Info("monitor giving up after consecutive failures, resuming normal interval",
					"pubkey", key[:12], "failures", s.failures[key], "next", interval)
			}
			delete(s.failures, key)
		}
	}
	s.nextDue[key] = time.Now().Add(interval)
}

// Targets returns the current monitor target set from the lister — the nodes
// whose monitor toggle is on right now. The API uses this to list monitored
// nodes, so a freshly enrolled node shows up before its first poll completes.
func (s *Service) Targets(ctx context.Context) ([]Target, error) {
	return s.lister.Targets(ctx)
}

// AirtimeLock exposes the poll mutex so other radio-driving services (the
// signal-test runner) can serialize their own RF round-trips against monitor
// polls, one operation at a time, instead of contending for airtime. Callers
// must lock/unlock per-operation, not for an extended duration — holding it
// for a whole multi-minute test would starve scheduled polls.
func (s *Service) AirtimeLock() *sync.Mutex {
	return &s.pollMu
}

// RegisterCollector wires a Collector for the given node kind (e.g. "repeater").
// Safe to call before Start; calling for an existing kind replaces it.
func (s *Service) RegisterCollector(kind string, c Collector) {
	s.mu.Lock()
	s.collectors[kind] = c
	s.mu.Unlock()
}

// Start launches the scheduler and prune goroutines, running until ctx is
// cancelled. Start returns immediately. The poller idles when no nodes are
// flagged for monitoring, so there's no global enable switch.
func (s *Service) Start(ctx context.Context) {
	s.log.Info("node monitoring started", "defaultInterval", defaultInterval, "stagger", stagger, "retention", retention)
	go s.scheduleLoop(ctx)
	go s.pruneLoop(ctx)
}

// scheduleLoop wakes every pollTick, polls every monitor that's due, and sleeps
// again. Polls run sequentially within a cycle with a stagger gap between them,
// so one cycle never overlaps the next and RF requests don't burst.
func (s *Service) scheduleLoop(ctx context.Context) {
	s.runCycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollTick):
			s.runCycle(ctx)
		}
	}
}

func (s *Service) runCycle(ctx context.Context) {
	targets, err := s.lister.Targets(ctx)
	if err != nil {
		s.log.Error("listing monitor targets", "error", err)
		return
	}

	now := time.Now()
	first := true
	for _, t := range targets {
		key := hex.EncodeToString(t.Pubkey)
		s.mu.RLock()
		due := s.nextDue[key]
		s.mu.RUnlock()
		if !due.IsZero() && now.Before(due) {
			continue
		}

		// Stagger consecutive polls within a cycle (skip the gap before the first).
		if !first && stagger > 0 {
			if !sleepCtx(ctx, stagger) {
				return
			}
		}
		first = false

		s.pollMu.Lock()
		retryFailure, err := s.poll(ctx, t)
		s.pollMu.Unlock()

		s.rescheduleAfter(key, t, err == nil && !retryFailure)
	}
}

// poll runs one Collector call and persists/broadcasts the result. The
// returned error is non-nil only when the poll itself failed (no data to
// persist) — the scheduler retries on it and a manual PollNow surfaces it to
// the caller. retryFailure is set separately: it's true when the poll did
// get data (persisted normally) but the Collector still wants the scheduler
// to treat it as a failure for retry-scheduling purposes (CollectResult.
// RetryFailure — e.g. a link's trace timed out). Callers must hold s.pollMu.
func (s *Service) poll(ctx context.Context, t Target) (retryFailure bool, err error) {
	s.mu.RLock()
	collector := s.collectors[t.Kind]
	s.mu.RUnlock()

	pollTS := time.Now().Unix()
	state := store.NodeState{
		Pubkey:      t.Pubkey,
		CompanionID: t.CompanionID,
		Kind:        t.Kind,
		LastPollTS:  pollTS,
	}

	if collector == nil {
		state.LastError = "no collector registered for kind " + t.Kind
		s.log.Debug("no collector for target", "kind", t.Kind, "pubkey", hex.EncodeToString(t.Pubkey))
		s.persistState(state)
		return false, fmt.Errorf("%s", state.LastError)
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	res, err := collector.Collect(reqCtx, t)
	cancel()
	if err != nil {
		state.LastError = err.Error()
		s.log.Warn("monitor poll failed", "kind", t.Kind, "pubkey", hex.EncodeToString(t.Pubkey), "error", err)
		s.persistState(state)
		return false, err
	}

	state.LastOkTS = pollTS
	if res.Name != "" {
		state.Name = res.Name
	}

	metrics := make([]store.Metric, 0, len(res.Readings))
	snapshot := make(map[string]float64, len(res.Readings))
	for _, r := range res.Readings {
		metrics = append(metrics, store.Metric{
			TS:      pollTS,
			Pubkey:  t.Pubkey,
			Metric:  r.Metric,
			Channel: r.Channel,
			Value:   r.Value,
		})
		snapshot[r.Metric] = r.Value
	}
	if blob, mErr := json.Marshal(snapshot); mErr == nil {
		state.State = string(blob)
	}

	neighbors := make([]store.Neighbor, 0, len(res.Neighbors))
	for _, n := range res.Neighbors {
		neighbors = append(neighbors, store.Neighbor{
			TS:             pollTS,
			Pubkey:         t.Pubkey,
			NeighborPubkey: n.Pubkey,
			SNR:            n.SNR,
		})
	}

	// Persist metrics + neighbours + state on the writer goroutine. The poll
	// ctx is a per-request timeout that's cancelled once poll() returns, so the
	// async closure must use a background ctx instead of capturing it.
	s.st.WriteAsync(func() {
		if err := s.st.Metrics.RecordMetrics(context.Background(), metrics); err != nil {
			s.log.Error("recording metrics", "error", err)
		}
		if err := s.st.Metrics.RecordNeighbors(context.Background(), neighbors); err != nil {
			s.log.Error("recording neighbors", "error", err)
		}
		if err := s.st.Metrics.UpsertNodeState(context.Background(), &state); err != nil {
			s.log.Error("upserting node state", "error", err)
		}
	})

	if s.bc != nil {
		s.bc.Broadcast(WSTopicMetrics, map[string]any{
			"pubkey":      hex.EncodeToString(t.Pubkey),
			"companionId": t.CompanionID,
			"kind":        t.Kind,
			"name":        state.Name,
			"ts":          pollTS,
			"metrics":     snapshot,
		})
	}
	return res.RetryFailure, nil
}

// PollNow performs an immediate, out-of-band poll of a single node, bypassing
// the schedule — used by the UI's per-node "poll" button. It resolves the
// node's current target from the lister (so companion/kind/probes match a
// scheduled poll), runs the collector, persists + broadcasts exactly as a
// scheduled poll, and resets the node's next scheduled poll relative to now.
// Returns an error if the node isn't currently monitored, the poll fails, or a
// poll is already in flight.
func (s *Service) PollNow(ctx context.Context, pubkey []byte) error {
	if !s.pollMu.TryLock() {
		return fmt.Errorf("a poll is already in progress; try again shortly")
	}
	defer s.pollMu.Unlock()

	targets, err := s.lister.Targets(ctx)
	if err != nil {
		return fmt.Errorf("listing monitor targets: %w", err)
	}
	key := hex.EncodeToString(pubkey)
	var target *Target
	for i := range targets {
		if hex.EncodeToString(targets[i].Pubkey) == key {
			target = &targets[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("node is not currently monitored")
	}

	retryFailure, pollErr := s.poll(ctx, *target)
	s.rescheduleAfter(key, *target, pollErr == nil && !retryFailure)
	return pollErr
}

// persistState records a poll failure without clobbering the last-known-good
// snapshot (preserves last_ok_ts + metric state).
func (s *Service) persistState(state store.NodeState) {
	s.st.WriteAsync(func() {
		if err := s.st.Metrics.MarkPollFailure(context.Background(), &state); err != nil {
			s.log.Error("recording poll failure", "error", err)
		}
	})
}

// pruneLoop periodically drops time-series rows older than the retention window.
func (s *Service) pruneLoop(ctx context.Context) {
	// Prune once shortly after start, then on the interval.
	if !sleepCtx(ctx, time.Minute) {
		return
	}
	for {
		s.prune()
		select {
		case <-ctx.Done():
			return
		case <-time.After(pruneInterval):
		}
	}
}

func (s *Service) prune() {
	cutoff := time.Now().Add(-retention).Unix()
	s.st.WriteAsync(func() {
		removed, err := s.st.Metrics.PruneMetrics(context.Background(), cutoff)
		if err != nil {
			s.log.Error("pruning metrics", "error", err)
			return
		}
		if removed > 0 {
			s.log.Info("pruned old metrics", "rows", removed, "cutoff", cutoff)
		}
	})
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
