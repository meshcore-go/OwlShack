// Package modem owns connecting to the KISS radio hardware, exposing it as a
// node.Modem plus a device-stats provider, and building the standard RadioMux
// options. The supervisor loop that drives reconnects lives in the app
// package and consumes this one.
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

// handlerWatchdog is the per-dispatch latency alarm. Not configurable: DATA
// dispatch is sub-millisecond by design, so any value an operator might pick
// would only mask a stall.
const handlerWatchdog = 500 * time.Millisecond

// State holds a live modem connection together with the resources that must be
// torn down when it is replaced (on reconnect) or shut down.
type State struct {
	Modem      node.Modem
	Stats      StatsProvider
	RecvErrors *atomic.Uint64

	radioConfig   *hardware.RadioConfig
	airtimeFactor float64
	closers       []io.Closer
	watcherDone   chan struct{}
}

// Close stops the dead-watcher goroutine and closes the modem's resources in
// reverse order of acquisition.
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

// StartDeadWatcher spawns a goroutine that signals reconnectCh once when the
// underlying modem's read loop exits. No-op for modems that don't expose
// Dead().
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

// MuxOptions builds the standard mux options for the given modem state.
// Centralized so the same options are used at startup and after reconnect.
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

// Setup connects to the modem described by cfg and returns the ready State.
func Setup(ctx context.Context, cfg *config.Config) (*State, error) {
	ms := &State{
		RecvErrors: &atomic.Uint64{},
	}

	conn := *cfg.Connection
	connScheme, connAddr, ok := config.ParseConnection(conn)
	if !ok {
		return nil, fmt.Errorf("invalid connection string: %s", conn)
	}

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

	radioConfig := &hardware.RadioConfig{
		FreqHz: uint32(*cfg.Freq * 1000000),
		BwHz:   uint32(*cfg.Bw * 1000),
		SF:     *cfg.SF,
		CR:     *cfg.CR,
	}

	// TX flow control paces sends to the radio: the firmware holds ONE pending
	// TX slot and drops any data frame that arrives while it's busy
	// (HW_ERR_TX_BUSY), so without waiting for TX_DONE a burst is silently lost
	// — the airtime budget alone permits back-to-back writes. The estimator
	// sizes that wait from the radio's own time-on-air, since the fixed default
	// is too short for a max-length frame at a high spreading factor.
	kissModem := hardware.NewKissModem(
		t,
		hardware.WithSignalReport(true),
		hardware.WithLogger(slog.Default()),
		hardware.WithTxFlowControl(hardware.DefaultTxTimeout),
		hardware.WithTxAirtimeEstimator(hardware.LoRaAirtimeEstimator(radioConfig)),
		// DATA frames dispatch serially on one goroutine, and our single mux
		// fans out to the companion, the repeater and the observer inside that
		// one call — so a slow handler stalls RX for all of them. Dispatch
		// should be sub-millisecond; 500ms only fires on a real stall.
		hardware.WithHandlerWatchdog(handlerWatchdog),
	)
	kissModem.SetErrorHandler(func(err error) {
		slog.Warn("modem error", "component", "modem", "error", err)
	})

	connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connectCancel()

	if err := kissModem.Connect(connectCtx); err != nil {
		return nil, fmt.Errorf("kiss connect: %w", err)
	}
	ms.closers = append(ms.closers, kissModem)

	ms.radioConfig = radioConfig
	ms.airtimeFactor = cfg.AirtimeFactorOr()
	// The library takes an inverted factor, so log the percentage an operator
	// actually cares about — deriving it from the factor is the exact mistake
	// this line exists to prevent.
	slog.Info("airtime budget",
		"duty_cycle_pct", cfg.DutyCyclePercentOr(), "airtime_factor", ms.airtimeFactor)

	if err := kissModem.SetRadio(radioConfig); err != nil {
		ms.Close()
		return nil, fmt.Errorf("SET_RADIO: %w", err)
	}
	slog.Info("SET_RADIO", "freq", *cfg.Freq, "bw", *cfg.Bw, "sf", *cfg.SF, "cr", *cfg.CR)

	if err := kissModem.SetTxPower(*cfg.TX); err != nil {
		ms.Close()
		return nil, fmt.Errorf("SET_TX_POWER: %w", err)
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

	return ms, nil
}
