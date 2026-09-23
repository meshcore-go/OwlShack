package sensor

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func oneSensor(metric Metric, v float64) []Status {
	return []Status{{
		Spec:     Spec{ID: 1},
		Readings: []Reading{{Metric: metric, Value: v}},
		At:       time.Now(),
	}}
}

func replyHex(t *testing.T, perms byte, self SelfReadings, entries []ChannelEntry, sts []Status) string {
	t.Helper()
	body, dropped := BuildReply(perms, self, entries, sts, MaxReplyBody(nil))
	if dropped {
		t.Fatal("the map was dropped, which this reply is not big enough to need")
	}
	return strings.ToUpper(hex.EncodeToString(body))
}

// Channel 1 is read as the battery, so a mapped PiSugar or ADC divider has to replace the radio board's cell, not join it.
func TestBuildReply_AMappedBatteryReplacesTheBoards(t *testing.T) {
	self := SelfReadings{BatteryVolts: 4.0}
	mapped := []ChannelEntry{{Channel: ChannelSelf, Type: meshcore.LPPVoltage, SensorID: 1, Metric: Voltage}}

	// 4.0V from the board alone.
	if got := replyHex(t, PermAll, self, nil, nil); got != "01740190" {
		t.Fatalf("board only: %s, want 01740190", got)
	}
	// 3.7V from the sensor, and the board's 4.0V gone rather than beside it.
	got := replyHex(t, PermAll, self, mapped, oneSensor(Voltage, 3.7))
	if got != "01740172" {
		t.Fatalf("mapped battery: %s, want 01740172 and no second voltage", got)
	}
}

// The firmware's getBattMilliVolts returns 0 with no cell, so a node sending no voltage on channel 1 reads as another kind of node.
func TestBuildReply_TheBatteryIsAlwaysSent(t *testing.T) {
	if got := replyHex(t, PermBase, SelfReadings{}, nil, nil); got != "01740000" {
		t.Fatalf("a node with no battery replied %q, want 01740000", got)
	}
}

// A mapped channel-1 row is the node talking about itself, so it answers to base; gated as environment it would hide a battery from a requester allowed base.
func TestBuildReply_TheNodesOwnChannelAnswersToBase(t *testing.T) {
	mapped := []ChannelEntry{{Channel: ChannelSelf, Type: meshcore.LPPVoltage, SensorID: 1, Metric: Voltage}}
	sts := oneSensor(Voltage, 3.7)

	if got := replyHex(t, PermBase, SelfReadings{}, mapped, sts); got != "01740172" {
		t.Fatalf("base only: %s, want the mapped battery", got)
	}
	if got := replyHex(t, PermEnvironment, SelfReadings{}, mapped, sts); got != "" {
		t.Fatalf("environment only: %s, want nothing from the node's own channel", got)
	}
}

// A failing mapped sensor publishes nothing rather than the board's reading: the operator said which battery this is, and a different one is worse than none.
func TestBuildReply_AFailedMappedSensorDoesNotFallBack(t *testing.T) {
	self := SelfReadings{BatteryVolts: 4.0}
	mapped := []ChannelEntry{{Channel: ChannelSelf, Type: meshcore.LPPVoltage, SensorID: 1, Metric: Voltage}}
	failing := []Status{{Spec: Spec{ID: 1}, Err: "i2c: no such device"}}

	if got := replyHex(t, PermAll, self, mapped, failing); got != "" {
		t.Fatalf("reply %s, want nothing rather than the board's own battery", got)
	}
}

// Position is its own class on the node's own channel, so it goes without the location permission while the battery stays.
func TestBuildReply_PositionIsGatedSeparately(t *testing.T) {
	lat, lon := -41.2865, 174.7762
	self := SelfReadings{BatteryVolts: 4.0, Lat: &lat, Lon: &lon}

	with := replyHex(t, PermAll, self, nil, nil)
	if !strings.Contains(with, "0188") {
		t.Fatalf("reply %s carries no position", with)
	}
	without := replyHex(t, PermBase, self, nil, nil)
	if strings.Contains(without, "0188") {
		t.Fatalf("reply %s carries a position the location class denies", without)
	}
	if !strings.HasPrefix(without, "01740190") {
		t.Fatalf("reply %s lost the battery along with the position", without)
	}
}

