package monitor

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func newReschedTestService() *Service {
	return &Service{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		nextDue:  make(map[string]time.Time),
		failures: make(map[string]int),
	}
}

// dueIn reports the scheduled delay until the next poll for key.
func (s *Service) dueIn(key string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Until(s.nextDue[key])
}

// approx checks d is within 2s of want (rescheduleAfter stamps off time.Now).
func approx(d, want time.Duration) bool { return d > want-2*time.Second && d < want+2*time.Second }

func TestRescheduleBoundedRetry(t *testing.T) {
	s := newReschedTestService()
	const key = "8d558c745fcc497b889769304b92cb096f4dfc4952dafa1cd401fd7c46530112"
	tgt := Target{IntervalSecs: 3600, RetrySecs: 60, MaxRetries: 2}

	// Two failures re-attempt at the retry delay.
	s.rescheduleAfter(key, tgt, false)
	if got := s.dueIn(key); !approx(got, 60*time.Second) {
		t.Fatalf("fail #1: due in %v, want ~60s (retry)", got)
	}
	s.rescheduleAfter(key, tgt, false)
	if got := s.dueIn(key); !approx(got, 60*time.Second) {
		t.Fatalf("fail #2: due in %v, want ~60s (retry)", got)
	}

	// Third failure exceeds MaxRetries → fall back to the normal interval and reset.
	s.rescheduleAfter(key, tgt, false)
	if got := s.dueIn(key); !approx(got, 3600*time.Second) {
		t.Fatalf("fail #3: due in %v, want ~3600s (normal interval)", got)
	}
	if s.failures[key] != 0 {
		t.Fatalf("failure counter not reset after giving up: %d", s.failures[key])
	}

	// A success always clears the counter and uses the normal interval.
	s.rescheduleAfter(key, tgt, false)
	s.rescheduleAfter(key, tgt, true)
	if got := s.dueIn(key); !approx(got, 3600*time.Second) {
		t.Fatalf("success: due in %v, want ~3600s", got)
	}
	if s.failures[key] != 0 {
		t.Fatalf("failure counter not cleared on success: %d", s.failures[key])
	}
}

func TestRescheduleDefaults(t *testing.T) {
	s := newReschedTestService()
	const key = "b8b950572f212cba178ab74a5b6f217460e5d34835361ee2b342c1a86628e492"
	tgt := Target{} // all zero → defaults

	s.rescheduleAfter(key, tgt, false)
	if got := s.dueIn(key); !approx(got, retryInterval) {
		t.Fatalf("default retry: due in %v, want ~%v", got, retryInterval)
	}
	// Exhaust defaultMaxRetries, next failure falls back to defaultInterval.
	for i := 1; i < defaultMaxRetries; i++ {
		s.rescheduleAfter(key, tgt, false)
	}
	s.rescheduleAfter(key, tgt, false)
	if got := s.dueIn(key); !approx(got, defaultInterval) {
		t.Fatalf("default give-up: due in %v, want ~%v", got, defaultInterval)
	}
}
