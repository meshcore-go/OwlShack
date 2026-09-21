package sensor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeProvider is driven entirely from the test: every failure mode the hub has to keep
// distinguishable is reachable by setting a field.
type fakeProvider struct {
	mu        sync.Mutex
	available bool
	reason    string
	openErr   error
	readErr   error
	readings  []Reading
	opens     int
	closes    int
}

func (p *fakeProvider) ID() string    { return "fake" }
func (p *fakeProvider) Label() string { return "Fake" }

func (p *fakeProvider) Available(context.Context) (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.available, p.reason
}

func (p *fakeProvider) Discover(context.Context) ([]Candidate, error) {
	return []Candidate{{Kind: "thing", Label: "A thing", Options: map[string]string{"n": "1"}}}, nil
}

func (p *fakeProvider) Open(Spec) (Sensor, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.openErr != nil {
		return nil, p.openErr
	}
	p.opens++
	return &fakeSensor{p: p}, nil
}

func (p *fakeProvider) set(f func(*fakeProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

type fakeSensor struct{ p *fakeProvider }

func (s *fakeSensor) Read(context.Context) ([]Reading, error) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	if s.p.readErr != nil {
		return nil, s.p.readErr
	}
	return s.p.readings, nil
}

func (s *fakeSensor) Close() error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	s.p.closes++
	return nil
}

func newFake() *fakeProvider {
	return &fakeProvider{available: true, readings: []Reading{{Metric: Temperature, Value: 21.5, Unit: "C"}}}
}

func spec(id int64) Spec {
	return Spec{ID: id, Provider: "fake", Kind: "thing", Name: "s", Options: map[string]string{"n": "1"}}
}

// A configured sensor that has never been polled must not look like one reading zero, and one that
// has failed must not look like one that was never tried. These are the three states the whole
// framework exists to keep apart, so each is asserted rather than assumed.
func TestHub_ThreeStatesAreDistinguishable(t *testing.T) {
	p := newFake()
	h := NewHub(testLog(), p)
	h.Set([]Spec{spec(1)})

	got := h.Snapshot()
	if len(got) != 1 {
		t.Fatalf("snapshot has %d entries, want 1", len(got))
	}
	if !got[0].At.IsZero() || got[0].Err != "" || len(got[0].Readings) != 0 {
		t.Fatalf("before any poll: got %+v, want no readings, no timestamp and no error", got[0])
	}

	h.pass(t.Context())
	got = h.Snapshot()
	if got[0].At.IsZero() || got[0].Err != "" || len(got[0].Readings) != 1 {
		t.Fatalf("after a good poll: got %+v, want one reading, a timestamp and no error", got[0])
	}
	goodAt := got[0].At

	p.set(func(p *fakeProvider) { p.readErr = errors.New("bus went away") })
	h.pass(t.Context())
	got = h.Snapshot()
	if got[0].Err == "" {
		t.Fatal("after a failed poll: Err is empty, so a failing sensor is indistinguishable from a working one")
	}
	if !got[0].At.Equal(goodAt) || len(got[0].Readings) != 1 {
		t.Fatalf("after a failed poll: got %+v, want the last good reading and its timestamp kept", got[0])
	}
}

// An unavailable provider must not answer with an empty list, which reads as "nothing is attached".
func TestHub_DiscoverOnUnavailableProviderErrors(t *testing.T) {
	p := newFake()
	p.set(func(p *fakeProvider) { p.available, p.reason = false, "no bus here" })
	h := NewHub(testLog(), p)

	if _, err := h.Discover(t.Context(), "fake"); err == nil {
		t.Fatal("Discover on an unavailable provider returned no error")
	}

	// And the positive: with the provider available it does find something, so the check above is
	// not passing because Discover never works.
	p.set(func(p *fakeProvider) { p.available, p.reason = true, "" })
	found, err := h.Discover(t.Context(), "fake")
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover when available = (%v, %v), want one candidate and no error", found, err)
	}
}

