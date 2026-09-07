package echo

import (
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// TestPrune: entries past ttl are dropped, fresh ones kept.
func TestPrune(t *testing.T) {
	tr := NewTracker(nil, nil, nil)
	tr.ttl = 10 * time.Millisecond
	var old, fresh [meshcore.PacketHashSize]byte
	old[0], fresh[0] = 1, 2

	tr.pending[old] = &entry{registeredAt: time.Now().Add(-time.Second)}
	tr.pending[fresh] = &entry{registeredAt: time.Now()}
	tr.Prune()

	if _, ok := tr.pending[old]; ok {
		t.Error("expired entry survived Prune")
	}
	if _, ok := tr.pending[fresh]; !ok {
		t.Error("fresh entry was pruned")
	}
}
