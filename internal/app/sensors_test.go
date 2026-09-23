package app

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/config"
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

// An edit or delete that would leave a channel or a derived sensor reading nothing is refused, not logged or left dangling.
func TestSensorEdits_RefuseToStrandWhatReadsThem(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "edits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{}, &sensor.VirtualProvider{})
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}
	c := store.Companion{Name: "home"}
	db.WriteSync(func() { err = db.Companions.Create(ctx, &c) })
	if err != nil {
		t.Fatal(err)
	}

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
	node := api.TelemetryNode{Kind: store.NodeKindCompanion, ID: c.ID}
	row := []api.TelemetryMapEntry{{Node: node, Channel: 2, Type: int(meshcore.LPPAnalogInput), SensorID: id, Metric: "moisture"}}
	if err := b.SetTelemetryMap(ctx, node, row); err != nil {
		t.Fatalf("a map reading the expression's own metric was refused: %v", err)
	}

	derived.Options["metric"] = "humidity"
	if err := b.UpdateSensor(ctx, id, derived); err == nil || !strings.Contains(err.Error(), "channel 2") {
		t.Errorf("an edit that strands channel 2 gave %v, want it refused", err)
	}
	if err := b.DeleteSensor(ctx, ups); err == nil || !strings.Contains(err.Error(), "double") {
		t.Errorf("deleting a sensor that double reads gave %v, want it refused", err)
	}

	// Provokes both: with nothing reading them, the same edit and deletes go through.
	if err := b.SetTelemetryMap(ctx, node, []api.TelemetryMapEntry{}); err != nil {
		t.Fatal(err)
	}
	if err := b.UpdateSensor(ctx, id, derived); err != nil {
		t.Errorf("the edit was refused with nothing reading it: %v", err)
	}
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
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}

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

// A database that cannot be read is an error, not a host with no nodes, which the editor would show as nothing to publish from.
func TestTelemetryMap_ADatabaseErrorIsNotAnEmptyHost(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatal(err)
	}
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{})
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}
	if _, err := b.TelemetryMap(ctx); err != nil {
		t.Fatalf("an empty, readable host gave %v", err)
	}
	db.Close()
	if _, err := b.TelemetryMap(ctx); err == nil {
		t.Fatal("a closed database read as a host with no nodes")
	}
}

// The API answers a refusal 422 with its reason and anything else 500 without it, so every refusal has to be marked and nothing else may be.
func TestSensorEdits_MarkARefusalApartFromAFailure(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "marks.db"))
	if err != nil {
		t.Fatal(err)
	}
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{}, &sensor.VirtualProvider{})
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}
	ups := api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: "ups", Options: map[string]string{"address": "127.0.0.1:9"}}
	id, err := b.CreateSensor(ctx, ups)
	if err != nil {
		t.Fatal(err)
	}
	derived := api.SensorInput{
		Provider: "virtual", Kind: sensor.KindExpression, Name: "double",
		Options:  map[string]string{"expression": "v * 2", "metric": "moisture", "unit": "%"},
		Bindings: []api.SensorBinding{{Name: "v", SensorID: id, Metric: "voltage"}},
	}
	if _, err := b.CreateSensor(ctx, derived); err != nil {
		t.Fatal(err)
	}
	c := store.Companion{Name: "home"}
	db.WriteSync(func() { err = db.Companions.Create(ctx, &c) })
	if err != nil {
		t.Fatal(err)
	}
	home := api.TelemetryNode{Kind: store.NodeKindCompanion, ID: c.ID}
	row := func(ch int) []api.TelemetryMapEntry {
		return []api.TelemetryMapEntry{{Node: home, Channel: ch, Type: 116, SensorID: id, Metric: "voltage"}}
	}
	elsewhere := func(name, addr string) api.SensorInput {
		return api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: name, Options: map[string]string{"address": addr}}
	}
	var verr *api.ValidationError
	for name, err := range map[string]error{
		"a duplicate name":        second(b.CreateSensor(ctx, elsewhere("ups", "127.0.0.1:10"))),
		"an unknown provider":     second(b.SensorKinds("nonesuch")),
		"a scan of no provider":   second(b.DiscoverSensors(ctx, "nonesuch")),
		"deleting what is read":   b.DeleteSensor(ctx, id),
		"a node that is not here": b.SetTelemetryMap(ctx, api.TelemetryNode{Kind: store.NodeKindRepeater, ID: store.RepeaterNodeID}, []api.TelemetryMapEntry{}),
		"channel 0":               b.SetTelemetryMap(ctx, home, row(0)),
		"channel 300":             b.SetTelemetryMap(ctx, home, row(300)),
	} {
		if !errors.As(err, &verr) {
			t.Errorf("%s gave %v, which is not marked as the request's fault", name, err)
		}
	}
	for name, err := range map[string]error{
		"editing a sensor that is gone":  b.UpdateSensor(ctx, 999, elsewhere("gone", "127.0.0.1:11")),
		"deleting a sensor that is gone": b.DeleteSensor(ctx, 999),
	} {
		if !errors.Is(err, sql.ErrNoRows) || errors.As(err, &verr) {
			t.Errorf("%s gave %v, want an unmarked missing row", name, err)
		}
	}

	// Provokes the other half: a database that cannot be written is the server's failure, never the request's.
	db.Close()
	if _, err := b.CreateSensor(ctx, elsewhere("another", "127.0.0.1:12")); err == nil || errors.As(err, &verr) {
		t.Errorf("a create on a closed database gave %v, want an unmarked failure", err)
	}
}

