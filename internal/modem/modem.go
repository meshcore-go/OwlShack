// Package modem connects to the KISS radio hardware and exposes it as a node.Modem, a stats provider and the standard mux options.
package modem

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/meshcore-go/hardware"
	kissTransport "github.com/meshcore-go/meshcore-go/hardware/transport"
	"github.com/meshcore-go/meshcore-go/node"
)

// handlerWatchdog is deliberately not configurable: DATA dispatch is sub-millisecond, so an operator's value could only mask a stall.
const handlerWatchdog = 500 * time.Millisecond

// State holds a live modem connection with the resources torn down when it is replaced or shut down.
type State struct {
	Modem      node.Modem
	Stats      StatsProvider
	RecvErrors *atomic.Uint64

	radioConfig   *hardware.RadioConfig
	airtimeFactor float64
	closers       []io.Closer
	watcherDone   chan struct{}
}

// Close stops the dead-watcher and closes the modem's resources in reverse order of acquisition.
func (m *State) Close() {
	if m.watcherDone != nil {
		select {
		case <-m.watcherDone:
		default:
			close(m.watcherDone)
		}
	}
	for i := len(m.closers) - 1; i >= 0; i-- {
		m.closers[i].Close()
	}
}

// StartDeadWatcher signals reconnectCh once when the read loop exits; a no-op for modems without Dead().
func (m *State) StartDeadWatcher(reconnectCh chan<- struct{}) {
	d, ok := m.Modem.(interface{ Dead() <-chan struct{} })
	if !ok {
		return
	}
	m.watcherDone = make(chan struct{})
	done := m.watcherDone
	dead := d.Dead()
	go func() {
		select {
		case <-dead:
			select {
			case reconnectCh <- struct{}{}:
			default:
			}
		case <-done:
		}
	}()
}

// MuxOptions builds the standard mux options, shared by startup and reconnect.
func MuxOptions(ms *State) []node.MuxOption {
	opts := []node.MuxOption{
		node.WithMuxLogger(slog.Default()),
		node.WithMuxErrorHandler(func(err error) {
			slog.Debug("mux receive error", "component", "modem", "error", err)
			ms.RecvErrors.Add(1)
		}),
	}
	if ms.radioConfig != nil {
		opts = append(opts, node.WithMuxAirtimeEstimator(hardware.LoRaAirtimeEstimator(ms.radioConfig)))
	}
	opts = append(opts, node.WithMuxAirtimeFactor(ms.airtimeFactor))
	return opts
}

// Setup connects to the radio described by cfg and returns the ready State.
// Two backends: a KISS modem over serial or TCP, which is MeshCore firmware
// driving the radio for us, and a bare SX12xx wired to the host's SPI bus,
// where there is no firmware and this process is the radio stack.
func Setup(ctx context.Context, cfg *config.Config) (*State, error) {
	ms := &State{
		RecvErrors: &atomic.Uint64{},
	}

	conn := *cfg.Connection
	connScheme, connAddr, ok := config.ParseConnection(conn)
	if !ok {
		return nil, fmt.Errorf("invalid connection string: %s", conn)
	}

	radioConfig := &hardware.RadioConfig{
		FreqHz: uint32(*cfg.Freq * 1000000),
		BwHz:   uint32(*cfg.Bw * 1000),
		SF:     *cfg.SF,
		CR:     *cfg.CR,
	}
	ms.radioConfig = radioConfig
	ms.airtimeFactor = cfg.AirtimeFactorOr()
	// The library takes an inverted factor, so log the percentage an operator
	// actually cares about — deriving it from the factor is the exact mistake
	// this line exists to prevent.
	slog.Info("airtime budget",
		"duty_cycle_pct", cfg.DutyCyclePercentOr(), "airtime_factor", ms.airtimeFactor)

	var err error
	switch connScheme {
	case "spi":
		err = setupSPI(ms, cfg, connAddr, radioConfig)
	default:
		err = setupKiss(ctx, ms, cfg, connScheme, connAddr, radioConfig)
	}
	if err != nil {
		return nil, err
	}
	return ms, nil
}

// setupKiss connects to MeshCore firmware over KISS and configures its radio.
func setupKiss(ctx context.Context, ms *State, cfg *config.Config, connScheme, connAddr string, radioConfig *hardware.RadioConfig) error {
	var t hardware.Transport
	switch connScheme {
	case "serial":
		t = kissTransport.NewSerialTransport(kissTransport.SerialConfig{
			Port:     connAddr,
			BaudRate: *cfg.BaudRate,
		})
	case "tcp":
		t = kissTransport.NewTCPTransport(kissTransport.TCPConfig{
			Address: connAddr,
		})
	}

	// The firmware holds ONE pending TX slot and drops frames arriving while busy (HW_ERR_TX_BUSY), so sends wait for TX_DONE.
	kissModem := hardware.NewKissModem(
		t,
		hardware.WithSignalReport(true),
		hardware.WithLogger(slog.Default()),
		hardware.WithTxFlowControl(hardware.DefaultTxTimeout),
		hardware.WithTxAirtimeEstimator(hardware.LoRaAirtimeEstimator(radioConfig)),
		hardware.WithHandlerWatchdog(handlerWatchdog),
	)
	kissModem.SetErrorHandler(func(err error) {
		slog.Warn("modem error", "component", "modem", "error", err)
	})

	connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connectCancel()

	if err := kissModem.Connect(connectCtx); err != nil {
		return fmt.Errorf("kiss connect: %w", err)
	}
	ms.closers = append(ms.closers, kissModem)

	if err := kissModem.SetRadio(radioConfig); err != nil {
		ms.Close()
		return fmt.Errorf("SET_RADIO: %w", err)
	}
	slog.Info("SET_RADIO", "freq", *cfg.Freq, "bw", *cfg.Bw, "sf", *cfg.SF, "cr", *cfg.CR)

	if err := kissModem.SetTxPower(*cfg.TX); err != nil {
		ms.Close()
		return fmt.Errorf("SET_TX_POWER: %w", err)
	}
	slog.Info("SET_TX_POWER", "tx", *cfg.TX)

	ms.Stats = NewKissStatsProvider(kissModem, RadioInfo{
		FreqHz:  uint32(*cfg.Freq * 1000000),
		BwHz:    uint32(*cfg.Bw * 1000),
		SF:      *cfg.SF,
		CR:      *cfg.CR,
		TxPower: *cfg.TX,
	})

	ms.Modem = kissModem

	return nil
}
