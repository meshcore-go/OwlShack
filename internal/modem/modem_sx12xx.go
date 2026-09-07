package modem

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/meshcore-go/hardware"
	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
)

// periphOnce guards host.Init, which enumerates the board's buses and GPIOs.
// Once per process: a reconnect re-opens the SPI port, not the host.
var periphOnce struct {
	sync.Once
	err error
}

func initPeriph() error {
	periphOnce.Do(func() {
		if _, err := host.Init(); err != nil {
			periphOnce.err = fmt.Errorf("periph host init: %w", err)
		}
	})
	return periphOnce.err
}

// setupSPI drives an SX12xx wired straight to the host's SPI bus. There is no
// MeshCore firmware in front of the chip, so this process is the radio stack:
// the modem applies the MeshCore modulation and sync word itself and gates
// transmissions on channel activity.
func setupSPI(ms *State, cfg *config.Config, connAddr string, radioConfig *hardware.RadioConfig) error {
	if cfg.SPIBoard == nil || *cfg.SPIBoard == "" {
		return fmt.Errorf("spi connection needs spiBoard set (known: %v)", BoardNames())
	}
	board, err := LookupBoard(*cfg.SPIBoard)
	if err != nil {
		return err
	}

	// Past the module's rating the PA overheats, so this is a hard error rather
	// than a silent clamp: an operator who asked for 30 dBm on a 22 dBm part
	// needs to know the number they set is not the number they get.
	txPower := *cfg.TX
	if txPower > board.MaxTxPower {
		return fmt.Errorf("tx power %d dBm exceeds %s maximum of %d dBm",
			txPower, board.Label, board.MaxTxPower)
	}

	if err := initPeriph(); err != nil {
		return err
	}

	portName := connAddr
	if portName == "" {
		portName = board.SPIPort
	}
	port, err := spireg.Open(portName)
	if err != nil {
		return fmt.Errorf("open spi %s: %w", portName, err)
	}
	ms.closers = append(ms.closers, port)

	opts := board.Opts()
	radio, err := sx12xx.NewSX126x(port, &opts)
	if err != nil {
		ms.Close()
		return fmt.Errorf("sx126x on %s: %w", portName, err)
	}

	// Calibration and oscillator faults land here, and a wrong TCXO voltage is
	// the common one: the radio comes up, reports no error on any setter, and
	// simply never hears anything.
	if de, err := radio.DeviceErrors(); err != nil {
		slog.Warn("radio device errors unreadable", "component", "modem", "error", err)
	} else if de != 0 {
		slog.Error("radio reports device errors", "component", "modem",
			"errors", fmt.Sprintf("%#04x", de), "hint", "check TCXO voltage and frequency band")
	}

	stats := NewSx12xxStatsProvider(RadioInfo{
		FreqHz:  radioConfig.FreqHz,
		BwHz:    radioConfig.BwHz,
		SF:      radioConfig.SF,
		CR:      radioConfig.CR,
		TxPower: txPower,
	})

	m, err := sx12xx.NewModem(radio, radioConfig,
		sx12xx.WithTxPower(int(txPower)),
		sx12xx.WithModemLogger(slog.Default()),
		sx12xx.WithModemErrorHandler(func(err error) {
			stats.NoteError(err)
			ms.RecvErrors.Add(1)
		}),
	)
	if err != nil {
		ms.Close()
		return fmt.Errorf("sx12xx modem: %w", err)
	}
	ms.closers = append(ms.closers, m)
	stats.Attach(m)

	pre, payload := sx12xx.PacketWindows(radioConfig)
	slog.Info("radio up", "component", "modem", "board", board.Name, "chip", board.Chip,
		"spi", portName, "freq", *cfg.Freq, "bw", *cfg.Bw, "sf", *cfg.SF, "cr", *cfg.CR,
		"tx", txPower, "preamble_symbols", sx12xx.PreambleForSF(radioConfig.SF),
		"activity_window", pre+payload)

	ms.Stats = stats
	ms.Modem = m
	return nil
}
