// Package telemetry decodes MeshCore CayenneLPP telemetry payloads into named
// readings and firmware-independent time-series metric keys. It is transport-
// agnostic: any node type (repeater, companion, sensor, …) that obtains a raw
// telemetry payload — via a logged-in request or a sessionless contact request —
// feeds the bytes here, so the LPP decode + naming + metric-keying live in one
// place rather than inside one client.
//
// The dependency arrow points inward: telemetry -> meshcore-go only.
package telemetry

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// ChannelSelf is the LPP channel a node uses for its own readings (battery, MCU
// temperature, GPS); external sensors land on channels >= 2 on firmware 1.16+.
// TELEM_CHANNEL_SELF in firmware.
const ChannelSelf = 1

// Reading is one decoded LPP reading with a human label. Self marks a reading
// the node reports about itself (MCU temp, battery, location) rather than an
// attached sensor — decided in Parse so downstream keying is firmware-independent.
type Reading struct {
	Channel int    `json:"channel"`
	Type    int    `json:"type"`
	Name    string `json:"name"`
	Unit    string `json:"unit,omitempty"`
	Value   any    `json:"value"`
	Self    bool   `json:"self,omitempty"`
}

// Telemetry is a decoded telemetry payload: its readings plus the raw hex.
type Telemetry struct {
	Readings []Reading `json:"readings"`
	Raw      string    `json:"raw"`
}

// Parse decodes a raw CayenneLPP telemetry payload into named readings. An empty
// payload yields an empty (non-nil) Telemetry.
func Parse(data []byte) (*Telemetry, error) {
	out := &Telemetry{
		Raw:      hex.EncodeToString(data),
		Readings: []Reading{},
	}
	if len(data) == 0 {
		return out, nil
	}
	readings, err := meshcore.LPPDecode(data)
	if err != nil {
		return out, fmt.Errorf("decoding telemetry: %w", err)
	}
	// Classify the node's own (MCU/battery/GPS) readings vs external sensors.
	// Both firmwares put self-telemetry on the self channel; the distinction is
	// what else shares it. Firmware 1.16+ gives each external sensor its own
	// channel (>= 2), so the self channel carries only self readings. Firmware
	// <= 1.15 lumps everything on the self channel, so an external temperature
	// arrives as a SECOND LPPTemperature there. We therefore treat only the
	// FIRST reading of each self-type on the self channel as "self" (the MCU
	// emits its own telemetry first); any repeat — or anything on another
	// channel — is an external sensor. This keeps metric names firmware-
	// independent instead of mislabeling 1.15's external temp as the MCU's.
	selfClaimed := map[byte]bool{}
	for _, r := range readings {
		self := r.Channel == ChannelSelf && isSelfType(r.Type) && !selfClaimed[r.Type]
		if self {
			selfClaimed[r.Type] = true
		}
		name, unit := lppTypeMeta(self, r.Type)
		out.Readings = append(out.Readings, Reading{
			Channel: int(r.Channel),
			Type:    int(r.Type),
			Name:    name,
			Unit:    unit,
			Value:   r.Value,
			Self:    self,
		})
	}
	// Group by channel; stable to keep firmware emit order within a channel.
	sort.SliceStable(out.Readings, func(i, j int) bool {
		return out.Readings[i].Channel < out.Readings[j].Channel
	})
	return out, nil
}

// Metric is one flattened scalar time-series value: its firmware-independent
// metric key, the LPP channel it came from, and the value.
type Metric struct {
	Key     string
	Channel int
	Value   float64
}

// Metrics flattens the readings into scalar time-series values, keyed so the
// firmware's sensor identity is preserved. A node's OWN readings (Self) keep
// their clean name (mcu_temperature, battery, location) since there's only one
// of each. An EXTERNAL sensor carries its channel ("temperature_ch2"), because
// on firmware 1.16+ each sensor gets its own channel — that channel is the
// stable per-sensor handle, so a multi-sensor / custom board yields distinct,
// stable series rather than fighting over one name. (Firmware <=1.15 lumps
// externals onto the self channel, so the same board changes channel keys across
// that upgrade — an accepted one-off discontinuity for legacy hardware.) A
// numeric suffix guards the rare same-channel/type duplicate. Composite values
// (GPS, accel, gyro) split into per-axis series.
func (t *Telemetry) Metrics() []Metric {
	var out []Metric
	used := make(map[string]bool, len(t.Readings))
	for _, r := range t.Readings {
		base := normalizeMetric(r.Name)
		if base == "" {
			base = fmt.Sprintf("type_%d", r.Type)
		}
		for _, kv := range flattenValue(r.Value) {
			key := base + kv.suffix
			if !r.Self {
				key = fmt.Sprintf("%s_ch%d%s", base, r.Channel, kv.suffix)
			}
			key = disambiguate(used, key)
			used[key] = true
			out = append(out, Metric{Key: key, Channel: r.Channel, Value: kv.value})
		}
	}
	return out
}

