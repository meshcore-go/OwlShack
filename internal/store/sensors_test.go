package store

import (
	"reflect"
	"testing"
)

// Options are the only part of a sensor the store does not understand, so they are what a round
// trip has to prove: a provider gets back exactly what the operator saved.
func TestSensorRepo_RoundTrip(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := t.Context()

	rows := []Sensor{
		{Provider: "host", Kind: "load-average", Name: "Load", Options: map[string]string{}},
		{Provider: "host", Kind: "thermal-zone", Name: "CPU", Options: map[string]string{
			"path": "/sys/class/thermal/thermal_zone0/temp",
		}},
	}
	st.WriteSync(func() {
		for i := range rows {
			if err := st.Sensors.Create(ctx, &rows[i]); err != nil {
				t.Fatalf("creating sensor %d: %v", i, err)
			}
		}
	})

	got, err := st.Sensors.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("List() = %+v, want %+v", got, rows)
	}

	st.WriteSync(func() {
		if err := st.Sensors.Delete(ctx, rows[0].ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
	got, err = st.Sensors.List(ctx)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(got) != 1 || got[0].ID != rows[1].ID {
		t.Fatalf("after delete List() = %+v, want only the second sensor", got)
	}
}

// A sensor with no options must read back as an empty map, not nil: the provider indexes it.
func TestSensorRepo_EmptyOptionsReadBackEmpty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := t.Context()

	s := Sensor{Provider: "host", Kind: "load-average", Name: "Load"}
	st.WriteSync(func() {
		if err := st.Sensors.Create(ctx, &s); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})
	got, err := st.Sensors.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Options == nil {
		t.Fatal("Options came back nil; a provider reading one would panic on a nil map write")
	}
	if len(got[0].Options) != 0 {
		t.Fatalf("Options = %v, want empty", got[0].Options)
	}
}
