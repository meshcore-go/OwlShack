package repeater

import "testing"

// Every value must be strictly greater than the last, even within one wall-clock second.
func TestUniqueTimestamp(t *testing.T) {
	rm := &Client{}
	prev := rm.UniqueTimestamp()
	for i := 0; i < 1000; i++ {
		ts := rm.UniqueTimestamp()
		if ts <= prev {
			t.Fatalf("timestamp %d not > previous %d at iteration %d", ts, prev, i)
		}
		prev = ts
	}
}
