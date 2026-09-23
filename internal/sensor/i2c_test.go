package sensor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/conn/v3/i2c/i2ctest"
	"periph.io/x/conn/v3/physic"

	"github.com/meshcore-go/OwlShack/internal/sensor/ads1x15"
	"github.com/meshcore-go/OwlShack/internal/sensor/bme680"
)

func TestParseAddress(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		fallback uint16
		want     uint16
		wantErr  bool
	}{
		{"empty falls back", "", 0x5C, 0x5C, false},
		{"hex", "0x5d", 0x5C, 0x5D, false},
		{"decimal", "92", 0x5C, 0x5C, false},
		{"not a number", "0x5g", 0x5C, 0, true},
		{"below the device range", "0x07", 0x5C, 0, true},
		{"above the device range", "0x78", 0x5C, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAddress(tc.in, tc.fallback)
			if tc.wantErr != (err != nil) {
				t.Fatalf("parseAddress(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("parseAddress(%q) = %#02x, want %#02x", tc.in, got, tc.want)
			}
		})
	}
}

// An Env field a part does not measure stays zero, which would publish as a real 0 hPa or 0 %RH.
func TestEnvSensor_ReportsOnlyTheMetricsThePartMeasures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		metrics []Metric
		want    map[Metric]float64
	}{
		{"humidity part", []Metric{Temperature, Humidity}, map[Metric]float64{Temperature: 21, Humidity: 55}},
		{"pressure part", []Metric{Pressure, Temperature}, map[Metric]float64{Pressure: 1013.25, Temperature: 21}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &envSensor{dev: fixedEnv{}, metrics: tc.metrics}
			got, err := s.Read(t.Context())
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d readings, want %d: %+v", len(got), len(tc.want), got)
			}
			for _, r := range got {
				want, ok := tc.want[r.Metric]
				if !ok {
					t.Errorf("reported %s, which this part does not measure", r.Metric)
					continue
				}
				if diff := r.Value - want; diff > 0.01 || diff < -0.01 {
					t.Errorf("%s = %v, want %v", r.Metric, r.Value, want)
				}
				if r.Unit == "" {
					t.Errorf("%s has no unit", r.Metric)
				}
			}
		})
	}
}

func TestEnvSensor_ReadErrorIsReported(t *testing.T) {
	s := &envSensor{dev: failingEnv{}, metrics: []Metric{Temperature}}
	if _, err := s.Read(t.Context()); err == nil {
		t.Fatal("Read returned no error although the device failed")
	}
}

// A chip missing its metrics or addresses builds a sensor that reports nothing or reaches a reserved address.
func TestChipTableIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range chips {
		if c.kind == "" || c.label == "" {
			t.Errorf("chip %+v has no kind or label", c)
		}
		if seen[c.kind] {
			t.Errorf("duplicate chip kind %q", c.kind)
		}
		seen[c.kind] = true
		if len(c.metrics) == 0 {
			t.Errorf("chip %q declares no metrics, so it would report nothing", c.kind)
		}
		if len(c.addrs) == 0 {
			t.Errorf("chip %q declares no addresses, so it could never be found", c.kind)
		}
		for _, a := range c.addrs {
			if a < 0x08 || a > 0x77 {
				t.Errorf("chip %q address %#02x is outside the 7-bit device range", c.kind, a)
			}
		}
		if c.open == nil {
			t.Errorf("chip %q has no open function", c.kind)
		}
	}
}

func TestI2CProvider_OpenRejectsUnknownKind(t *testing.T) {
	if _, err := (I2CProvider{}).Open(Spec{Provider: "i2c", Kind: "nonsense"}); err == nil {
		t.Fatal("Open accepted a kind no driver claims")
	}
}

type fixedEnv struct{}

func (fixedEnv) Sense(e *physic.Env) error {
	e.Temperature = physic.ZeroCelsius + 21*physic.Kelvin
	e.Humidity = 55 * physic.PercentRH
	e.Pressure = 101325 * physic.Pascal
	return nil
}
func (fixedEnv) SenseContinuous(time.Duration) (<-chan physic.Env, error) { return nil, nil }
func (fixedEnv) Precision(*physic.Env)                                    {}
func (fixedEnv) Halt() error                                              { return nil }
func (fixedEnv) String() string                                           { return "fixedEnv" }

type failingEnv struct{ fixedEnv }

func (failingEnv) Sense(*physic.Env) error { return errors.New("bus fault") }

