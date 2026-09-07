package telemetry

import (
	"encoding/binary"
	"fmt"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// SeriesEntry is one sensor channel's min/max/avg over the requested window
// (firmware MinMaxAvg).
type SeriesEntry struct {
	Channel int     `json:"channel"`
	Type    int     `json:"type"`
	Name    string  `json:"name"`
	Unit    string  `json:"unit,omitempty"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Avg     float64 `json:"avg"`
}

// Series is a decoded REQ_TYPE_GET_AVG_MIN_MAX reply.
type Series struct {
	NodeTime uint32        `json:"nodeTime"` // the sensor's clock when it answered
	Entries  []SeriesEntry `json:"entries"`
	Raw      string        `json:"raw"`
}

// ParseSeries decodes the body of a sensor's GET_AVG_MIN_MAX reply (after the
// reflected tag): [now:u32 LE] then per entry [channel:u8][lpp_type:u8] and
// min, max, avg each packed by the firmware's putFloat — MSB-first integer of
// lppDataSize(type) bytes, scaled by lppMultiplier(type), two's-complement when
// lppSigned(type). Composite types (GPS, accelerometer…) are never aggregated
// by the firmware; their size is honoured so the stream stays aligned but the
// value is reported as its raw integer.
func ParseSeries(data []byte) (*Series, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("series data too short: got %d bytes", len(data))
	}
	s := &Series{
		NodeTime: binary.LittleEndian.Uint32(data[:4]),
		Entries:  []SeriesEntry{},
		Raw:      fmt.Sprintf("%x", data),
	}
	pos := 4
	for pos+2 <= len(data) {
		// The reply is AES-ECB padded to a 16-byte block, so a run of trailing
		// zeros is padding rather than a channel — firmware channels are 1-based.
		if allZero(data[pos:]) {
			break
		}
		ch, typ := data[pos], data[pos+1]
		pos += 2
		sz := lppDataSize(typ)
		if pos+3*sz > len(data) {
			return nil, fmt.Errorf("series entry ch%d type %d truncated at byte %d", ch, typ, pos)
		}
		mult, signed := lppMultiplier(typ), lppSigned(typ)
		name, unit := lppTypeMeta(false, typ)
		s.Entries = append(s.Entries, SeriesEntry{
			Channel: int(ch),
			Type:    int(typ),
			Name:    name,
			Unit:    unit,
			Min:     lppGetFloat(data[pos:pos+sz], mult, signed),
			Max:     lppGetFloat(data[pos+sz:pos+2*sz], mult, signed),
			Avg:     lppGetFloat(data[pos+2*sz:pos+3*sz], mult, signed),
		})
		pos += 3 * sz
	}
	return s, nil
}

// lppGetFloat mirrors the firmware getFloat: MSB-first, optional two's
// complement over `size*8` bits, divided by the multiplier.
func lppGetFloat(b []byte, mult uint32, signed bool) float64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	sign := 1.0
	// Over 8 bytes the sign bit is unrepresentable (9-byte GPS shifted by 71).
	if signed && len(b) <= 8 {
		bit := uint64(1) << (uint(len(b))*8 - 1)
		if v&bit == bit {
			v = (bit << 1) - v
			sign = -1
		}
	}
	return sign * float64(v) / float64(mult)
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// The three tables below are the firmware's getDataSize / getMultiplier /
// isSigned (examples/simple_sensor/SensorMesh.cpp).
func lppDataSize(typ byte) int {
	switch typ {
	case meshcore.LPPGPS:
		return 9
	case meshcore.LPPPolyline:
		return 8
	case meshcore.LPPGyrometer, meshcore.LPPAccelerometer:
		return 6
	case meshcore.LPPGenericSensor, meshcore.LPPFrequency, meshcore.LPPDistance, meshcore.LPPEnergy, meshcore.LPPUnixTime:
		return 4
	case meshcore.LPPColour:
		return 3
	case meshcore.LPPAnalogInput, meshcore.LPPAnalogOutput, meshcore.LPPLuminosity, meshcore.LPPTemperature,
		meshcore.LPPConcentration, meshcore.LPPBarometricPressure, meshcore.LPPRelativeHumidity, meshcore.LPPAltitude,
		meshcore.LPPVoltage, meshcore.LPPCurrent, meshcore.LPPDirection, meshcore.LPPPower:
		return 2
	}
	return 1
}

func lppMultiplier(typ byte) uint32 {
	switch typ {
	case meshcore.LPPCurrent, meshcore.LPPDistance, meshcore.LPPEnergy:
		return 1000
	case meshcore.LPPVoltage, meshcore.LPPAnalogInput, meshcore.LPPAnalogOutput:
		return 100
	case meshcore.LPPTemperature, meshcore.LPPBarometricPressure, meshcore.LPPRelativeHumidity:
		return 10
	}
	return 1
}

func lppSigned(typ byte) bool {
	switch typ {
	case meshcore.LPPAltitude, meshcore.LPPTemperature, meshcore.LPPGyrometer, meshcore.LPPAnalogInput,
		meshcore.LPPAnalogOutput, meshcore.LPPGPS, meshcore.LPPAccelerometer:
		return true
	}
	return false
}
