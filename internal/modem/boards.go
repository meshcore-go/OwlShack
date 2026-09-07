package modem

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
	"periph.io/x/conn/v3/physic"
)

// boardsJSON is the shipped board list; BoardsFile adds to it without a rebuild.
//
//go:embed boards.json
var boardsJSON []byte

// BoardsFile is an optional board list in the working directory, merged by key.
const BoardsFile = "boards.json"

// Board is the wiring of one SPI radio hat, fixed by its layout and so chosen
// by name rather than entered pin by pin.
type Board struct {
	// Name is the stored identifier; Label and Chip are for the UI.
	Name  string
	Label string
	Chip  string
	// SPIPort is the periph port name the hat's NSS is wired to.
	SPIPort string
	// MaxTxPower is the module's rating in dBm; past it the PA cooks.
	MaxTxPower uint8
	// Verified is "hardware" (run on the physical hat here) or "community".
	Verified string
	Notes    string
	// Unsupported is why this build refuses the board; it stays listed, because
	// known-but-refused is a different answer from missing.
	Unsupported string

	leds ledPins
	opts sx12xx.Opts
}

// Opts returns the driver options for the board. A copy, so a caller cannot
// mutate the registry.
func (b Board) Opts() sx12xx.Opts { return b.opts }

// LEDs returns the board's activity LED pins, both empty when it has none.
func (b Board) LEDs() ledPins { return b.leds }

// HasLEDs reports whether the board drives activity LEDs.
func (b Board) HasLEDs() bool { return b.leds.any() }

// boardFile is one entry in boards.json. Pins are pointers because 0 is GPIO0,
// a real pin, so an omitted line cannot be spelled with a zero value.
type boardFile struct {
	Label string `json:"label"`
	Chip  string `json:"chip"`

	BusID int `json:"bus_id"`
	CSID  int `json:"cs_id"`

	CSPin    *int  `json:"cs_pin"`
	ResetPin *int  `json:"reset_pin"`
	BusyPin  *int  `json:"busy_pin"`
	IRQPin   *int  `json:"irq_pin"`
	TxEnPin  *int  `json:"txen_pin"`
	RxEnPin  *int  `json:"rxen_pin"`
	EnPins   []int `json:"en_pins"`
	// EnPin is openhop's singular spelling; a dropped enable line reads as a dead radio.
	EnPin    *int `json:"en_pin"`
	TxLedPin *int `json:"txled_pin"`
	RxLedPin *int `json:"rxled_pin"`

	TxPower         *int     `json:"tx_power"`
	UseDIO2RF       *bool    `json:"use_dio2_rf"`
	UseDIO3TCXO     *bool    `json:"use_dio3_tcxo"`
	DIO3TCXOVoltage *float64 `json:"dio3_tcxo_voltage"`
	RxBoostedGain   bool     `json:"rx_boosted_gain"`
	// GPIOChip selects a non-default gpiochip, which periph cannot express.
	GPIOChip *int `json:"gpio_chip"`

	Verified string `json:"verified"`
	Notes    string `json:"notes"`
}

type boardsDoc struct {
	Boards map[string]boardFile `json:"boards"`
}

var boards = mustLoadBoards()

func mustLoadBoards() map[string]Board {
	out, err := parseBoards(boardsJSON)
	if err != nil {
		panic("modem: shipped boards.json is invalid: " + err.Error())
	}
	return out
}

// LoadBoardOverrides merges BoardsFile over the shipped list. A missing file is
// fine; a malformed one is an error, since ignoring it would leave the operator
// picking from a list without the board they just added.
func LoadBoardOverrides() error {
	raw, err := os.ReadFile(BoardsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", BoardsFile, err)
	}
	extra, err := parseBoards(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", BoardsFile, err)
	}
	names := make([]string, 0, len(extra))
	for name, b := range extra {
		names = append(names, name)
		boards[name] = b
	}
	sort.Strings(names)
	slog.Info("loaded board overrides", "component", "modem", "file", BoardsFile, "boards", names)
	return nil
}

func parseBoards(raw []byte) (map[string]Board, error) {
	var doc boardsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Boards) == 0 {
		return nil, fmt.Errorf("no boards defined")
	}
	out := make(map[string]Board, len(doc.Boards))
	for name, bf := range doc.Boards {
		b, err := bf.board(name)
		if err != nil {
			return nil, fmt.Errorf("board %q: %w", name, err)
		}
		out[name] = b
	}
	return out, nil
}