// fakeBus answers as an SHTC3 at one address and NACKs everywhere else.
type fakeBus struct {
	shtc3Addr uint16
	// extra is an address that ACKs a read but is no part this build knows.
	extra   uint16
	lastCmd uint16
}

func (b *fakeBus) String() string                  { return "fakeBus" }
func (b *fakeBus) Close() error                    { return nil }
func (b *fakeBus) SetSpeed(physic.Frequency) error { return nil }

func (b *fakeBus) Tx(addr uint16, w, r []byte) error {
	if addr == b.extra && len(w) == 0 {
		copy(r, make([]byte, len(r)))
		return nil
	}
	if addr != b.shtc3Addr {
		return errors.New("no ack")
	}
	if len(w) == 2 {
		b.lastCmd = uint16(w[0])<<8 | uint16(w[1])
		return nil
	}
	if len(r) == 3 && b.lastCmd == 0xEFC8 {
		r[0], r[1] = 0x08, 0x07
		r[2] = sensirionCRC(r[0:2])
		return nil
	}
	return errors.New("unexpected transaction")
}

func sensirionCRC(b []byte) byte {
	crc := byte(0xFF)
	for _, v := range b {
		crc ^= v
		for range 8 {
			if crc&0x80 != 0 {
				crc = crc<<1 ^ 0x31
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// An address that merely ACKs is never claimed as a known chip.
func TestScanBus_IdentifiesAPartAndListsWhatItCannotDrive(t *testing.T) {
	bus := &fakeBus{shtc3Addr: 0x70, extra: 0x76}
	got := scanBus(t.Context(), "fake", bus)

	var addable, unknown []Candidate
	for _, c := range got {
		if c.Addable {
			addable = append(addable, c)
		} else {
			unknown = append(unknown, c)
		}
	}

	if len(addable) != 1 || addable[0].Kind != "shtc3" {
		t.Fatalf("addable = %+v, want exactly one shtc3", addable)
	}
	if addable[0].Options["bus"] != "fake" || addable[0].Options["address"] != "0x70" {
		t.Errorf("options = %v, want the bus and address the scan found it at", addable[0].Options)
	}

	// The undrivable part is still listed, so a populated bus never reads as empty.
	if len(unknown) != 1 {
		t.Fatalf("unknown = %+v, want the one address that acked but is not a known part", unknown)
	}
	if unknown[0].Kind != "" {
		t.Errorf("unknown candidate claims kind %q", unknown[0].Kind)
	}
}

// An empty bus and an unreadable one must not give the same answer.
func TestScanBus_EmptyBusYieldsNothing(t *testing.T) {
	bus := &fakeBus{shtc3Addr: 0xFFFF, extra: 0xFFFF}
	if got := scanBus(t.Context(), "fake", bus); len(got) != 0 {
		t.Fatalf("scanBus on an empty bus = %+v, want nothing", got)
	}
}

func TestScanBus_HonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := scanBus(ctx, "fake", &fakeBus{shtc3Addr: 0x70, extra: 0x76}); len(got) != 0 {
		t.Fatalf("scanBus with a cancelled context = %+v, want nothing", got)
	}
}

// The address is free text for a part behind a multiplexer, so the provider is what refuses a reserved one.
func TestI2CProvider_ValidateChecksTheAddressWithoutTouchingTheBus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"declared default", "", false},
		{"the other strap", "0x5d", false},
		{"an address no chip table lists", "0x21", false},
		{"reserved low", "0x03", true},
		{"reserved high", "0x7f", true},
		{"not a number", "twelve", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := I2CProvider{}.Validate(Spec{
				Provider: "i2c", Kind: "lps22hb", Name: "p",
				Options: map[string]string{"address": tc.addr},
			})
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate(%q) = %v, wantErr %v", tc.addr, err, tc.wantErr)
			}
		})
	}
}

// Every kind has to be addable by hand, so each needs a bus and an address field.
func TestI2CProvider_KindsDeclareTheFieldsOpenNeeds(t *testing.T) {
	kinds := I2CProvider{}.Kinds()
	if len(kinds) != len(chips) {
		t.Fatalf("Kinds() has %d entries, want one per chip (%d)", len(kinds), len(chips))
	}
	for _, k := range kinds {
		keys := map[string]Field{}
		for _, f := range k.Fields {
			keys[f.Key] = f
		}
		for _, want := range []string{"bus", "address"} {
			f, ok := keys[want]
			if !ok {
				t.Errorf("kind %q declares no %q field", k.Kind, want)
				continue
			}
			if !f.Required {
				t.Errorf("kind %q field %q is not required, so it could be left empty", k.Kind, want)
			}
		}
		if keys["address"].Default == "" {
			t.Errorf("kind %q has no default address, so the form opens blank", k.Kind)
		}
		if len(keys["address"].Choices) != 0 {
			t.Errorf("kind %q pins the address to a picker; a part behind a mux could not be reached", k.Kind)
		}
	}
}

