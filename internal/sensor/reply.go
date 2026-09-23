package sensor

import (
	"bytes"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// selfTypes are what channel 1 carries, read as the node's battery and board temperature; GPS belongs there too but comes from configuration.
var selfTypes = []byte{meshcore.LPPVoltage, meshcore.LPPTemperature}

// SelfTypes are the LPP types a channel map may put on the node's own channel.
func SelfTypes() []byte { return append([]byte(nil), selfTypes...) }

// IsSelfType reports whether the node's own channel may carry this type.
func IsSelfType(t byte) bool { return bytes.IndexByte(selfTypes, t) >= 0 }

// SelfReadings is what a node knows of itself without a sensor: a nil temperature or position is unmeasurable, and 0 V is no cell, as the firmware reports.
type SelfReadings struct {
	BatteryVolts float64
	TempC        *float64
	Lat, Lon     *float64
}

// BuildReply builds a reply body; a mapped channel-1 row replaces the built-in reading, and a map too big for the packet is dropped whole, as truncated LPP decodes as garbage.
func BuildReply(perms byte, self SelfReadings, entries []ChannelEntry, statuses []Status, maxBody int) (body []byte, dropped bool) {
	mapped := map[byte]bool{}
	for _, e := range entries {
		if e.Channel == ChannelSelf {
			mapped[e.Type] = true
		}
	}

	enc := meshcore.NewLPPEncoder()
	if perms&PermBase != 0 {
		// Always a voltage, as the firmware always sends one: a mains-powered node says 0, not nothing.
		if !mapped[meshcore.LPPVoltage] {
			enc.AddVoltage(ChannelSelf, self.BatteryVolts)
		}
		if !mapped[meshcore.LPPTemperature] && self.TempC != nil {
			enc.AddTemperature(ChannelSelf, *self.TempC)
		}
		// A failing mapped sensor publishes nothing, since falling back would report the board's battery as the one the operator pointed at.
		encodeEntries(enc, entries, statuses, func(e ChannelEntry) bool { return e.Channel == ChannelSelf })
	}
	if perms&PermLocation != 0 && self.Lat != nil && self.Lon != nil {
		enc.AddGPS(ChannelSelf, *self.Lat, *self.Lon, 0)
	}
	selfOnly := bytes.Clone(enc.Bytes())

	if perms&PermEnvironment != 0 {
		encodeEntries(enc, entries, statuses, func(e ChannelEntry) bool { return e.Channel != ChannelSelf })
	}
	if len(enc.Bytes()) > maxBody {
		return selfOnly, true
	}
	return enc.Bytes(), false
}
