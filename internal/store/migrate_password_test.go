package store

import (
	"testing"
)

// A blank admin password compared equal to the blank a login sends, so any node in range was
// granted admin. Slot 11 backfills it to the firmware's own default rather than inventing one.
func TestMigrateV9_BlankAdminPasswordBackfilled(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)

	rep := &Repeater{Name: "blank", AdminPassword: "", GuestPassword: ""}
	if err := st.Repeater.Set(t.Context(), rep); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := migrateV9(t.Context(), st.db); err != nil {
		t.Fatalf("migrateV9: %v", err)
	}

	got, err := st.Repeater.Get(t.Context())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AdminPassword != "password" {
		t.Errorf("admin password = %q, want %q", got.AdminPassword, "password")
	}
	// Guest stays blank on purpose: blank guest grants PERM_ACL_GUEST (0), as the firmware does.
	if got.GuestPassword != "" {
		t.Errorf("guest password = %q, want it left alone", got.GuestPassword)
	}
}

// An operator who already set a password must keep it, or the migration would hand every
// repeater the same known string.
func TestMigrateV9_LeavesAnExistingPassword(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)

	if err := st.Repeater.Set(t.Context(), &Repeater{Name: "set", AdminPassword: "s3cret"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := migrateV9(t.Context(), st.db); err != nil {
		t.Fatalf("migrateV9: %v", err)
	}
	got, err := st.Repeater.Get(t.Context())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AdminPassword != "s3cret" {
		t.Errorf("admin password = %q, want it untouched", got.AdminPassword)
	}
}

// Running twice must not undo an operator's later change back to the default.
func TestMigrateV9_Idempotent(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)

	if err := st.Repeater.Set(t.Context(), &Repeater{Name: "twice", AdminPassword: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := range 2 {
		if err := migrateV9(t.Context(), st.db); err != nil {
			t.Fatalf("migrateV9 run %d: %v", i+1, err)
		}
	}
	got, err := st.Repeater.Get(t.Context())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AdminPassword != "password" {
		t.Errorf("admin password = %q, want %q", got.AdminPassword, "password")
	}
}

// A repeater row that does not exist yet must not make the migration fail: the table is empty on
// every install that has never configured one.
func TestMigrateV9_NoRepeaterConfigured(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if err := migrateV9(t.Context(), st.db); err != nil {
		t.Errorf("migrateV9 on an empty table: %v", err)
	}
}
