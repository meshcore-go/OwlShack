package repeater

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/store"
)

func routeTestClient(t *testing.T) (*Client, int64, []byte) {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	comp := &store.Companion{Name: "test"}
	if err := st.Companions.Create(t.Context(), comp); err != nil {
		t.Fatalf("Companions.Create: %v", err)
	}

	pubkey := bytes.Repeat([]byte{0x8d}, 32)
	if err := st.Contacts.Add(t.Context(), comp.ID, pubkey, "JKSparrOwl", "REPEATER"); err != nil {
		t.Fatalf("Contacts.Add: %v", err)
	}

	return &Client{store: st, companionID: comp.ID}, comp.ID, pubkey
}

func pubkeyArray(b []byte) [meshcore.PubKeySize]byte {
	var k [meshcore.PubKeySize]byte
	copy(k[:], b)
	return k
}

// The peer table is emptied of routes on every restart (hydratePeerTables leaves OutPath nil), so a
// route that only lives there is lost and every command floods until an inbound PATH arrives.
func TestLearnedRoute_FallsBackToTheContactRow(t *testing.T) {
	t.Parallel()

	rm, companionID, pubkey := routeTestClient(t)
	key := pubkeyArray(pubkey)

	// Nothing stored anywhere yet: flood is correct.
	if path, _ := rm.learnedRoute(key, nil); path != nil {
		t.Errorf("with no route stored, got %x, want nil (flood)", path)
	}

	if err := rm.store.Contacts.UpdateOutPath(t.Context(), companionID, pubkey, []byte{0xe6, 0x07, 0x1a}, 1); err != nil {
		t.Fatalf("UpdateOutPath: %v", err)
	}

	// A nil peer table entry must not mean "flood" while the row holds a route.
	path, hashSize := rm.learnedRoute(key, nil)
	if !bytes.Equal(path, []byte{0xe6, 0x07, 0x1a}) {
		t.Errorf("path = %x, want e6071a from the contact row", path)
	}
	if hashSize != 1 {
		t.Errorf("hashSize = %d, want 1", hashSize)
	}
	if rt, _ := routeForPeer(path, hashSize); rt != meshcore.RouteTypeDirect {
		t.Errorf("routeType = 0x%02x, want direct", rt)
	}
}

// persistOutPath writes asynchronously, so straight after a PATH the row still holds the old route:
// preferring it would send down a path we already know is superseded.
func TestLearnedRoute_LivePeerTableWinsOverAStaleRow(t *testing.T) {
	t.Parallel()

	rm, companionID, pubkey := routeTestClient(t)
	if err := rm.store.Contacts.UpdateOutPath(t.Context(), companionID, pubkey, []byte{0xaa, 0xbb}, 1); err != nil {
		t.Fatalf("UpdateOutPath: %v", err)
	}

	id, err := meshcore.NewIdentityFromBytes(pubkey)
	if err != nil {
		t.Fatalf("NewIdentityFromBytes: %v", err)
	}
	fresh := &node.Peer{Identity: id, OutPath: []byte{0xe6}, OutPathHashSize: 1}

	if path, _ := rm.learnedRoute(pubkeyArray(pubkey), fresh); !bytes.Equal(path, []byte{0xe6}) {
		t.Errorf("path = %x, want e6 from the live peer table", path)
	}
}

// A direct neighbour is an empty-but-not-nil path. Collapsing the two would flood at the one peer
// that needs no path at all.
func TestLearnedRoute_ZeroHopIsARouteNotAnAbsence(t *testing.T) {
	t.Parallel()

	rm, companionID, pubkey := routeTestClient(t)
	if err := rm.store.Contacts.UpdateOutPath(t.Context(), companionID, pubkey, []byte{}, 1); err != nil {
		t.Fatalf("UpdateOutPath: %v", err)
	}

	path, _ := rm.learnedRoute(pubkeyArray(pubkey), nil)
	if path == nil {
		t.Fatal("a 0-hop route read back as nil, which floods at a direct neighbour")
	}
	if len(path) != 0 {
		t.Errorf("path = %x, want empty", path)
	}
	if rt, pathLen := routeForPeer(path, 1); rt != meshcore.RouteTypeDirect || pathLen != 0 {
		t.Errorf("routeType/pathLen = 0x%02x/%d, want direct/0", rt, pathLen)
	}
}

// The write-through ResetPeerPath relies on: a nil path must clear the row, or learnedRoute reads
// the old route straight back and the Reset Path button does nothing. ResetPeerPath itself needs a
// live node, so this covers the store half only.
func TestPersistOutPath_NilClearsTheRow(t *testing.T) {
	t.Parallel()

	rm, companionID, pubkey := routeTestClient(t)
	if err := rm.store.Contacts.UpdateOutPath(t.Context(), companionID, pubkey, []byte{0xe6, 0x07, 0x1a}, 1); err != nil {
		t.Fatalf("UpdateOutPath: %v", err)
	}

	rm.persistOutPath(pubkey, nil, 0)
	rm.store.WriteSync(func() {}) // drain the async writer

	if path, _ := rm.learnedRoute(pubkeyArray(pubkey), nil); path != nil {
		t.Errorf("after a reset the route came back as %x, want nil (flood to rediscover)", path)
	}
}
