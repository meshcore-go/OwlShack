package modem

import "testing"

func TestLookupBoard(t *testing.T) {
	if _, err := LookupBoard("ultrapeaterzero-e22p"); err != nil {
		t.Fatalf("LookupBoard: %v", err)
	}
	if _, err := LookupBoard("no-such-hat"); err == nil {
		t.Error("LookupBoard(unknown) = nil error, want one naming the known boards")
	}
}

// A board missing a reset or busy pin fails at bring-up as a dead radio, and
// one whose RF switch is unset transmits into a terminated switch: it looks
// like a healthy node that nobody can hear. Checked here because a half-added
// board otherwise compiles and only fails on someone's roof.
func TestBoardRegistryIsComplete(t *testing.T) {
	for _, b := range Boards() {
		t.Run(b.Name, func(t *testing.T) {
			if b.Name == "" || b.Label == "" || b.Chip == "" || b.SPIPort == "" {
				t.Errorf("board has an empty identity field: %+v", b)
			}
			if b.MaxTxPower == 0 {
				t.Error("MaxTxPower is 0, which rejects every transmit power")
			}
			o := b.Opts()
			if o.ResetPin == "" {
				t.Error("ResetPin unset: the radio can never be brought up")
			}
			if o.BusyPin == "" {
				t.Error("BusyPin unset: every SPI command races the chip")
			}
			if o.Speed == 0 {
				t.Error("Speed unset: the SPI clock would default to the bus minimum")
			}
			if b.Verified != "hardware" && b.Verified != "community" {
				t.Errorf("verified = %q, want a declared provenance", b.Verified)
			}
			// One of the two RF-switch mechanisms must be configured, or
			// transmissions never reach the antenna. A board that has neither
			// is listed but must be refused, not offered.
			if !o.UseDIO2AsRfSwitch && o.TxEnPin == "" && b.Unsupported == "" {
				t.Error("no RF switch control and not marked unsupported: transmissions would go into a terminated switch")
			}
		})
	}
}

// The one hat this project has run must stay usable; refusing it would fail
// every SPI node with no obvious cause.
func TestVerifiedBoardIsUsable(t *testing.T) {
	b, err := LookupBoard("ultrapeaterzero-e22p")
	if err != nil {
		t.Fatalf("LookupBoard: %v", err)
	}
	if b.Unsupported != "" {
		t.Fatalf("the hardware-verified board is refused: %s", b.Unsupported)
	}
	if b.Verified != "hardware" {
		t.Errorf("verified = %q, want hardware", b.Verified)
	}
	// The wiring confirmed on the hat, so a boards.json edit that moves a pin
	// fails here rather than on someone's roof.
	o := b.Opts()
	for _, tc := range []struct{ field, got, want string }{
		{"reset", o.ResetPin, "GPIO25"},
		{"busy", o.BusyPin, "GPIO5"},
		{"dio1", o.Dio1Pin, "GPIO12"},
		{"cs", o.CSPin, "GPIO24"},
		{"txen", o.TxEnPin, "GPIO27"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if len(o.EnablePins) != 2 || o.EnablePins[0] != "GPIO17" || o.EnablePins[1] != "GPIO16" {
		t.Errorf("EnablePins = %v, want [GPIO17 GPIO16]: a missed enable line is a radio that never answers", o.EnablePins)
	}
	if o.TCXOVoltage == 0 {
		t.Error("TCXO unset: DIO3 powers the oscillator on this board, and without it the radio hears nothing")
	}
	if leds := b.LEDs(); leds.Tx != "GPIO21" || leds.Rx != "GPIO20" {
		t.Errorf("LEDs = %+v, want tx GPIO21 / rx GPIO20", leds)
	}
}

// Reading an omitted pin as GPIO0 would drive an unrelated line every transaction.
func TestParseBoards_PinAbsenceIsNotGPIO0(t *testing.T) {
	const doc = `{"boards":{"t":{
		"reset_pin":25,"busy_pin":5,"tx_power":22,"use_dio2_rf":true,
		"cs_pin":-1,"txen_pin":0,"verified":"community"}}}`
	got, err := parseBoards([]byte(doc))
	if err != nil {
		t.Fatalf("parseBoards: %v", err)
	}
	o := got["t"].Opts()
	if o.CSPin != "" {
		t.Errorf("cs_pin -1 became %q, want no pin at all", o.CSPin)
	}
	if o.TxEnPin != "GPIO0" {
		t.Errorf("txen_pin 0 became %q, want GPIO0: 0 is a real pin", o.TxEnPin)
	}
	if o.Dio1Pin != "" {
		t.Errorf("an omitted irq_pin became %q, want no pin", o.Dio1Pin)
	}
}

func TestParseBoards_Rejections(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"provenance is required", `{"boards":{"t":{"reset_pin":1,"busy_pin":2,"tx_power":22}}}`},
		{"provenance must be known", `{"boards":{"t":{"reset_pin":1,"busy_pin":2,"tx_power":22,"verified":"probably"}}}`},
		{"reset is required", `{"boards":{"t":{"busy_pin":2,"tx_power":22,"verified":"community"}}}`},
		{"busy is required", `{"boards":{"t":{"reset_pin":1,"tx_power":22,"verified":"community"}}}`},
		{"tx power is required", `{"boards":{"t":{"reset_pin":1,"busy_pin":2,"verified":"community"}}}`},
		{"tx power is bounded", `{"boards":{"t":{"reset_pin":1,"busy_pin":2,"tx_power":40,"verified":"community"}}}`},
		{"tcxo voltage must be one the chip can make", `{"boards":{"t":{"reset_pin":1,"busy_pin":2,"tx_power":22,"verified":"community","use_dio3_tcxo":true,"dio3_tcxo_voltage":5}}}`},
		{"an empty file is not a board list", `{"boards":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseBoards([]byte(tc.doc)); err == nil {
				t.Error("parseBoards accepted it; a board that reaches the radio on wrong or missing pins has no symptom to debug")
			}
		})
	}
}

// A pasted entry whose en_pin was dropped is a module that never powers up.
func TestParseBoards_AcceptsSingularEnPin(t *testing.T) {
	const doc = `{"boards":{"t":{"reset_pin":25,"busy_pin":5,"tx_power":22,
		"use_dio2_rf":true,"en_pin":26,"verified":"community"}}}`
	got, err := parseBoards([]byte(doc))
	if err != nil {
		t.Fatalf("parseBoards: %v", err)
	}
	if o := got["t"].Opts(); len(o.EnablePins) != 1 || o.EnablePins[0] != "GPIO26" {
		t.Errorf("EnablePins = %v, want [GPIO26]", o.EnablePins)
	}
}