// board converts one file entry, rejecting anything it cannot express: a board
// on the wrong pins is the failure mode with no symptom.
func (bf boardFile) board(name string) (Board, error) {
	switch bf.Verified {
	case "hardware", "community":
	default:
		return Board{}, fmt.Errorf("verified must be \"hardware\" or \"community\", got %q", bf.Verified)
	}
	if bf.TxPower == nil || *bf.TxPower <= 0 || *bf.TxPower > 30 {
		return Board{}, fmt.Errorf("tx_power must be set, 1-30 dBm")
	}
	reset, ok := pinName(bf.ResetPin)
	if !ok {
		return Board{}, fmt.Errorf("reset_pin is required")
	}
	busy, ok := pinName(bf.BusyPin)
	if !ok {
		return Board{}, fmt.Errorf("busy_pin is required")
	}
	tcxo, delay, err := tcxoSetting(bf)
	if err != nil {
		return Board{}, err
	}

	irq, _ := pinName(bf.IRQPin)
	cs, _ := pinName(bf.CSPin)
	txen, _ := pinName(bf.TxEnPin)
	rxen, _ := pinName(bf.RxEnPin)
	txled, _ := pinName(bf.TxLedPin)
	rxled, _ := pinName(bf.RxLedPin)

	enables := make([]string, 0, len(bf.EnPins)+1)
	for _, p := range append(append([]int{}, bf.EnPins...), derefPins(bf.EnPin)...) {
		if n, ok := pinName(&p); ok {
			enables = append(enables, n)
		}
	}

	b := Board{
		Name:       name,
		Label:      bf.Label,
		Chip:       bf.Chip,
		SPIPort:    fmt.Sprintf("SPI%d.%d", bf.BusID, bf.CSID),
		MaxTxPower: uint8(*bf.TxPower),
		Verified:   bf.Verified,
		Notes:      bf.Notes,
		leds:       ledPins{Tx: txled, Rx: rxled},
		opts: sx12xx.Opts{
			Speed:             8 * physic.MegaHertz,
			ResetPin:          reset,
			BusyPin:           busy,
			Dio1Pin:           irq,
			CSPin:             cs,
			TxEnPin:           txen,
			RxEnPin:           rxen,
			EnablePins:        enables,
			RegulatorMode:     sx12xx.RegulatorDCDCLDO,
			UseDIO2AsRfSwitch: bf.UseDIO2RF != nil && *bf.UseDIO2RF,
			TCXOVoltage:       tcxo,
			TCXODelay:         delay,
			BusyTimeout:       100 * time.Millisecond,
			RxBoostedGain:     bf.RxBoostedGain,
		},
	}
	// Modules with an integrated switch need neither line, but an imported board
	// list does not say which kind it is, and a terminated switch looks healthy.
	if !b.opts.UseDIO2AsRfSwitch && b.opts.TxEnPin == "" {
		b.Unsupported = "no RF switch control configured: set use_dio2_rf or txen_pin once confirmed against the board's schematic"
	}
	if bf.GPIOChip != nil && *bf.GPIOChip != 0 {
		b.Unsupported = fmt.Sprintf("needs gpiochip %d; periph resolves pins by name and has no chip selector", *bf.GPIOChip)
	}
	if b.Label == "" {
		b.Label = name
	}
	return b, nil
}

// tcxoSetting maps the DIO3 TCXO voltage onto the driver's constant. A wrong one
// comes up clean and then never hears anything.
func tcxoSetting(bf boardFile) (voltage byte, delay time.Duration, err error) {
	if bf.UseDIO3TCXO == nil || !*bf.UseDIO3TCXO {
		return 0, 0, nil
	}
	v := 1.8
	if bf.DIO3TCXOVoltage != nil {
		v = *bf.DIO3TCXOVoltage
	}
	byVolts := map[float64]byte{
		1.6: sx12xx.TCXO1_6V, 1.7: sx12xx.TCXO1_7V, 1.8: sx12xx.TCXO1_8V,
		2.2: sx12xx.TCXO2_2V, 2.4: sx12xx.TCXO2_4V, 2.7: sx12xx.TCXO2_7V,
		3.0: sx12xx.TCXO3_0V, 3.3: sx12xx.TCXO3_3V,
	}
	c, ok := byVolts[v]
	if !ok {
		return 0, 0, fmt.Errorf("dio3_tcxo_voltage %.1f is not one the SX126x can produce", v)
	}
	return c, 10 * time.Millisecond, nil
}

// pinName converts a BCM number to periph's gpioreg name; nil and -1 mean absent.
func pinName(bcm *int) (string, bool) {
	if bcm == nil || *bcm < 0 {
		return "", false
	}
	return fmt.Sprintf("GPIO%d", *bcm), true
}

func derefPins(p *int) []int {
	if p == nil {
		return nil
	}
	return []int{*p}
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
