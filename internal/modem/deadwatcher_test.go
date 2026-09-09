package modem

import (
	"testing"
	"time"
)

func TestStartDeadWatcherSignalsReconnect(t *testing.T) {
	dead := make(chan struct{})
	ms := &State{Modem: deadStubModem{dead: dead}}
	reconnect := make(chan struct{}, 1)
	ms.StartDeadWatcher(reconnect)

	close(dead)
	select {
	case <-reconnect:
	case <-time.After(2 * time.Second):
		t.Fatal("a dead modem did not signal a reconnect")
	}
}

func TestStartDeadWatcherIgnoresModemWithoutDead(t *testing.T) {
	ms := &State{Modem: stubModem{}}
	reconnect := make(chan struct{}, 1)
	ms.StartDeadWatcher(reconnect)

	select {
	case <-reconnect:
		t.Fatal("a modem with no Dead() signalled a reconnect")
	case <-time.After(50 * time.Millisecond):
	}
}

type stubModem struct{}

func (stubModem) SendData([]byte) error                                                        { return nil }
func (stubModem) SetDataHandler(func(data []byte, snr float32, rssi int8, hasSignalInfo bool)) {}
func (stubModem) AddOutboundHandler(func([]byte))                                              {}

type deadStubModem struct {
	stubModem
	dead chan struct{}
}

func (m deadStubModem) Dead() <-chan struct{} { return m.dead }
