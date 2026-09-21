package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/sensor"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// sensorPollInterval is how often every configured sensor is read. A sensor added from the UI is
// read straight away, so this only sets how quickly a value goes stale, not how long an add feels.
const sensorPollInterval = 30 * time.Second

// startSensors brings up the sensor hub and runs it for the life of the process. It is deliberately
// outside the radio lifecycle: a sensor on the I2C bus or the host has nothing to do with the modem,
// and a reconnect must not disturb it.
func startSensors(ctx context.Context, db *store.Store, hub *api.Hub, log *slog.Logger) (*sensor.Hub, error) {
	sh := sensor.NewHub(log, sensor.HostProvider{})
	if err := loadSensors(ctx, db, sh); err != nil {
		return nil, err
	}
	sh.OnUpdate(func(st []sensor.Status) {
		hub.Broadcast("sensors", sensorStatusDTOs(st))
	})
	go sh.Poll(ctx, sensorPollInterval)
	return sh, nil
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

func (b *backend) DiscoverSensors(ctx context.Context, provider string) ([]api.SensorCandidate, error) {
	if b.sensors == nil {
		return nil, fmt.Errorf("sensors are not ready yet")
	}
	found, err := b.sensors.Discover(ctx, provider)
	if err != nil {
		return nil, err
	}
	out := make([]api.SensorCandidate, 0, len(found))
	for _, c := range found {
		out = append(out, api.SensorCandidate{
			Kind: c.Kind, Label: c.Label, Detail: c.Detail, Options: c.Options,
		})
	}
	return out, nil
}

func (b *backend) CreateSensor(ctx context.Context, in api.SensorInput) (int64, error) {
	if b.sensors == nil {
		return 0, fmt.Errorf("sensors are not ready yet")
	}
	spec := sensor.Spec{
		Provider: in.Provider, Kind: in.Kind, Name: in.Name, Options: in.Options,
	}
	if err := spec.Validate(); err != nil {
		return 0, err
	}
	row := store.Sensor{
		Provider: spec.Provider, Kind: spec.Kind, Name: spec.Name, Options: spec.Options,
	}
	var err error
	b.db.WriteSync(func() { err = b.db.Sensors.Create(ctx, &row) })
	if err != nil {
		return 0, err
	}
	return row.ID, loadSensors(ctx, b.db, b.sensors)
}

func (b *backend) DeleteSensor(ctx context.Context, id int64) error {
	if b.sensors == nil {
		return fmt.Errorf("sensors are not ready yet")
	}
	var err error
	b.db.WriteSync(func() { err = b.db.Sensors.Delete(ctx, id) })
	if err != nil {
		return err
	}
	return loadSensors(ctx, b.db, b.sensors)
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
			Readings: make([]api.SensorReading, 0, len(s.Readings)),
			Error:    s.Err,
		}
		if row.Options == nil {
			row.Options = map[string]string{}
		}
		for _, r := range s.Readings {
			row.Readings = append(row.Readings, api.SensorReading{
				Metric: string(r.Metric), Label: r.Label, Value: r.Value, Unit: r.Unit,
			})
		}
		// Left null when the sensor has never been read, which is what tells the page apart from one
		// that read and reported nothing.
		if !s.At.IsZero() {
			at := s.At.UTC().Format(time.RFC3339)
			row.At = &at
		}
		out = append(out, row)
	}
	return out
}