// Defaulting with several buses picks by sort order, and a Pi lists HDMI channels beside the header bus.
func TestSoleBus_OnlyDefaultsWhenThereIsNoChoiceToMake(t *testing.T) {
	if got := soleBus([]string{"/dev/i2c-1"}); got != "/dev/i2c-1" {
		t.Errorf("one bus: got %q, want it defaulted", got)
	}
	if got := soleBus([]string{"/dev/i2c-1", "/dev/i2c-20"}); got != "" {
		t.Errorf("two buses: got %q, want no default so the operator chooses", got)
	}
	if got := soleBus(nil); got != "" {
		t.Errorf("no buses: got %q, want empty", got)
	}
}

// A catalogue row the operator cannot search is a part they cannot find at sixty entries.
func TestI2CProvider_KindsAreSearchable(t *testing.T) {
	for _, k := range (I2CProvider{}).Kinds() {
		if k.Description == "" {
			t.Errorf("kind %q has no description, so a catalogue row shows only a part number", k.Kind)
		}
		if k.Category == "" {
			t.Errorf("kind %q has no category, so it cannot be grouped", k.Kind)
		}
		if len(k.Metrics) == 0 {
			t.Errorf("kind %q lists no metrics, so it cannot be found by what it measures", k.Kind)
		}
	}
}

// A part's own extra fields have to reach the catalogue without colliding with the bus and address the provider adds.
func TestI2CProvider_KindsCarryTheirOwnFields(t *testing.T) {
	var bme KindInfo
	for _, k := range (I2CProvider{}).Kinds() {
		seen := map[string]bool{}
		for _, f := range k.Fields {
			if seen[f.Key] {
				t.Errorf("kind %q declares %q twice, so one of them is unreachable", k.Kind, f.Key)
			}
			seen[f.Key] = true
		}
		for _, want := range []string{"bus", "address"} {
			if !seen[want] {
				t.Errorf("kind %q has no %q field, so it cannot be opened", k.Kind, want)
			}
		}
		if k.Kind == "bme680" {
			bme = k
		}
	}
	if bme.Kind == "" {
		t.Fatal("no bme680 in the catalogue")
	}
	for _, want := range []string{"oversampling", "heater"} {
		if !slices.ContainsFunc(bme.Fields, func(f Field) bool { return f.Key == want }) {
			t.Errorf("bme680 does not offer %q, so its per-kind fields are not reaching the catalogue", want)
		}
	}
}

// adsBus answers as a genuine ADS1115 does: a two-bit pointer, so Chip_ID reads back the config register.
type adsBus struct{ addr uint16 }

func (b *adsBus) Tx(addr uint16, w, r []byte) error {
	if addr != b.addr {
		return errors.New("nack")
	}
	if len(w) == 0 && len(r) > 0 {
		// The bare probe a scan uses to notice something is there at all.
		r[0] = 0
		return nil
	}
	if len(r) != 2 {
		return nil
	}
	var v uint16
	switch w[0] & 0x03 { // a two-bit pointer is the whole point
	case 0x01:
		v = 0x8583
	case 0x02:
		v = 0x8000
	case 0x03:
		v = 0x7FFF
	}
	r[0], r[1] = byte(v>>8), byte(v)
	return nil
}
func (b *adsBus) SetSpeed(physic.Frequency) error { return nil }
func (b *adsBus) String() string                  { return "adsBus" }
func (b *adsBus) Close() error                    { return nil }

// 0x48 is also the LM75 and TMP102, so an unidentifiable part is listed as responding, not placed.
func TestScanBus_WillNotClaimAPartThatCannotProveWhatItIs(t *testing.T) {
	bus := &adsBus{addr: 0x48}
	got := scanBus(context.Background(), "/dev/i2c-1", bus)

	for _, c := range got {
		if c.Kind == "ads1115" {
			t.Fatalf("the scan claimed an ads1115 at %s, which it cannot tell from an LM75", c.Detail)
		}
		if c.Kind == "sgm58031" {
			t.Fatalf("the scan claimed an sgm58031, but this part has no Chip_ID register")
		}
	}
	var unknown bool
	for _, c := range got {
		if !c.Addable && strings.Contains(c.Label, "0x48") {
			unknown = true
		}
	}
	if !unknown {
		t.Error("0x48 answered but was not listed as an unknown responder, so the bus looks empty there")
	}
}

