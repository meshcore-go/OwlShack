package shtc3

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2ctest"
	"periph.io/x/conn/v3/physic"
)

// A wrong polynomial still yields a plausible byte, so only the published vector proves this.
func TestCRC8_DatasheetVector(t *testing.T) {
	if got := crc8([]byte{0xBE, 0xEF}); got != 0x92 {
		t.Fatalf("crc8(0xBE, 0xEF) = %#02x, want 0x92", got)
	}
}

// openOps is the wire traffic a successful open expects: wake, reset, wake, read ID, sleep.
func openOps(id uint16) []i2ctest.IO {
	idHi, idLo := byte(id>>8), byte(id)
	return []i2ctest.IO{
		{Addr: DefaultAddress, W: []byte{0x35, 0x17}},
		{Addr: DefaultAddress, W: []byte{0x80, 0x5D}},
		{Addr: DefaultAddress, W: []byte{0x35, 0x17}},
		{Addr: DefaultAddress, W: []byte{0xEF, 0xC8}},
		{Addr: DefaultAddress, R: []byte{idHi, idLo, crc8([]byte{idHi, idLo})}},
		{Addr: DefaultAddress, W: []byte{0xB0, 0x98}},
	}
}

func TestNewI2C_OpensAndReadsIdentity(t *testing.T) {
	const id = 0x0887 // real ID seen on an SHTC3: the fixed bits plus vendor bits
	bus := &i2ctest.Playback{Ops: openOps(id), DontPanic: true}
	d, err := NewI2C(bus, nil)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}
	if d.ID() != id {
		t.Errorf("ID() = %#04x, want %#04x", d.ID(), id)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("playback not fully consumed: %v", err)
	}
}

// 0x70 is also the PCA9548A multiplexer's default, so a wrong part here is a real possibility.
func TestNewI2C_RejectsWrongDevice(t *testing.T) {
	bus := &i2ctest.Playback{Ops: openOps(0x1234), DontPanic: true}
	if _, err := NewI2C(bus, nil); err == nil {
		t.Fatal("NewI2C accepted a device whose ID register is not an SHTC3's")
	}
}

func TestNewI2C_RejectsCorruptIdentity(t *testing.T) {
	ops := openOps(0x0807)
	ops[4].R[2] ^= 0xFF // break the CRC over the ID word
	bus := &i2ctest.Playback{Ops: ops, DontPanic: true}
	if _, err := NewI2C(bus, nil); err == nil {
		t.Fatal("NewI2C accepted an ID word whose CRC does not match")
	}
}

// word encodes a measurement half the way the part sends it: big-endian, CRC last.
func word(raw uint16) []byte {
	hi, lo := byte(raw>>8), byte(raw)
	return []byte{hi, lo, crc8([]byte{hi, lo})}
}

func measureOps(tRaw, hRaw uint16) []i2ctest.IO {
	r := append(word(tRaw), word(hRaw)...)
	return []i2ctest.IO{
		{Addr: DefaultAddress, W: []byte{0x35, 0x17}}, // wake
		{Addr: DefaultAddress, W: []byte{0x78, 0x66}}, // normal mode, T first, no stretching
		{Addr: DefaultAddress, R: r},
		{Addr: DefaultAddress, W: []byte{0xB0, 0x98}}, // sleep
	}
}

// Tight enough to tell the datasheet's 2^16 divisor from a 65535 one, a 0.0011C gap.
const tolerance = 0.0005

// Both raw words are asymmetric across their bytes; a palindrome could not see an endianness swap.
func TestSense_ConvertsRawWords(t *testing.T) {
	ops := append(openOps(0x0807), measureOps(0x6400, 0x8000)...)
	bus := &i2ctest.Playback{Ops: ops, DontPanic: true}
	d, err := NewI2C(bus, nil)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}

	var e physic.Env
	if err := d.Sense(&e); err != nil {
		t.Fatalf("Sense: %v", err)
	}
	if c := e.Temperature.Celsius(); math.Abs(c-23.359375) > tolerance {
		t.Errorf("temperature = %.6f C, want 23.359375", c)
	}
	rh := float64(e.Humidity) / float64(physic.PercentRH)
	if math.Abs(rh-50) > tolerance {
		t.Errorf("humidity = %.6f %%RH, want 50.000000", rh)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("playback not fully consumed: %v", err)
	}
}

// The range ends catch a sign or offset slip that a mid-scale value would hide.
func TestSense_ConvertsRangeEnds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tRaw   uint16
		hRaw   uint16
		wantC  float64
		wantRH float64
	}{
		{"bottom of scale", 0x0000, 0x0000, -45, 0},
		{"top of scale", 0xFFFF, 0xFFFF, 129.997330, 99.998474},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := append(openOps(0x0807), measureOps(tc.tRaw, tc.hRaw)...)
			bus := &i2ctest.Playback{Ops: ops, DontPanic: true}
			d, err := NewI2C(bus, nil)
			if err != nil {
				t.Fatalf("NewI2C: %v", err)
			}
			var e physic.Env
			if err := d.Sense(&e); err != nil {
				t.Fatalf("Sense: %v", err)
			}
			if c := e.Temperature.Celsius(); math.Abs(c-tc.wantC) > tolerance {
				t.Errorf("temperature = %.6f C, want %.6f", c, tc.wantC)
			}
			rh := float64(e.Humidity) / float64(physic.PercentRH)
			if math.Abs(rh-tc.wantRH) > tolerance {
				t.Errorf("humidity = %.6f %%RH, want %.6f", rh, tc.wantRH)
			}
		})
	}
}

