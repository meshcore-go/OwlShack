package sensor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Host kinds.
const (
	KindThermalZone = "thermal-zone"
	KindLoadAverage = "load-average"
)

// thermalRoot is where Linux exposes thermal zones; a variable so a test can point it elsewhere.
var thermalRoot = "/sys/class/thermal"

// loadAvgPath is the kernel's load average file.
var loadAvgPath = "/proc/loadavg"

// HostProvider reads the machine OwlShack runs on. It needs no hardware and no permissions beyond
// reading sysfs, so it is the one provider that works on a laptop and on a Pi alike.
type HostProvider struct{}

func (HostProvider) ID() string    { return "host" }
func (HostProvider) Label() string { return "Host system" }

func (HostProvider) Available(context.Context) (bool, string) {
	if _, err := os.Stat(loadAvgPath); err != nil {
		return false, fmt.Sprintf("this is not a Linux host: %s is missing", loadAvgPath)
	}
	return true, ""
}

// Discover lists one candidate per thermal zone, named by the zone's own type, plus the load
// average, which every Linux host has.
func (p HostProvider) Discover(context.Context) ([]Candidate, error) {
	out := []Candidate{{
		Kind:    KindLoadAverage,
		Label:   "Load average",
		Detail:  loadAvgPath,
		Options: map[string]string{},
	}}

	zones, err := filepath.Glob(filepath.Join(thermalRoot, "thermal_zone*"))
	if err != nil {
		return out, fmt.Errorf("listing thermal zones: %w", err)
	}
	for _, dir := range zones {
		kind := readTrimmed(filepath.Join(dir, "type"))
		if kind == "" {
			kind = filepath.Base(dir)
		}
		out = append(out, Candidate{
			Kind:    KindThermalZone,
			Label:   "Temperature: " + kind,
			Detail:  dir,
			Options: map[string]string{"path": filepath.Join(dir, "temp")},
		})
	}
	return out, nil
}

func (p HostProvider) Open(spec Spec) (Sensor, error) {
	switch spec.Kind {
	case KindLoadAverage:
		return loadAverage{}, nil
	case KindThermalZone:
		path := spec.Options["path"]
		if path == "" {
			return nil, fmt.Errorf("thermal zone sensor needs a path option")
		}
		return thermalZone{path: path}, nil
	}
	return nil, fmt.Errorf("host provider has no sensor kind %q", spec.Kind)
}

// thermalZone reads one sysfs zone. The file is opened per read rather than held: a zone can
// disappear when hardware is removed, and the next read should say so rather than keep a dead handle.
type thermalZone struct{ path string }

func (t thermalZone) Read(context.Context) ([]Reading, error) {
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", t.path, err)
	}
	milli, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", t.path, err)
	}
	return []Reading{{
		Metric: Temperature, Value: float64(milli) / 1000, Unit: "C",
	}}, nil
}

// loadAverage reports all three windows, which is what makes it useful: one number cannot say
// whether load is climbing or settling.
type loadAverage struct{}

func (loadAverage) Read(context.Context) ([]Reading, error) {
	raw, err := os.ReadFile(loadAvgPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", loadAvgPath, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return nil, fmt.Errorf("%s has %d fields, want at least 3", loadAvgPath, len(fields))
	}
	labels := [3]string{"1 min", "5 min", "15 min"}
	out := make([]Reading, 0, 3)
	for i, label := range labels {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil, fmt.Errorf("parsing %s field %d: %w", loadAvgPath, i+1, err)
		}
		out = append(out, Reading{Metric: Load, Label: label, Value: v})
	}
	return out, nil
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