// Unscannable must not mean unusable: the part is still in the catalogue to add by hand.
func TestI2CProvider_AnonymousPartsAreStillInTheCatalogue(t *testing.T) {
	var ads KindInfo
	for _, k := range (I2CProvider{}).Kinds() {
		if k.Kind == "ads1115" {
			ads = k
		}
	}
	if ads.Kind == "" {
		t.Fatal("ads1115 is not in the catalogue, so a part a scan cannot see could never be added")
	}
	for _, want := range []string{"channel", "gain"} {
		if !slices.ContainsFunc(ads.Fields, func(f Field) bool { return f.Key == want }) {
			t.Errorf("ads1115 does not offer %q", want)
		}
	}
}

// An ADC takes the other adapter; volts and raw counts differ only by gain and both look plausible.
func TestNewSensor_RoutesAnADCToTheAnalogueAdapterAndReportsVolts(t *testing.T) {
	const cfg = 0xC383 // AIN0 vs ground, +-4.096V, single-shot, ~128Hz, comparator off
	r16 := func(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }
	bus := &i2ctest.Playback{
		Ops: []i2ctest.IO{
			{Addr: 0x48, W: []byte{0x01}, R: r16(0x8583)},
			{Addr: 0x48, W: []byte{0x05}, R: r16(0x0080)}, // an SGM58031's own chip id
			{Addr: 0x48, W: []byte{0x01, 0xC3, 0x83}},
			{Addr: 0x48, W: []byte{0x01}, R: r16(cfg &^ 0x8000)}, // still converting
			{Addr: 0x48, W: []byte{0x01}, R: r16(cfg)},
			{Addr: 0x48, W: []byte{0x01}, R: r16(cfg)},
			{Addr: 0x48, W: []byte{0x00}, R: r16(0x4000)}, // half of full scale
		},
		DontPanic: true,
	}
	dev, err := ads1x15.NewI2C(bus, nil)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}
	s, err := newSensor(dev, bus, []Metric{Voltage}, nil)
	if err != nil {
		t.Fatalf("newSensor: %v", err)
	}
	readings, err := s.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("got %d readings, want 1", len(readings))
	}
	if readings[0].Metric != Voltage || readings[0].Unit != "V" {
		t.Errorf("got %s in %q, want voltage in V", readings[0].Metric, readings[0].Unit)
	}
	// 0x4000 of 0x8000 at the 4.096V range.
	if got := readings[0].Value; got < 2.0479 || got > 2.0481 {
		t.Errorf("value = %v V, want 2.048", got)
	}
}

// recordingBus is a BME680 at 0x76 and an SHTC3 at 0x70, keeping every transaction so a test sees what a scan did.
type recordingBus struct {
	sent    [][]byte
	lastCmd uint16
}

func (b *recordingBus) Tx(addr uint16, w, r []byte) error {
	if len(w) > 0 {
		b.sent = append(b.sent, append([]byte{byte(addr)}, w...))
	}
	switch addr {
	case 0x76:
		if len(w) == 1 && w[0] == 0xD0 && len(r) == 1 {
			r[0] = 0x61 // BME68x chip id
			return nil
		}
	case 0x77:
		// Answers, but its chip id says it is not a BME68x.
		if len(w) == 1 && w[0] == 0xD0 && len(r) == 1 {
			r[0] = 0x60
			return nil
		}
		if len(w) == 0 && len(r) == 1 {
			r[0] = 0
			return nil
		}
	case 0x5D:
		// Answers, but its WHO_AM_I says it is not an LPS22HB.
		if len(w) == 1 && w[0] == 0x0F && len(r) == 1 {
			r[0] = 0xAA
			return nil
		}
		if len(w) == 0 && len(r) == 1 {
			r[0] = 0
			return nil
		}
	case 0x70:
		// The SHTC3 sends a command and reads the answer as two separate transactions.
		if len(w) == 2 && len(r) == 0 {
			b.lastCmd = uint16(w[0])<<8 | uint16(w[1])
			return nil
		}
		if len(w) == 0 && len(r) == 3 && b.lastCmd == 0xEFC8 {
			r[0], r[1], r[2] = 0x08, 0x07, 0x21 // id 0x0807 with its CRC
			return nil
		}
	}
	if len(r) > 0 {
		return errors.New("nack")
	}
	return nil
}
func (b *recordingBus) SetSpeed(physic.Frequency) error { return nil }
func (b *recordingBus) String() string                  { return "recordingBus" }
func (b *recordingBus) Close() error                    { return nil }