// A sensor whose Open fails must stay in the list carrying the reason, and must be retried: a
// device that was unplugged and put back has to recover without a restart.
func TestHub_FailedOpenIsReportedAndRetried(t *testing.T) {
	p := newFake()
	p.set(func(p *fakeProvider) { p.openErr = errors.New("device busy") })
	h := NewHub(testLog(), p)
	h.Set([]Spec{spec(1)})

	h.pass(t.Context())
	got := h.Snapshot()
	if len(got) != 1 {
		t.Fatalf("a sensor that failed to open vanished from the list: %+v", got)
	}
	if got[0].Err != "device busy" {
		t.Fatalf("Err = %q, want the open failure", got[0].Err)
	}

	p.set(func(p *fakeProvider) { p.openErr = nil })
	h.pass(t.Context())
	got = h.Snapshot()
	if got[0].Err != "" || len(got[0].Readings) != 1 {
		t.Fatalf("after the device came back: got %+v, want a clean reading", got[0])
	}
}

// Reconfiguring must not disturb the sensors that did not change: reopening them would drop the
// readings of everything else every time one sensor is added.
func TestHub_SetKeepsUnchangedSensorsOpen(t *testing.T) {
	p := newFake()
	h := NewHub(testLog(), p)
	h.Set([]Spec{spec(1)})
	h.pass(t.Context())

	p.mu.Lock()
	opensAfterFirst := p.opens
	p.mu.Unlock()
	if opensAfterFirst != 1 {
		t.Fatalf("opens = %d after the first pass, want 1", opensAfterFirst)
	}

	h.Set([]Spec{spec(1), spec(2)})
	h.pass(t.Context())

	p.mu.Lock()
	opens, closes := p.opens, p.closes
	p.mu.Unlock()
	if opens != 2 {
		t.Errorf("opens = %d after adding a second sensor, want 2 (the first must not be reopened)", opens)
	}
	if closes != 0 {
		t.Errorf("closes = %d, want 0: nothing was removed", closes)
	}

	h.Set([]Spec{spec(2)})
	p.mu.Lock()
	closes = p.closes
	p.mu.Unlock()
	if closes != 1 {
		t.Errorf("closes = %d after removing a sensor, want 1", closes)
	}
	if got := h.Snapshot(); len(got) != 1 || got[0].Spec.ID != 2 {
		t.Errorf("snapshot after removal = %+v, want only sensor 2", got)
	}
}

// Changing a sensor's options must reopen it; keeping the old handle would report the old device
// under the new configuration.
func TestHub_ChangedOptionsReopen(t *testing.T) {
	p := newFake()
	h := NewHub(testLog(), p)
	h.Set([]Spec{spec(1)})
	h.pass(t.Context())

	changed := spec(1)
	changed.Options = map[string]string{"n": "2"}
	h.Set([]Spec{changed})
	h.pass(t.Context())

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opens != 2 || p.closes != 1 {
		t.Errorf("opens = %d, closes = %d after an options change, want 2 and 1", p.opens, p.closes)
	}
}

func TestHub_OnUpdateFiresWithTheSnapshot(t *testing.T) {
	p := newFake()
	h := NewHub(testLog(), p)

	var mu sync.Mutex
	var last []Status
	h.OnUpdate(func(s []Status) {
		mu.Lock()
		defer mu.Unlock()
		last = s
	})

	h.Set([]Spec{spec(1)})
	h.pass(t.Context())

	mu.Lock()
	defer mu.Unlock()
	if len(last) != 1 || len(last[0].Readings) != 1 {
		t.Fatalf("listener got %+v, want one sensor with one reading", last)
	}
}