// An oversized map costs the map, and the node's own channel - including a mapped battery - survives.
func TestBuildReply_OversizeKeepsTheNodesOwnChannel(t *testing.T) {
	mapped := []ChannelEntry{{Channel: ChannelSelf, Type: meshcore.LPPVoltage, SensorID: 1, Metric: Voltage}}
	for ch := 2; ch <= 60; ch++ {
		mapped = append(mapped, ChannelEntry{Channel: byte(ch), Type: meshcore.LPPVoltage, SensorID: 1, Metric: Voltage})
	}
	body, dropped := BuildReply(PermAll, SelfReadings{}, mapped, oneSensor(Voltage, 3.7), MaxReplyBody(nil))
	if !dropped {
		t.Fatal("a map this wide has to be dropped")
	}
	if got := strings.ToUpper(hex.EncodeToString(body)); got != "01740172" {
		t.Fatalf("reply %s, want the node's own channel with its mapped battery", got)
	}
}

// request is a telemetry request as it arrived: over a flood path of that many bytes, or direct below zero.
func request(path int) *meshcore.Packet {
	if path < 0 {
		return &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeReq, 0)}
	}
	return &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeReq, 0), Path: make([]byte, path)}
}

// A map the editor accepts has to arrive whole on every common path, beside the node's own voltage, temperature and position.
func TestBuildReply_AMapAtTheBudgetFitsEveryCommonReply(t *testing.T) {
	var entries []ChannelEntry
	for ch := 2; ch <= 22; ch++ {
		entries = append(entries, ChannelEntry{Channel: byte(ch), Type: meshcore.LPPGenericSensor, SensorID: 1, Metric: Temperature})
	}
	for ch := 23; ch <= 24; ch++ {
		entries = append(entries, ChannelEntry{Channel: byte(ch), Type: meshcore.LPPTemperature, SensorID: 1, Metric: Temperature})
	}
	if n := TelemetrySize(entries); n != MaxTelemetryPayload {
		t.Fatalf("the map is %d bytes, not the %d budget this test is about", n, MaxTelemetryPayload)
	}
	if err := ValidateChannelMap(entries); err != nil {
		t.Fatalf("refused a map at the budget: %v", err)
	}

	temp, lat, lon := 21.5, -41.2865, 174.7762
	self := SelfReadings{BatteryVolts: 4.1, TempC: &temp, Lat: &lat, Lon: &lon}
	var body []byte
	for _, path := range []int{-1, 0, 1, 2, 17} {
		var dropped bool
		body, dropped = BuildReply(PermAll, self, entries, oneSensor(Temperature, 22.5), MaxReplyBody(request(path)))
		if dropped {
			t.Errorf("a %d-byte flood path (-1 is direct) dropped a map the editor accepted", path)
		}
	}
	if tightest := MaxReplyBody(request(1)); len(body) != tightest {
		t.Errorf("a full reply is %d bytes where a one-byte path holds %d, so the budget is off", len(body), tightest)
	}
}

// The temperature half of the same rule: a mapped board temperature replaces the radio board's, never joins it.
func TestBuildReply_AMappedTemperatureReplacesTheBoards(t *testing.T) {
	board := 35.2
	self := SelfReadings{BatteryVolts: 4.0, TempC: &board}
	mapped := []ChannelEntry{{Channel: ChannelSelf, Type: meshcore.LPPTemperature, SensorID: 1, Metric: Temperature}}

	// 4.0 V, then the board's 35.2 C.
	if got := replyHex(t, PermAll, self, nil, nil); got != "0174019001670160" {
		t.Fatalf("board only: %s, want 0174019001670160", got)
	}
	// The sensor's 21.5 C, and the board's 35.2 C gone rather than beside it.
	if got := replyHex(t, PermAll, self, mapped, oneSensor(Temperature, 21.5)); got != "01740190016700D7" {
		t.Fatalf("mapped temperature: %s, want 01740190016700D7 and no second temperature", got)
	}
}