// writesTo returns the register writes a scan sent to one address.
func (b *recordingBus) writesTo(addr byte) [][]byte {
	var out [][]byte
	for _, t := range b.sent {
		if t[0] == addr && len(t) > 2 {
			out = append(out, t[1:])
		}
	}
	return out
}

// Opening a part to identify it resets it, which leaves a configured BME680 with its gas heater off and no error.
func TestScanBus_IdentifiesWithoutConfiguring(t *testing.T) {
	bus := &recordingBus{}
	got := scanBus(context.Background(), "/dev/i2c-1", bus)

	var kinds []string
	for _, c := range got {
		if c.Addable {
			kinds = append(kinds, c.Kind)
		}
	}
	if !slices.Contains(kinds, "bme680") || !slices.Contains(kinds, "shtc3") {
		t.Fatalf("scan found %v, want both bme680 and shtc3", kinds)
	}

	// The BME680 is identified by a plain register read, so a scan must not write to it at all.
	if w := bus.writesTo(0x76); len(w) != 0 {
		t.Errorf("the scan wrote %#v to the BME680; identifying it needs no writes", w)
	}
	// Answering is not identifying, or the catalogue names whatever sits at a known address.
	for _, c := range got {
		if c.Addable && strings.Contains(c.Detail, "0x77") {
			t.Errorf("claimed %q at 0x77, whose chip id reads 0x60", c.Kind)
		}
		if c.Addable && strings.Contains(c.Detail, "0x5d") {
			t.Errorf("claimed %q at 0x5d, whose WHO_AM_I reads 0xaa", c.Kind)
		}
	}
	for _, addr := range []string{"0x77", "0x5d"} {
		var listed bool
		for _, c := range got {
			if !c.Addable && strings.Contains(c.Label, addr) {
				listed = true
			}
		}
		if !listed {
			t.Errorf("%s answered but is not listed as an unknown responder, so the bus looks empty there", addr)
		}
	}

	// The SHTC3 has to be woken to answer, but must never be reset or told to measure.
	for _, w := range bus.writesTo(0x70) {
		cmd := uint16(w[0])<<8 | uint16(w[1])
		switch cmd {
		case 0x3517, 0xB098, 0xEFC8: // wake, sleep, read id
		default:
			t.Errorf("the scan sent the SHTC3 command %#04x, which is not identification", cmd)
		}
	}
}

// A chip a scan may claim needs something to claim it by, or adding one without a probe drops it from every scan.
func TestChips_EveryScannablePartCanIdentifyItself(t *testing.T) {
	for _, c := range chips {
		if c.anonymous {
			if c.probe != nil {
				t.Errorf("%s is marked anonymous but has a probe; one of the two is wrong", c.kind)
			}
			continue
		}
		if c.probe == nil {
			t.Errorf("%s has no probe, so a scan can never find it", c.kind)
		}
	}
}

// gasEnv is a part with a gas plate and a fusion over it, as the BME680 presents them.
type gasEnv struct {
	fixedEnv
	ohms     physic.ElectricResistance
	gasValid bool
	air      bme680.AirQuality
	airValid bool
	// restored is what the provider handed back at open; refuse makes the part reject it.
	restored []byte
	refuse   bool
	state    []byte
}

func (g *gasEnv) SenseGas() (physic.ElectricResistance, bool)            { return g.ohms, g.gasValid }
func (g *gasEnv) SenseGasCompensated() (physic.ElectricResistance, bool) { return g.ohms, g.gasValid }
func (g *gasEnv) SenseAirQuality() (bme680.AirQuality, bool)             { return g.air, g.airValid }
func (g *gasEnv) AirQualityState() ([]byte, error)                       { return g.state, nil }

func (g *gasEnv) RestoreAirQuality(state []byte) error {
	if g.refuse {
		return errors.New("state from another build")
	}
	g.restored = state
	return nil
}

func bmeChip(t *testing.T) chip {
	t.Helper()
	c, ok := chipByKind("bme680")
	if !ok {
		t.Fatal("no bme680 in the chip table")
	}
	return c
}