// Snapshot must stay answerable while a sensor is mid-read, or the whole UI stalls behind the
// slowest device on the bus. The read has to be provably in flight first: without that the
// snapshot can win the race and the test passes whatever the locking does.
func TestHub_SnapshotDoesNotWaitOnASlowRead(t *testing.T) {
	p := &blockingProvider{entered: make(chan struct{}), block: make(chan struct{})}
	h := NewHub(testLog(), p)
	h.Set([]Spec{{ID: 1, Provider: "blocking", Kind: "slow", Name: "slow"}})

	done := make(chan struct{})
	go func() {
		h.pass(context.Background())
		close(done)
	}()
	<-p.entered

	got := make(chan int, 1)
	go func() { got <- len(h.Snapshot()) }()
	select {
	case n := <-got:
		if n != 1 {
			t.Errorf("snapshot has %d entries, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot blocked behind an in-flight sensor read")
	}

	close(p.block)
	<-done
}

// blockingProvider hands out a sensor whose Read parks until the test releases it, and announces
// on entered that it has parked.
type blockingProvider struct {
	entered chan struct{}
	block   chan struct{}
}

func (p *blockingProvider) ID() string                               { return "blocking" }
func (p *blockingProvider) Label() string                            { return "Blocking" }
func (p *blockingProvider) Available(context.Context) (bool, string) { return true, "" }
func (p *blockingProvider) Discover(context.Context) ([]Candidate, error) {
	return nil, nil
}
func (p *blockingProvider) Open(Spec) (Sensor, error) { return blockingSensor{p}, nil }

type blockingSensor struct{ p *blockingProvider }

func (s blockingSensor) Read(ctx context.Context) ([]Reading, error) {
	close(s.p.entered)
	select {
	case <-s.p.block:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSpec_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spec    Spec
		wantErr bool
	}{
		{"complete", Spec{Provider: "host", Kind: "load-average", Name: "load"}, false},
		{"no provider", Spec{Kind: "load-average", Name: "load"}, true},
		{"no kind", Spec{Provider: "host", Name: "load"}, true},
		{"no name", Spec{Provider: "host", Kind: "load-average"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.spec.Validate(); tc.wantErr != (err != nil) {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestHostProvider_DiscoverAndRead(t *testing.T) {
	dir := t.TempDir()
	zone := filepath.Join(dir, "thermal_zone0")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(zone, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("type", "cpu-thermal\n")
	write("temp", "48312\n")

	old := thermalRoot
	thermalRoot = dir
	t.Cleanup(func() { thermalRoot = old })

	p := HostProvider{}
	found, err := p.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var zoneCand *Candidate
	for i := range found {
		if found[i].Kind == KindThermalZone {
			zoneCand = &found[i]
		}
	}
	if zoneCand == nil {
		t.Fatalf("Discover found no thermal zone in %v", found)
	}
	if zoneCand.Label != "Temperature: cpu-thermal" {
		t.Errorf("label = %q, want the zone's own type", zoneCand.Label)
	}

	s, err := p.Open(Spec{Provider: "host", Kind: KindThermalZone, Name: "cpu", Options: zoneCand.Options})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	readings, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(readings) != 1 || readings[0].Value != 48.312 {
		t.Fatalf("readings = %+v, want one at 48.312 C (millidegrees scaled)", readings)
	}
}

// The load average is the one host sensor with several readings, so it is what proves a sensor is
// not limited to a single value.
func TestHostProvider_LoadAverageReportsAllThreeWindows(t *testing.T) {
	s, err := HostProvider{}.Open(Spec{Provider: "host", Kind: KindLoadAverage, Name: "load"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	readings, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(readings) != 3 {
		t.Fatalf("readings = %+v, want three windows", readings)
	}
	for _, r := range readings {
		if r.Label == "" {
			t.Errorf("reading %+v has no label, so the three windows are indistinguishable", r)
		}
	}
}

func TestHostProvider_OpenRejectsUnknownKind(t *testing.T) {
	if _, err := (HostProvider{}).Open(Spec{Provider: "host", Kind: "nonsense"}); err == nil {
		t.Fatal("Open accepted an unknown kind")
	}
}
