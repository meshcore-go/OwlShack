package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/sensor"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// sensorPollInterval is how often every sensor is read; one added from the UI reads at once, so this sets how fast a value goes stale, not how long an add feels.
const sensorPollInterval = 30 * time.Second

// startSensors runs the hub for the life of the process, outside the radio lifecycle.
func startSensors(ctx context.Context, db *store.Store, hub *api.Hub, log *slog.Logger) (*sensor.Hub, error) {
	i2c := sensor.I2CProvider{Period: sensorPollInterval, State: sensorState{db: db}}
	sh := sensor.NewHub(log, i2c, sensor.PiSugarProvider{}, &sensor.VirtualProvider{})
	if err := loadSensors(ctx, db, sh); err != nil {
		return nil, err
	}
	sh.OnUpdate(func(st []sensor.Status) {
		hub.Broadcast("sensors", sensorStatusDTOs(st))
	})
	go sh.Poll(ctx, sensorPollInterval)
	return sh, nil
}

// sensorState is the store behind a sensor's learned calibration; a save waits for the writer, so a reopen reads what its close saved.
type sensorState struct{ db *store.Store }

func (s sensorState) LoadState(sensorID int64) ([]byte, error) {
	return s.db.Sensors.LoadState(context.Background(), sensorID)
}

func (s sensorState) SaveState(sensorID int64, state []byte) error {
	var err error
	s.db.WriteSync(func() { err = s.db.Sensors.SaveState(context.Background(), sensorID, state) })
	return err
}

func loadSensors(ctx context.Context, db *store.Store, sh *sensor.Hub) error {
	rows, err := db.Sensors.List(ctx)
	if err != nil {
		return fmt.Errorf("reading sensors: %w", err)
	}
	specs := make([]sensor.Spec, 0, len(rows))
	for _, r := range rows {
		specs = append(specs, sensor.Spec{
			ID: r.ID, Provider: r.Provider, Kind: r.Kind, Name: r.Name, Options: r.Options,
			Bindings: specBindings(r.Bindings),
		})
	}
	sh.Set(specs)
	return nil
}

func (b *backend) Sensors() []api.SensorStatus {
	if b.sensors == nil {
		return []api.SensorStatus{}
	}
	return sensorStatusDTOs(b.sensors.Snapshot())
}

func (b *backend) SensorProviders(ctx context.Context) []api.SensorProviderInfo {
	if b.sensors == nil {
		return []api.SensorProviderInfo{}
	}
	infos := b.sensors.Providers(ctx)
	out := make([]api.SensorProviderInfo, 0, len(infos))
	for _, p := range infos {
		out = append(out, api.SensorProviderInfo{
			ID: p.ID, Label: p.Label, Available: p.Available, Reason: p.Reason,
		})
	}
	return out
}

func (b *backend) DiscoverSensors(ctx context.Context, provider string) (api.SensorScan, error) {
	if b.sensors == nil {
		return api.SensorScan{}, fmt.Errorf("sensors are not ready yet")
	}
	res, err := b.sensors.Discover(ctx, provider)
	if err != nil {
		return api.SensorScan{}, err
	}
	out := api.SensorScan{
		Candidates: make([]api.SensorCandidate, 0, len(res.Candidates)),
		Problems:   make([]api.SensorProviderProblem, 0, len(res.Problems)),
	}
	for _, c := range res.Candidates {
		out.Candidates = append(out.Candidates, api.SensorCandidate{
			Kind: c.Kind, Provider: c.Provider, Label: c.Label, Detail: c.Detail,
			Addable: c.Addable, Options: c.Options,
		})
	}
	for _, p := range res.Problems {
		out.Problems = append(out.Problems, api.SensorProviderProblem{
			Provider: p.Provider, Label: p.Label, Reason: p.Reason,
		})
	}
	return out, nil
}

func (b *backend) SensorKinds(provider string) ([]api.SensorKindInfo, error) {
	if b.sensors == nil {
		return nil, fmt.Errorf("sensors are not ready yet")
	}
	kinds, err := b.sensors.Kinds(provider)
	if err != nil {
		return nil, err
	}
	out := make([]api.SensorKindInfo, 0, len(kinds))
	for _, k := range kinds {
		fields := make([]api.SensorField, 0, len(k.Fields))
		for _, f := range k.Fields {
			fields = append(fields, api.SensorField{
				Key: f.Key, Label: f.Label, Help: f.Help,
				Default: f.Default, Choices: f.Choices, Required: f.Required,
				Multiline: f.Multiline, Identifies: f.Identifies,
			})
		}
		metrics := make([]string, 0, len(k.Metrics))
		for _, m := range k.Metrics {
			metrics = append(metrics, string(m))
		}
		out = append(out, api.SensorKindInfo{
			Kind: k.Kind, Provider: k.Provider, Label: k.Label,
			Description: k.Description, Category: k.Category,
			Metrics: metrics, Binds: k.Binds, Fields: fields,
		})
	}
	return out, nil
}

// sensorWrites holds each sensor change from its checks to its reload; ponytail: one process-wide lock, per-sensor if edits queue behind a slow reload.
var sensorWrites sync.Mutex