// The derived values ride on the part's own Status, so the page and the channel map need no special case; a part with no fusion reports none.
func TestEnvSensor_ReportsTheDerivedAirQuality(t *testing.T) {
	c := bmeChip(t)
	dev := &gasEnv{
		ohms: 50_000 * physic.Ohm, gasValid: true, airValid: true,
		air: bme680.AirQuality{
			IAQ: 87, StaticIAQ: 91, CO2Equivalent: 910, BreathVOCEquiv: 1.4,
			GasPercentage: 62, Accuracy: 3, GasAccuracy: 2, RunIn: true,
		},
	}
	s := &envSensor{dev: dev, metrics: c.metrics}
	got, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	by := map[Metric]Reading{}
	for _, r := range got {
		by[r.Metric] = r
	}
	for _, m := range c.metrics {
		if _, ok := by[m]; !ok {
			t.Errorf("%s is declared by the chip but was not reported", m)
		}
	}
	// Gas resistance is in ohms, as the firmware's BME680 publishes it (EnvironmentSensorManager.cpp).
	if got := by[Resistance]; got.Value != 50_000 || got.Unit != "Ω" {
		t.Errorf("resistance = %v %s, want 50000 Ω", got.Value, got.Unit)
	}
	// The same units as the rest of the app, which writes "°C" everywhere else.
	if got := by[Temperature].Unit; got != "°C" {
		t.Errorf("temperature is in %q, want °C", got)
	}
	for m, want := range map[Metric]float64{
		IAQ: 87, StaticIAQ: 91, CO2Equivalent: 910, BreathVOC: 1.4, GasPercentage: 62,
		IAQAccuracy: 3, GasPercentageAccuracy: 2, AirQualityRunIn: 1,
	} {
		if got := by[m].Value; got != want {
			t.Errorf("%s = %v, want %v", m, got, want)
		}
		if by[m].Label == "" {
			t.Errorf("%s has no label, so the page would show its metric name", m)
		}
	}

	dev.airValid = false
	if got, err = s.Read(t.Context()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, r := range got {
		if r.Metric != Temperature && r.Metric != Humidity && r.Metric != Pressure &&
			r.Metric != Resistance && r.Metric != GasCompensated {
			t.Errorf("reported %s from a part whose fusion is not running", r.Metric)
		}
	}
}

// At accuracy 0 the index reads as real air when it is not, so only the accuracies and run-in go out.
func TestEnvSensor_HoldsTheIndexBackUntilRunInEnds(t *testing.T) {
	dev := &gasEnv{
		ohms: 50_000 * physic.Ohm, gasValid: true, airValid: true,
		air: bme680.AirQuality{IAQ: 50, StaticIAQ: 50, CO2Equivalent: 600, BreathVOCEquiv: 0.5, Stabilised: true},
	}
	s := &envSensor{dev: dev, metrics: bmeChip(t).metrics}
	got, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reported := map[Metric]bool{}
	for _, r := range got {
		reported[r.Metric] = true
	}
	for _, m := range []Metric{IAQ, StaticIAQ, CO2Equivalent, BreathVOC, GasPercentage} {
		if reported[m] {
			t.Errorf("%s was reported at accuracy 0", m)
		}
	}
	for _, m := range []Metric{IAQAccuracy, GasPercentageAccuracy, AirQualityRunIn} {
		if !reported[m] {
			t.Errorf("%s was held back too, so nothing says the index is still calibrating", m)
		}
	}
}

// fakeState is the store seam, kept in memory; loadErr and saveErr stand in for a database that failed.
type fakeState struct {
	loaded           map[int64][]byte
	saved            map[int64][]byte
	saves            int
	loadErr, saveErr error
}

func (f *fakeState) LoadState(id int64) ([]byte, error) { return f.loaded[id], f.loadErr }

func (f *fakeState) SaveState(id int64, b []byte) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.saved == nil {
		f.saved = map[int64][]byte{}
	}
	f.saved[id] = b
	return nil
}

