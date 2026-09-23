package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/meshcore-go/OwlShack/internal/store"
)

// A PATCH names what it changes: replacing the blob let the repeater list's {"isRepeater":true} wipe a saved password and a telemetry grant.
func TestContactMetadataPatch_KeepsWhatItDoesNotName(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "contacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := store.Companion{Name: "home"}
	pub := bytes.Repeat([]byte{0xAB}, 32)
	st.WriteSync(func() {
		if err := st.Companions.Create(ctx, &c); err != nil {
			t.Fatal(err)
		}
		if err := st.Contacts.Add(ctx, c.ID, pub, "rep", "REPEATER"); err != nil {
			t.Fatal(err)
		}
		err = st.Contacts.UpdateMetadata(ctx, c.ID, pub, store.ContactMetadata{RepeaterPassword: "pw", TelemPerms: 5, MonitorProbes: []string{"status"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	patch := func(key []byte, body string) int {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/companions/home/contacts/"+hex.EncodeToString(key), strings.NewReader(body)))
		return rec.Code
	}
	stored := func() store.ContactMetadata {
		got, err := st.Contacts.Get(ctx, c.ID, pub)
		if err != nil {
			t.Fatal(err)
		}
		return got.Metadata
	}

	if code := patch(pub, `{"isRepeater":true}`); code != http.StatusNoContent {
		t.Fatalf("PATCH = %d", code)
	}
	if got, want := stored(), (store.ContactMetadata{IsRepeater: true, RepeaterPassword: "pw", TelemPerms: 5, MonitorProbes: []string{"status"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored %+v, want %+v", got, want)
	}

	// Naming a field with its empty value is how a caller clears it.
	if code := patch(pub, `{"repeaterPassword":"","monitorProbes":null}`); code != http.StatusNoContent {
		t.Fatalf("PATCH = %d", code)
	}
	if got, want := stored(), (store.ContactMetadata{IsRepeater: true, TelemPerms: 5}); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored %+v, want %+v", got, want)
	}

	if code := patch(pub, `{"telemPerms":300}`); code != http.StatusBadRequest {
		t.Errorf("an out-of-range grant got %d, want 400", code)
	}
	if code := patch(bytes.Repeat([]byte{0xCD}, 32), `{"isRepeater":true}`); code != http.StatusNotFound {
		t.Errorf("a contact that does not exist got %d, want 404", code)
	}
}