// A flipped bit is a plausible temperature, so nothing downstream could catch it.
func TestSense_RejectsCorruptMeasurement(t *testing.T) {
	ops := append(openOps(0x0807), measureOps(0x6400, 0x8000)...)
	ops[len(ops)-2].R[2] ^= 0xFF // break the temperature CRC
	bus := &i2ctest.Playback{Ops: ops, DontPanic: true}
	d, err := NewI2C(bus, nil)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}
	var e physic.Env
	if err := d.Sense(&e); err == nil {
		t.Fatal("Sense accepted a measurement whose CRC does not match")
	}
}

// Low power has to reach the wire as its own command, not just a shorter sleep.
func TestSense_LowPowerUsesItsOwnCommand(t *testing.T) {
	ops := append(openOps(0x0807), measureOps(0x6400, 0x8000)...)
	ops[len(ops)-3].W = []byte{0x60, 0x9C}
	bus := &i2ctest.Playback{Ops: ops, DontPanic: true}
	opts := DefaultOpts
	opts.LowPower = true
	d, err := NewI2C(bus, &opts)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}
	var e physic.Env
	if err := d.Sense(&e); err != nil {
		t.Fatalf("Sense: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("playback not fully consumed: %v", err)
	}
}

func TestPrecision(t *testing.T) {
	var e physic.Env
	(&SHTC3{}).Precision(&e)
	// 175 K over 16 bits, and 100 %RH over 16 bits.
	if want := physic.Temperature(math.Round(175 * float64(physic.Kelvin) / 65536)); e.Temperature != want {
		t.Errorf("temperature precision = %v, want %v", e.Temperature, want)
	}
	if e.Pressure != 0 {
		t.Errorf("pressure precision = %v, want 0: the SHTC3 has no barometer", e.Pressure)
	}
}

// recordBus fails the write at failWrite, so a fault can be aimed at an exact step.
type recordBus struct {
	writes    [][]byte
	failWrite int // 1-based index of the write to fail; 0 fails nothing
	n         int
}

func (b *recordBus) String() string                  { return "recordBus" }
func (b *recordBus) Close() error                    { return nil }
func (b *recordBus) SetSpeed(physic.Frequency) error { return nil }

func (b *recordBus) Tx(addr uint16, w, r []byte) error {
	if len(w) > 0 {
		b.n++
		b.writes = append(b.writes, append([]byte(nil), w...))
		if b.n == b.failWrite {
			return errors.New("bus fault")
		}
	}
	return nil
}

// A part left awake inverts what a later error means: the sleep that should NACK would succeed.
func TestSense_LeavesThePartAsleepAfterAFault(t *testing.T) {
	// Failing the measure is the path that could return without putting the part back to sleep.
	bus := &recordBus{}
	d := &SHTC3{dev: i2c.Dev{Bus: bus, Addr: DefaultAddress}, opts: DefaultOpts}

	bus.failWrite = 2 // the measurement command
	var e physic.Env
	if err := d.Sense(&e); err == nil {
		t.Fatal("Sense returned no error although the measurement command failed")
	}
	if len(bus.writes) == 0 {
		t.Fatal("no traffic at all")
	}
	last := bus.writes[len(bus.writes)-1]
	if want := []byte{0xB0, 0x98}; !bytes.Equal(last, want) {
		t.Errorf("last write was %#v, want the sleep command %#v: the part was left awake", last, want)
	}
}

// countingBus counts transactions, which Playback cannot: it reports ops left over, not ones sent past the end.
type countingBus struct {
	i2c.Bus
	n int
}

func (c *countingBus) Tx(addr uint16, w, r []byte) error {
	c.n++
	return c.Bus.Tx(addr, w, r)
}

// A sleeping SHTC3 NACKs everything but wake, so a command from Halt would invent a failure.
func TestHalt_SendsNothingToASleepingPart(t *testing.T) {
	ops := append(openOps(0x0807), measureOps(0x6400, 0x8000)...)
	bus := &countingBus{Bus: &i2ctest.Playback{Ops: ops, DontPanic: true}}
	d, err := NewI2C(bus, nil)
	if err != nil {
		t.Fatalf("NewI2C: %v", err)
	}
	var e physic.Env
	if err := d.Sense(&e); err != nil {
		t.Fatalf("Sense: %v", err)
	}
	before := bus.n
	if err := d.Halt(); err != nil {
		t.Fatalf("Halt: %v", err)
	}
	if sent := bus.n - before; sent != 0 {
		t.Errorf("Halt sent %d transactions to a sleeping part", sent)
	}
}