// Without this a restart puts the index back to "calibrating" every time, whatever it had learned.
func TestAirQualityState_RestoresAtOpenAndSavesOnItsOwnCadence(t *testing.T) {
	store := &fakeState{loaded: map[int64][]byte{7: []byte("learned")}}
	p := I2CProvider{Period: 30 * time.Second, State: store}
	dev := &gasEnv{gasValid: true, airValid: true, state: []byte("fresh")}

	air := p.restoreAirQuality(dev, 7)
	if string(dev.restored) != "learned" {
		t.Fatalf("the part was handed %q at open, want the stored state", dev.restored)
	}

	s := &envSensor{dev: dev, metrics: bmeChip(t).metrics, air: air}
	if _, err := s.Read(t.Context()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	// A part that refused the stored state would otherwise write a fresh one over it on its first read.
	if store.saves != 0 {
		t.Errorf("the first read saved %q, before a period of learning", store.saved[7])
	}
	air.saved = time.Now().Add(-stateSaveInterval)
	if _, err := s.Read(t.Context()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(store.saved[7]) != "fresh" {
		t.Errorf("a period on, the read saved %q, want what the part handed back", store.saved[7])
	}
	// The row is replaced on a slow cadence, not written on every pass.
	store.saved = nil
	if _, err := s.Read(t.Context()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if store.saved != nil {
		t.Errorf("state was saved again within %s of the last one", stateSaveInterval)
	}
	// Closing saves whatever the cadence, or a restart loses what was learned since the last save.
	dev.state = []byte("at close")
	s.bus = &fakeBus{}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if string(store.saved[7]) != "at close" {
		t.Errorf("closing saved %q, want the part's state as it closed", store.saved[7])
	}

	// A refused blob leaves the part relearning, and still reading.
	refusing := &gasEnv{gasValid: true, airValid: true, refuse: true}
	s = &envSensor{dev: refusing, metrics: bmeChip(t).metrics, air: p.restoreAirQuality(refusing, 7)}
	if refusing.restored != nil {
		t.Error("a refused state still reached the part")
	}
	if got, err := s.Read(t.Context()); err != nil || len(got) == 0 {
		t.Errorf("after refusing a stored state the part read %d values, %v", len(got), err)
	}
}

// A load that failed says nothing about the row, which may hold days of learning, so nothing this run learns replaces it.
func TestAirQualityState_AFailedLoadKeepsWhatIsStored(t *testing.T) {
	store := &fakeState{loadErr: errors.New("database is locked")}
	p := I2CProvider{Period: 30 * time.Second, State: store}
	dev := &gasEnv{gasValid: true, airValid: true, state: []byte("fresh")}

	s := &envSensor{dev: dev, metrics: bmeChip(t).metrics, air: p.restoreAirQuality(dev, 7), bus: &fakeBus{}}
	s.air.saved = time.Now().Add(-stateSaveInterval)
	if _, err := s.Read(t.Context()); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if store.saves != 0 {
		t.Errorf("%d saves after a failed load, want none", store.saves)
	}
}

// A save that failed is not done, so the next read tries again rather than waiting out another period.
func TestAirQualityState_AFailedSaveIsRetried(t *testing.T) {
	store := &fakeState{saveErr: errors.New("disk I/O error")}
	p := I2CProvider{Period: 30 * time.Second, State: store}
	dev := &gasEnv{gasValid: true, airValid: true, state: []byte("fresh")}

	s := &envSensor{dev: dev, metrics: bmeChip(t).metrics, air: p.restoreAirQuality(dev, 7)}
	s.air.saved = time.Now().Add(-stateSaveInterval)
	for range 2 {
		if _, err := s.Read(t.Context()); err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if store.saves != 2 {
		t.Errorf("%d save attempts over two reads after a failure, want 2", store.saves)
	}
}

// Nothing stores a sensor with no id, and a part with no fusion is never asked for one.
func TestAirQualityState_SkipsWhatItCannotKey(t *testing.T) {
	store := &fakeState{loaded: map[int64][]byte{7: []byte("learned")}}
	p := I2CProvider{Period: 30 * time.Second, State: store}
	dev := &gasEnv{gasValid: true, airValid: true}

	if a := p.restoreAirQuality(dev, 0); a != nil {
		t.Error("a sensor with no id was given somewhere to save")
	}
	if a := (I2CProvider{Period: 30 * time.Second}).restoreAirQuality(dev, 7); a != nil {
		t.Error("a provider with no store was given somewhere to save")
	}
	if a := p.restoreAirQuality(fixedEnv{}, 7); a != nil {
		t.Error("a part with no fusion was given somewhere to save")
	}
	if a := p.restoreAirQuality(dev, 7); a == nil {
		t.Error("a stored sensor was given nowhere to save, so this proves nothing")
	}
}

// The poll period has to reach the part, or its fusion is sized for a rate it never sees; the rest is what an operator can get wrong in the form.
func TestBMEOpts_CarriesThePollPeriodAndTheForm(t *testing.T) {
	got, err := bmeOpts(0x77, map[string]string{
		"oversampling": "4x", "heater": "on", "temperature_offset": "1.5",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("bmeOpts: %v", err)
	}
	if got.SamplePeriod != 30*time.Second {
		t.Errorf("sample period = %s, want 30s", got.SamplePeriod)
	}
	if got.Address != 0x77 {
		t.Errorf("address = %#02x, want 0x77", got.Address)
	}
	if got.Temperature != bme680.Oversampling4x || got.Humidity != bme680.Oversampling4x {
		t.Errorf("oversampling = %s/%s, want 4x on every channel", got.Temperature, got.Humidity)
	}
	if got.Heater != bme680.HeaterWarmUp {
		t.Errorf("heater = %v, want the warm-up profile", got.Heater)
	}
	// The offset is a difference, not a temperature, so it is read in kelvin rather than through Celsius.
	if k := float64(got.TemperatureOffset) / float64(physic.Kelvin); !(k > 1.49 && k < 1.51) {
		t.Errorf("temperature offset = %v K, want 1.5", k)
	}

	if off, err := bmeOpts(0x76, map[string]string{"heater": "off"}, 30*time.Second); err != nil || off.Heater != bme680.HeaterOff {
		t.Errorf("heater off gave %v, %v", off.Heater, err)
	}
	for _, bad := range []map[string]string{{"oversampling": "3x"}, {"temperature_offset": "warm"}} {
		if _, err := bmeOpts(0x76, bad, 30*time.Second); err == nil {
			t.Errorf("bmeOpts accepted %v", bad)
		}
	}
}

// Checked at save, or "abc" saves and then fails every poll, and "NaN" or 1e300 turn into a garbage offset.
func TestI2CValidate_RefusesAnOffsetThatIsNotADegreeCount(t *testing.T) {
	spec := func(off string) Spec {
		return Spec{Provider: "i2c", Kind: "bme680", Name: "air", Options: map[string]string{
			"bus": "/dev/i2c-1", "address": "0x76", "temperature_offset": off,
		}}
	}
	for _, bad := range []string{"abc", "NaN", "Inf", "1e300", "-60"} {
		if err := (I2CProvider{}).Validate(spec(bad)); err == nil {
			t.Errorf("an offset of %q was accepted", bad)
		}
	}
	if err := (I2CProvider{}).Validate(spec("2.5")); err != nil {
		t.Errorf("a 2.5 C offset was refused: %v", err)
	}
}

// A four-channel ADC is four sensors, and the rig's A0/A1 pair on one chip refused every edit while the claim ignored the input.
func TestI2CClaim_AnADCsInputsAreSeparateSensors(t *testing.T) {
	adc := func(ch string) Spec {
		return Spec{Provider: "i2c", Kind: "sgm58031", Options: map[string]string{"bus": "/dev/i2c-1", "address": "0x48", "channel": ch}}
	}
	p := I2CProvider{}
	if p.Claim(adc("A0")) == p.Claim(adc("A1")) {
		t.Error("A0 and A1 claim the same thing, so the second cannot be added")
	}
	if p.Claim(adc("A0")) != p.Claim(adc("A0")) {
		t.Error("two sensors on A0 do not collide")
	}
}

// A bus that will not open is worth showing even when another found parts; dropped, it reads as a bus with nothing on it.
func TestI2CDiscover_ReportsABusThatWouldNotOpen(t *testing.T) {
	if err := hostInit(); err != nil {
		t.Skipf("periph host init: %v", err)
	}
	for name, open := range map[string]i2creg.Opener{
		"zz-dead": func() (i2c.BusCloser, error) { return nil, errors.New("permission denied") },
		"zz-live": func() (i2c.BusCloser, error) { return &recordingBus{}, nil },
	} {
		if err := i2creg.Register(name, nil, -1, open); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = i2creg.Unregister(name) })
	}
	found, err := (I2CProvider{}).Discover(t.Context())
	if len(found) == 0 {
		t.Fatal("the live bus found nothing, so this proves nothing")
	}
	if err == nil || !strings.Contains(err.Error(), "zz-dead") {
		t.Errorf("the scan reported %v, want the bus that would not open", err)
	}
}

// The bus picker lists buses by number; as text, /dev/i2c-10 to -19 sat between -1 and -2.
func TestBusNames_ByNumber(t *testing.T) {
	if err := hostInit(); err != nil {
		t.Skipf("periph host init: %v", err)
	}
	for name, n := range map[string]int{"zz-a": 1010, "zz-b": 1002} {
		if err := i2creg.Register(name, nil, n, func() (i2c.BusCloser, error) { return &recordingBus{}, nil }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = i2creg.Unregister(name) })
	}
	names := busNames()
	if a, b := slices.Index(names, "zz-a"), slices.Index(names, "zz-b"); a < 0 || b < 0 || b > a {
		t.Errorf("bus 1002 listed at %d and bus 1010 at %d in %v, want 1002 first", b, a, names)
	}
}
