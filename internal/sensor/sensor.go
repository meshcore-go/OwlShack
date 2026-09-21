// Package sensor is the framework that connects sensor providers to sensor consumers.
//
// A Provider knows how to find and open sensors of one origin: the host system, an I2C bus, the
// modem. A Sensor produces Readings. Consumers never see either - they read from a Hub, which owns
// the configured set, polls it, and holds the last result for each one.
package sensor

import (
	"context"
	"fmt"
	"time"
)

// Provider is one origin of sensors. Implementations are passed to NewHub explicitly; there is no
// global registry, so a test builds a Hub with exactly the providers it means to exercise.
type Provider interface {
	// ID is the stable key stored against every sensor this provider opened.
	ID() string
	// Label names the provider in the picker.
	Label() string
	// Available reports whether this provider can run here, and why not when it cannot. Finding
	// nothing and being unable to look are different answers, and the operator needs to tell them
	// apart: an empty list from an unavailable provider would read as "no sensors here".
	Available(ctx context.Context) (bool, string)
	// Discover lists what could be added right now. It must not configure or claim anything.
	Discover(ctx context.Context) ([]Candidate, error)
	// Open turns a stored spec into a live sensor.
	Open(spec Spec) (Sensor, error)
}

// Sensor is one opened source of readings.
type Sensor interface {
	Read(ctx context.Context) ([]Reading, error)
}

// Closer is implemented by sensors holding a file handle or bus lock.
type Closer interface {
	Close() error
}

// Spec is a configured sensor: what the operator picked, as stored.
type Spec struct {
	ID       int64
	Provider string
	Kind     string
	Name     string
	// Options are provider-specific and opaque to everything else, which is what keeps the add form
	// generic: Discover hands back the options a candidate needs and the UI passes them straight on.
	Options map[string]string
}

// Candidate is something a provider found. Nothing is configured from it until the operator says so.
type Candidate struct {
	Kind    string
	Label   string
	Detail  string
	Options map[string]string
}

// Metric names what a reading measures. The set is open: a provider may report anything, and the UI
// renders the name as given.
type Metric string

const (
	Temperature Metric = "temperature"
	Humidity    Metric = "humidity"
	Pressure    Metric = "pressure"
	Voltage     Metric = "voltage"
	Load        Metric = "load"
	Percentage  Metric = "percentage"
)

// Reading is one measurement. Label separates readings a sensor reports more than one of, such as
// the three load averages, and is empty when the metric alone identifies it.
type Reading struct {
	Metric Metric
	Label  string
	Value  float64
	Unit   string
}

// Status is what a consumer sees for one configured sensor.
//
// The three states are deliberately distinguishable: At zero with no Err means configured and not
// yet polled, At set with no Err means the last read succeeded, and Err set means the last attempt
// failed - with Readings and At still describing the last read that worked, so a consumer can say
// how stale they are rather than being handed zeroes.
type Status struct {
	Spec     Spec
	Readings []Reading
	At       time.Time
	Err      string
}

// ProviderInfo is one provider's entry in the picker.
type ProviderInfo struct {
	ID        string
	Label     string
	Available bool
	// Reason is why the provider is unavailable, empty when it is available.
	Reason string
}

// Validate rejects a spec that names nothing openable. Both fields are required rather than
// defaulted: a sensor with no provider would be stored, listed, and never read.
func (s Spec) Validate() error {
	if s.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if s.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}
