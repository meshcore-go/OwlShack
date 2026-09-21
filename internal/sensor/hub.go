package sensor

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"sync"
	"time"
)

// readTimeout bounds one sensor's read so a wedged bus cannot stall the whole pass.
const readTimeout = 5 * time.Second

// Hub owns the configured sensors and is the only thing a consumer talks to. Consumers pull with
// Snapshot or subscribe with OnUpdate; neither needs to know a Provider exists.
type Hub struct {
	log       *slog.Logger
	providers map[string]Provider
	order     []string

	// pollMu serialises a poll pass against reconfiguration, so Set can never close a sensor the
	// pass is mid-read on. ponytail: one lock for the whole pass, which makes Set wait out a slow
	// read; split per-sensor if a wedged bus ever makes adding one sensor feel stuck.
	pollMu sync.Mutex

	// mu guards the fields a consumer reads. It is never held across a sensor read, so Snapshot
	// answers immediately however slow the hardware is.
	mu        sync.RWMutex
	entries   map[int64]*entry
	listeners []func([]Status)

	// kick asks the poll loop for an immediate pass, so a sensor just added shows a reading now
	// rather than at the next tick. Buffered and sent to without blocking: one pending kick is enough.
	kick chan struct{}
}

// entry is one configured sensor. sensor stays nil until a read opens it, and an open that fails is
// retried on the next pass - a device that was unplugged and put back must recover on its own.
type entry struct {
	spec     Spec
	sensor   Sensor
	readings []Reading
	at       time.Time
	err      string
}

func NewHub(log *slog.Logger, providers ...Provider) *Hub {
	h := &Hub{
		log:       log.With("component", "sensor"),
		providers: make(map[string]Provider, len(providers)),
		entries:   map[int64]*entry{},
		kick:      make(chan struct{}, 1),
	}
	for _, p := range providers {
		h.providers[p.ID()] = p
		h.order = append(h.order, p.ID())
	}
	return h
}

// Providers lists every provider with whether it can run here.
func (h *Hub) Providers(ctx context.Context) []ProviderInfo {
	out := make([]ProviderInfo, 0, len(h.order))
	for _, id := range h.order {
		p := h.providers[id]
		ok, reason := p.Available(ctx)
		out = append(out, ProviderInfo{ID: id, Label: p.Label(), Available: ok, Reason: reason})
	}
	return out
}

// Discover asks one provider what it can see. An unavailable provider returns its reason as the
// error rather than an empty list, which would read as "nothing is attached".
func (h *Hub) Discover(ctx context.Context, providerID string) ([]Candidate, error) {
	p, ok := h.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerID)
	}
	if ok, reason := p.Available(ctx); !ok {
		return nil, fmt.Errorf("%s is unavailable: %s", p.Label(), reason)
	}
	return p.Discover(ctx)
}

// Set reconciles the live set to the configured one. A spec that has not changed keeps its open
// sensor and its last reading, so adding one sensor does not reopen the others.
func (h *Hub) Set(specs []Spec) {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	h.mu.Lock()
	next := make(map[int64]*entry, len(specs))
	added := false
	for _, s := range specs {
		if old, ok := h.entries[s.ID]; ok && specEqual(old.spec, s) {
			next[s.ID] = old
			continue
		}
		if old, ok := h.entries[s.ID]; ok {
			closeSensor(old.sensor)
		}
		next[s.ID] = &entry{spec: s}
		added = true
	}
	for id, old := range h.entries {
		if _, kept := next[id]; !kept {
			closeSensor(old.sensor)
		}
	}
	h.entries = next
	h.mu.Unlock()

	if added {
		h.requestPass()
	} else {
		h.notify()
	}
}

// Snapshot is the pull seam: every configured sensor with its last result, in id order.
func (h *Hub) Snapshot() []Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked()
}

func (h *Hub) snapshotLocked() []Status {
	out := make([]Status, 0, len(h.entries))
	for _, e := range h.entries {
		out = append(out, Status{
			Spec: e.spec, Readings: append([]Reading(nil), e.readings...), At: e.at, Err: e.err,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.ID < out[j].Spec.ID })
	return out
}

// OnUpdate is the push seam: fn runs after every poll pass and every configuration change.
func (h *Hub) OnUpdate(fn func([]Status)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listeners = append(h.listeners, fn)
}

// Poll reads every sensor on each tick, and again whenever a change asks for an immediate pass.
func (h *Hub) Poll(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	h.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.kick:
		}
		h.pass(ctx)
	}
}

// Close releases every open sensor. The configured set is left alone: Close ends this process's use
// of the hardware, it does not unconfigure anything.
func (h *Hub) Close() {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		closeSensor(e.sensor)
		e.sensor = nil
	}
}

// pass reads every entry once, sequentially: the sensors share buses, and a handful of local reads
// is not worth the contention a fan-out would add.
func (h *Hub) pass(ctx context.Context) {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	h.mu.RLock()
	todo := make([]*entry, 0, len(h.entries))
	for _, e := range h.entries {
		todo = append(todo, e)
	}
	h.mu.RUnlock()

	for _, e := range todo {
		h.read(ctx, e)
	}
	h.notify()
}

// read opens the sensor if it is not open yet, then reads it. Both happen outside h.mu: the
// providers map is fixed at construction and pollMu already excludes Set, so the only thing the
// lock is needed for is publishing the result.
func (h *Hub) read(ctx context.Context, e *entry) {
	if e.sensor == nil {
		p, ok := h.providers[e.spec.Provider]
		if !ok {
			h.fail(e, fmt.Sprintf("unknown provider %q", e.spec.Provider))
			return
		}
		s, err := p.Open(e.spec)
		if err != nil {
			h.fail(e, err.Error())
			return
		}
		h.mu.Lock()
		e.sensor = s
		h.mu.Unlock()
	}

	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	readings, err := e.sensor.Read(rctx)
	if err != nil {
		h.log.Warn("sensor read failed", "sensor", e.spec.Name, "provider", e.spec.Provider, "error", err)
		h.fail(e, err.Error())
		return
	}
	h.mu.Lock()
	e.readings, e.at, e.err = readings, time.Now(), ""
	h.mu.Unlock()
}

// fail records why the last attempt failed. The previous readings and their timestamp are left
// alone: a consumer showing the last good value with its age is honest, where blanking it would
// lose the only evidence the sensor ever worked.
func (h *Hub) fail(e *entry, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e.err = msg
}

func (h *Hub) notify() {
	h.mu.RLock()
	snap := h.snapshotLocked()
	listeners := make([]func([]Status), len(h.listeners))
	copy(listeners, h.listeners)
	h.mu.RUnlock()
	for _, fn := range listeners {
		fn(snap)
	}
}

func (h *Hub) requestPass() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func specEqual(a, b Spec) bool {
	return a.Provider == b.Provider && a.Kind == b.Kind && maps.Equal(a.Options, b.Options)
}

func closeSensor(s Sensor) {
	if c, ok := s.(Closer); ok {
		_ = c.Close()
	}
}