// disambiguate appends a numeric suffix if key is already taken — only needed
// for the rare case of two readings sharing both type and channel (the channel
// already separates distinct external sensors).
func disambiguate(used map[string]bool, key string) string {
	if !used[key] {
		return key
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s_%d", key, i)
		if !used[cand] {
			return cand
		}
	}
}

type scalarValue struct {
	suffix string
	value  float64
}

func flattenValue(v any) []scalarValue {
	switch x := v.(type) {
	case float64:
		return []scalarValue{{"", x}}
	case float32:
		return []scalarValue{{"", float64(x)}}
	case meshcore.LPPGPSValue:
		return []scalarValue{{"_lat", x.Latitude}, {"_lon", x.Longitude}, {"_alt", x.Altitude}}
	case meshcore.LPPAccelValue:
		return []scalarValue{{"_x", x.X}, {"_y", x.Y}, {"_z", x.Z}}
	case meshcore.LPPGyroValue:
		return []scalarValue{{"_x", x.X}, {"_y", x.Y}, {"_z", x.Z}}
	default:
		return nil
	}
}

func normalizeMetric(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '-', r == '/':
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// isSelfType reports whether an LPP type is one a node reports about itself
// (MCU temperature, battery voltage, GPS) — as opposed to an attached sensor.
func isSelfType(typ byte) bool {
	switch typ {
	case meshcore.LPPTemperature, meshcore.LPPVoltage, meshcore.LPPGPS:
		return true
	}
	return false
}

// lppTypeMeta maps an LPP type to a display name + unit. When self is true the
// reading is the node's own (MCU temp / battery / location); otherwise it's an
// external sensor and gets the generic type name. Parse decides self-ness so the
// rule is firmware-independent.
func lppTypeMeta(self bool, typ byte) (name, unit string) {
	if self {
		switch typ {
		case meshcore.LPPTemperature:
			return "MCU temperature", "°C"
		case meshcore.LPPVoltage:
			return "Battery", "V"
		case meshcore.LPPGPS:
			return "Location", ""
		}
	}
	switch typ {
	case meshcore.LPPDigitalInput:
		return "Digital input", ""
	case meshcore.LPPDigitalOutput:
		return "Digital output", ""
	case meshcore.LPPAnalogInput:
		return "Analog input", "V"
	case meshcore.LPPAnalogOutput:
		return "Analog output", "V"
	case meshcore.LPPGenericSensor:
		return "Generic sensor", ""
	case meshcore.LPPLuminosity:
		return "Luminosity", "lux"
	case meshcore.LPPPresence:
		return "Presence", ""
	case meshcore.LPPTemperature:
		return "Temperature", "°C"
	case meshcore.LPPRelativeHumidity:
		return "Humidity", "%RH"
	case meshcore.LPPAccelerometer:
		return "Accelerometer", "G"
	case meshcore.LPPBarometricPressure:
		return "Pressure", "hPa"
	case meshcore.LPPVoltage:
		return "Voltage", "V"
	case meshcore.LPPCurrent:
		return "Current", "A"
	case meshcore.LPPFrequency:
		return "Frequency", "Hz"
	case meshcore.LPPPercentage:
		return "Percentage", "%"
	case meshcore.LPPAltitude:
		return "Altitude", "m"
	case meshcore.LPPConcentration:
		return "Concentration", "ppm"
	case meshcore.LPPPower:
		return "Power", "W"
	case meshcore.LPPDistance:
		return "Distance", "m"
	case meshcore.LPPEnergy:
		return "Energy", "kWh"
	case meshcore.LPPDirection:
		return "Direction", "°"
	case meshcore.LPPUnixTime:
		return "Unix time", ""
	case meshcore.LPPGyrometer:
		return "Gyrometer", "°/s"
	case meshcore.LPPColour:
		return "Colour", "RGB"
	case meshcore.LPPGPS:
		return "GPS", ""
	case meshcore.LPPSwitch:
		return "Switch", ""
	}
	return fmt.Sprintf("Type %d", typ), ""
}