func second[T any](_ T, err error) error { return err }

// Map iteration order is random, so without a sort the editor's rows come back shuffled between two loads.
func TestTelemetryMap_ComesBackInNodeOrder(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{})
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}
	id, err := b.CreateSensor(ctx, api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: "ups", Options: map[string]string{"address": "127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []api.TelemetryNode
	for _, name := range []string{"one", "two", "three", "four"} {
		c := store.Companion{Name: name}
		db.WriteSync(func() { err = db.Companions.Create(ctx, &c) })
		if err != nil {
			t.Fatal(err)
		}
		node := api.TelemetryNode{Kind: store.NodeKindCompanion, ID: c.ID}
		nodes = append(nodes, node)
		rows := []api.TelemetryMapEntry{
			{Node: node, Channel: 3, Type: 116, SensorID: id, Metric: "voltage"},
			{Node: node, Channel: 2, Type: 116, SensorID: id, Metric: "voltage"},
		}
		if err := b.SetTelemetryMap(ctx, node, rows); err != nil {
			t.Fatal(err)
		}
	}
	for range 20 {
		m, err := b.TelemetryMap(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got []api.TelemetryNode
		for i, e := range m.Entries {
			if want := nodes[i/2]; e.Node != want || e.Channel != 2+i%2 {
				t.Fatalf("entry %d is %+v, want node %+v channel %d", i, e, want, 2+i%2)
			}
			got = append(got, e.Node)
		}
		if len(got) != 2*len(nodes) {
			t.Fatalf("%d entries, want %d", len(got), 2*len(nodes))
		}
	}
}

// Every row carries the node it is for, and a row filed under another node's map would publish from the wrong one.
func TestSetTelemetryMap_RefusesARowForAnotherNode(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := sensor.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), sensor.PiSugarProvider{})
	b := &backend{db: db, sensors: hub, telemetry: newTelemetryPublisher(hub)}
	id, err := b.CreateSensor(ctx, api.SensorInput{Provider: "pisugar", Kind: "pisugar", Name: "ups", Options: map[string]string{"address": "127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, name := range []string{"home", "away"} {
		c := store.Companion{Name: name}
		db.WriteSync(func() { err = db.Companions.Create(ctx, &c) })
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	home := api.TelemetryNode{Kind: store.NodeKindCompanion, ID: ids[0]}
	row := func(node api.TelemetryNode) []api.TelemetryMapEntry {
		return []api.TelemetryMapEntry{{Node: node, Channel: 2, Type: 116, SensorID: id, Metric: "voltage"}}
	}
	for _, other := range []api.TelemetryNode{{Kind: store.NodeKindCompanion, ID: ids[1]}, {}} {
		if err := b.SetTelemetryMap(ctx, home, row(other)); err == nil {
			t.Errorf("a row for %+v was saved into %+v's map", other, home)
		}
	}
	// Provokes the positive: the same row for the node being saved goes in.
	if err := b.SetTelemetryMap(ctx, home, row(home)); err != nil {
		t.Errorf("a row for the node being saved was refused: %v", err)
	}
}

// A poll wedged on a bus leaves the last reading in place, and sent on it would read as fresh to every requester.
func TestFreshOnly_DropsAReadingPastThreePolls(t *testing.T) {
	now := time.Now()
	sts := []sensor.Status{
		{Spec: sensor.Spec{ID: 1}, At: now.Add(-sensorPollInterval)},
		{Spec: sensor.Spec{ID: 2}, At: now.Add(-3*sensorPollInterval - time.Second)},
		{Spec: sensor.Spec{ID: 3}},
	}
	got := freshOnly(sts, now)
	if len(got) != 1 || got[0].Spec.ID != 1 {
		t.Fatalf("kept %+v, want only the reading one poll old", got)
	}
}

// Modes have their own endpoint, so every other companion edit, and the config a node is built from, must carry them.
func TestCompanionTelemetryModes_SurviveAnEditAndAReload(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "modes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	def := config.DefaultConfig()
	if err := persistToTables(ctx, db, &def); err != nil {
		t.Fatal(err)
	}
	b := &backend{db: db}
	id, err := b.SaveCompanion(ctx, api.CompanionInput{Name: "home"})
	if err != nil {
		t.Fatal(err)
	}
	// One mode per class, so two columns swapped in a scan show up.
	if err := b.SetCompanionTelemetry(ctx, id, api.CompanionTelemetryInput{Base: "contacts", Location: "selected", Environment: "deny"}); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		cfg, err := readConfigFromTables(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cfg.Companions {
			if c.ID != id {
				continue
			}
			got := [3]string{strDeref(c.TelemetryBase), strDeref(c.TelemetryLocation), strDeref(c.TelemetryEnvironment)}
			if got != [3]string{"contacts", "selected", "deny"} {
				t.Fatalf("%s the modes read back as %v, want contacts, selected, deny", when, got)
			}
			return
		}
		t.Fatalf("%s the companion is gone from the config", when)
	}
	// Checked before the edit too: a swapped scan swaps back when the edit writes what it read.
	check("once set")
	if _, err := b.SaveCompanion(ctx, api.CompanionInput{ID: id, Name: "home renamed"}); err != nil {
		t.Fatal(err)
	}
	check("after an edit")
}
