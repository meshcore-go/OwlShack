package modem

import (
	"fmt"
	"sort"
	"time"

	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
	"periph.io/x/conn/v3/physic"
)

// Board is the wiring of one SPI radio hat: which GPIOs carry reset, busy and
// the RF-switch lines, and how the module is powered. It is fixed by the
// board's layout, so it is chosen by name rather than entered pin by pin.
type Board struct {
	// Name is the stored identifier; Label and Chip are for the UI.
	Name  string
	Label string
	Chip  string
	// SPIPort is the periph port name the hat's NSS is wired to.
	SPIPort string
	// MaxTxPower is the module's ceiling in dBm. The E22P's PA reaches 30 dBm
	// but its LoRa core is driven at 22; going past the module's rating is how
	// a PA gets cooked.
	MaxTxPower uint8

	opts sx12xx.Opts
}

// Opts returns the driver options for the board. A copy, so a caller cannot
// mutate the registry.
func (b Board) Opts() sx12xx.Opts { return b.opts }

// boards is the registry of known hats. A board only belongs here once its
// pinout has been confirmed against hardware or the vendor's schematic: a wrong
// reset or busy pin fails as a dead radio at startup, and a wrong RF-switch pin
// transmits into a terminated switch, which looks like a working node that
// nobody can hear.
var boards = map[string]Board{
	"ultrapeaterzero-e22p": {
		Name:       "ultrapeaterzero-e22p",
		Label:      "Zindello UltraPeaterZero (E22P, 30 dBm)",
		Chip:       "SX1262",
		SPIPort:    "SPI0.0",
		MaxTxPower: 22,
		opts: sx12xx.Opts{
			Speed:         8 * physic.MegaHertz,
			ResetPin:      "GPIO25",
			BusyPin:       "GPIO5",
			Dio1Pin:       "GPIO12",
			CSPin:         "GPIO24",
			TxEnPin:       "GPIO27",
			EnablePins:    []string{"GPIO17", "GPIO16"},
			RegulatorMode: sx12xx.RegulatorDCDCLDO,
			// The RF switch is on a GPIO, not DIO2; DIO3 powers the TCXO.
			UseDIO2AsRfSwitch: false,
			TCXOVoltage:       sx12xx.TCXO1_8V,
			TCXODelay:         10 * time.Millisecond,
			BusyTimeout:       100 * time.Millisecond,
		},
	},
}

// LookupBoard returns the board with the given name.
func LookupBoard(name string) (Board, error) {
	b, ok := boards[name]
	if !ok {
		return Board{}, fmt.Errorf("unknown spi board %q (known: %v)", name, BoardNames())
	}
	return b, nil
}

// BoardNames lists the registered boards, sorted for a stable UI order.
func BoardNames() []string {
	names := make([]string, 0, len(boards))
	for n := range boards {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Boards lists the registered boards, sorted by name.
func Boards() []Board {
	out := make([]Board, 0, len(boards))
	for _, n := range BoardNames() {
		out = append(out, boards[n])
	}
	return out
}