func (b *backend) CreateSensor(ctx context.Context, in api.SensorInput) (int64, error) {
	sensorWrites.Lock()
	defer sensorWrites.Unlock()
	spec, err := b.prepareSensor(0, in)
	if err != nil {
		return 0, err
	}
	row := store.Sensor{
		Provider: spec.Provider, Kind: spec.Kind, Name: spec.Name, Options: spec.Options,
		Bindings: rowBindings(spec.Bindings),
	}
	b.db.WriteSync(func() { err = b.db.Sensors.Create(ctx, &row) })
	if err != nil {
		return 0, err
	}
	// The row is written, so a client leaving now must not turn the reload into a failure.
	return row.ID, loadSensors(context.WithoutCancel(ctx), b.db, b.sensors)
}

func (b *backend) UpdateSensor(ctx context.Context, id int64, in api.SensorInput) error {
	sensorWrites.Lock()
	defer sensorWrites.Unlock()
	spec, err := b.prepareSensor(id, in)
	if err != nil {
		return err
	}
	if err := b.checkDependents(spec); err != nil {
		return err
	}
	row := store.Sensor{
		ID: id, Provider: spec.Provider, Kind: spec.Kind, Name: spec.Name, Options: spec.Options,
		Bindings: rowBindings(spec.Bindings),
	}
	b.db.WriteSync(func() { err = b.db.Sensors.Update(ctx, &row) })
	if err != nil {
		return err
	}
	return loadSensors(context.WithoutCancel(ctx), b.db, b.sensors)
}

// checkDependents refuses an edit that would leave a derived sensor reading something this one stops reporting.
func (b *backend) checkDependents(spec sensor.Spec) error {
	reports := b.sensors.Reports(spec)
	for _, s := range b.sensors.Snapshot() {
		for _, bd := range s.Spec.Bindings {
			if bd.SensorID == spec.ID && !reports[bd.Metric] {
				return fmt.Errorf("%s reads %s from this sensor, which it would no longer report", s.Spec.Name, bd.Metric)
			}
		}
	}
	return nil
}

// prepareSensor defaults then validates, and the id lets the hub refuse an edit that makes two sensors read each other.
func (b *backend) prepareSensor(id int64, in api.SensorInput) (sensor.Spec, error) {
	if b.sensors == nil {
		return sensor.Spec{}, fmt.Errorf("sensors are not ready yet")
	}
	return b.sensors.Prepare(sensor.Spec{
		ID: id, Provider: in.Provider, Kind: in.Kind, Name: in.Name, Options: in.Options,
		Bindings: apiToSpecBindings(in.Bindings),
	})
}

func specBindings(bs []store.SensorBinding) []sensor.Binding {
	out := make([]sensor.Binding, 0, len(bs))
	for _, b := range bs {
		out = append(out, sensor.Binding{Name: b.Name, SensorID: b.SensorID, Metric: sensor.Metric(b.Metric)})
	}
	return out
}

func rowBindings(bs []sensor.Binding) []store.SensorBinding {
	out := make([]store.SensorBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, store.SensorBinding{Name: b.Name, SensorID: b.SensorID, Metric: string(b.Metric)})
	}
	return out
}

func apiToSpecBindings(bs []api.SensorBinding) []sensor.Binding {
	out := make([]sensor.Binding, 0, len(bs))
	for _, b := range bs {
		out = append(out, sensor.Binding{Name: b.Name, SensorID: b.SensorID, Metric: sensor.Metric(b.Metric)})
	}
	return out
}

func apiBindings(bs []sensor.Binding) []api.SensorBinding {
	out := make([]api.SensorBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, api.SensorBinding{Name: b.Name, SensorID: b.SensorID, Metric: string(b.Metric)})
	}
	return out
}

func (b *backend) DeleteSensor(ctx context.Context, id int64) error {
	if b.sensors == nil {
		return fmt.Errorf("sensors are not ready yet")
	}
	sensorWrites.Lock()
	defer sensorWrites.Unlock()
	// Refused rather than left dangling: a derived sensor reading this one would fail every poll from then on.
	for _, s := range b.sensors.Snapshot() {
		for _, bd := range s.Spec.Bindings {
			if bd.SensorID == id {
				return fmt.Errorf("%s reads this sensor; remove it, or its binding, first", s.Spec.Name)
			}
		}
	}
	var err error
	b.db.WriteSync(func() { err = b.db.Sensors.Delete(ctx, id) })
	if err != nil {
		return err
	}
	return loadSensors(context.WithoutCancel(ctx), b.db, b.sensors)
}

func sensorStatusDTOs(in []sensor.Status) []api.SensorStatus {
	out := make([]api.SensorStatus, 0, len(in))
	for _, s := range in {
		row := api.SensorStatus{
			ID:       s.Spec.ID,
			Provider: s.Spec.Provider,
			Kind:     s.Spec.Kind,
			Name:     s.Spec.Name,
			Options:  s.Spec.Options,
			Bindings: apiBindings(s.Spec.Bindings),
			Readings: make([]api.SensorReading, 0, len(s.Readings)),
			Error:    s.Err,
		}
		if row.Options == nil {
			row.Options = map[string]string{}
		}
		for _, r := range s.Readings {
			row.Readings = append(row.Readings, api.SensorReading{
				Metric: string(r.Metric), Label: r.Label, Value: r.Value, Unit: r.Unit,
				Format: sensor.FormatOf(r.Metric),
			})
		}
		// Null until the sensor is first read, which tells the page that apart from a read that reported nothing.
		if !s.At.IsZero() {
			at := s.At.UTC().Format(time.RFC3339)
			row.At = &at
		}
		out = append(out, row)
	}
	return out
}
