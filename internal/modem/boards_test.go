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
			// One of the two RF-switch mechanisms must be configured, or
			// transmissions never reach the antenna.
			if !o.UseDIO2AsRfSwitch && o.TxEnPin == "" {
				t.Error("no RF switch control: neither DIO2 nor a TxEn pin is set")
			}
		})
	}
}
