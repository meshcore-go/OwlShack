package app

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/sensor"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// A close saves on the way out, so the save has to be written when it returns, not queued behind other writes or dropped by a full queue.
func TestSensorState_ASaveIsWrittenBeforeItReturns(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row := store.Sensor{Provider: "i2c", Kind: "bme680", Name: "Air", Options: map[string]string{}}
	db.WriteSync(func() { err = db.Sensors.Create(t.Context(), &row) })
	if err != nil {
		t.Fatal(err)
	}

	st := sensorState{db: db}
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	db.WriteAsync(func() { <-hold })

	saved := make(chan error, 1)
	go func() { saved <- st.SaveState(row.ID, []byte("at close")) }()
	select {
	case err := <-saved:
		t.Fatalf("the save returned (%v) while the writer had not run it", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-saved; err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if got, err := st.LoadState(row.ID); err != nil || string(got) != "at close" {
		t.Errorf("the reopen loaded %q, %v; want the state its close saved", got, err)
	}
}

// A delete that would leave a derived sensor reading nothing is refused, not left dangling.
func TestSensorEdits_RefuseToStrandWhatReadsThem(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "edits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{}, &sensor.VirtualProvider{})
	b := &backend{db: db, sensors: hub}

	ups, err := b.CreateSensor(ctx, api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: "ups", Options: map[string]string{"address": "127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	derived := api.SensorInput{
		Provider: "virtual", Kind: sensor.KindExpression, Name: "double",
		Options:  map[string]string{"expression": "v * 2", "metric": "moisture", "unit": "%"},
		Bindings: []api.SensorBinding{{Name: "v", SensorID: ups, Metric: "voltage"}},
	}
	id, err := b.CreateSensor(ctx, derived)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteSensor(ctx, ups); err == nil || !strings.Contains(err.Error(), "double") {
		t.Errorf("deleting a sensor that double reads gave %v, want it refused", err)
	}

	// Provokes it: with nothing reading them, the deletes go through.
	for _, del := range []int64{id, ups} {
		if err := b.DeleteSensor(ctx, del); err != nil {
			t.Errorf("deleting sensor %d with nothing reading it: %v", del, err)
		}
	}
}

// Uniqueness is checked against the hub, so creates racing between the check and the reload would each pass it and make several of one name.
func TestSensorEdits_RacingCreatesOfOneNameMakeOne(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{})
	b := &backend{db: db, sensors: hub}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 40 {
		wg.Go(func() {
			<-start
			_, _ = b.CreateSensor(ctx, api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: "ups",
				Options: map[string]string{"address": fmt.Sprintf("127.0.0.1:%d", 9000+i)}})
		})
	}
	close(start)
	wg.Wait()
	rows, err := db.Sensors.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d sensors called ups, want 1", len(rows))
	}
}
