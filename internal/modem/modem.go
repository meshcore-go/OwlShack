// Package modem owns connecting to the radio hardware (KISS or companion),
// exposing it as a node.Modem plus a device-stats provider, and building the
// standard RadioMux options. The supervisor loop that drives reconnects lives
// in the app package and consumes this one.
package modem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/config"
	companionClient "github.com/meshcore-go/meshcore-go/companion/client"
	companionTransport "github.com/meshcore-go/meshcore-go/companion/transport"
	"github.com/meshcore-go/meshcore-go/hardware"
	kissTransport "github.com/meshcore-go/meshcore-go/hardware/transport"
	"github.com/meshcore-go/meshcore-go/node"
)

// closerFunc adapts a bare func() to io.Closer for cleanup bookkeeping.
type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

// State holds a live modem connection together with the resources that must be
// torn down when it is replaced (on reconnect) or shut down.
type State struct {
	Modem      node.Modem
	Stats      StatsProvider
	RecvErrors *atomic.Uint64

	radioConfig *hardware.RadioConfig
	closers     []io.Closer
	watcherDone chan struct{}
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
// Dead() (e.g. the companion modem).
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
		opts = append(opts,
			node.WithMuxAirtimeEstimator(hardware.LoRaAirtimeEstimator(ms.radioConfig)),
			node.WithMuxRetryable(func(err error) bool {
				return errors.Is(err, hardware.ErrTxBusy)
			}),
		)
	}
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

	switch *cfg.NodeType {
	case "kiss":
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

		kissModem := hardware.NewKissModem(
			t,
			hardware.WithSignalReport(true),
			hardware.WithLogger(slog.Default()),
		)

		connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
		defer connectCancel()

		if err := kissModem.Connect(connectCtx); err != nil {
			return nil, fmt.Errorf("kiss connect: %w", err)
		}
		ms.closers = append(ms.closers, kissModem)

		ms.radioConfig = radioConfig

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

	case "companion":
		var t companionTransport.Transport

		switch connScheme {
		case "serial":
			t = companionTransport.NewSerialTransport(companionTransport.SerialConfig{
				Port:     connAddr,
				BaudRate: *cfg.BaudRate,
			})
		case "tcp":
			t = companionTransport.NewTCPTransport(companionTransport.TCPConfig{
				Address: connAddr,
			})
		}

		client := companionClient.New(t)
		client.SetErrorHandler(func(err error) {
			slog.Error("companion error", "component", "modem", "error", err)
		})

		connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
		defer connectCancel()

		if err := client.Connect(connectCtx); err != nil {
			return nil, fmt.Errorf("companion connect: %w", err)
		}
		ms.closers = append(ms.closers, client)

		selfInfo, err := client.AppStart(connectCtx, 1, "meshcore-bot")
		if err != nil {
			ms.Close()
			return nil, fmt.Errorf("companion app start: %w", err)
		}
		ms.Stats = NewCompanionStatsProvider(client, selfInfo)

		compModem := companionClient.NewCompanionModem(ctx, client)
		ms.closers = append(ms.closers, closerFunc(compModem.Close))

		ms.Modem = compModem

	default:
		return nil, fmt.Errorf("unsupported node type: %s", *cfg.NodeType)
	}

	return ms, nil
}
