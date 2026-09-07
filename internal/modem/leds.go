package modem

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
)

// ledPulse is long enough to see, short enough that two packets read as two blinks.
const ledPulse = 60 * time.Millisecond

// ledPins are a board's activity LED lines by gpioreg name, empty when it has none.
type ledPins struct {
	Tx string
	Rx string
}

func (p ledPins) any() bool { return p.Tx != "" || p.Rx != "" }

// activityLEDs blinks a board's TX and RX LEDs.
type activityLEDs struct {
	tx *blinker
	rx *blinker
}

// openLEDs claims the LED pins, skipping any that will not open: refusing to
// start over an LED would be worse than a dark one.
func openLEDs(p ledPins) *activityLEDs {
	if !p.any() {
		return nil
	}
	l := &activityLEDs{}
	for _, spec := range []struct {
		name string
		role string
		dst  **blinker
	}{
		{p.Tx, "tx", &l.tx},
		{p.Rx, "rx", &l.rx},
	} {
		if spec.name == "" {
			continue
		}
		b, err := newBlinker(spec.name, spec.role)
		if err != nil {
			slog.Warn("activity LED unavailable", "component", "modem",
				"role", spec.role, "pin", spec.name, "error", err)
			continue
		}
		*spec.dst = b
	}
	if l.tx == nil && l.rx == nil {
		return nil
	}
	return l
}

func (l *activityLEDs) noteTx() {
	if l != nil {
		l.tx.pulse()
	}
}

func (l *activityLEDs) noteRx() {
	if l != nil {
		l.rx.pulse()
	}
}

// Close darkens both LEDs; one left lit shows a node still transmitting.
func (l *activityLEDs) Close() error {
	if l == nil {
		return nil
	}
	l.tx.close()
	l.rx.close()
	return nil
}

// blinker drives one LED, darkened by a timer so the dispatch path never waits.
type blinker struct {
	pin  gpio.PinOut
	role string

	mu    sync.Mutex
	timer *time.Timer

	// A failing write fails on every packet, so it is reported once.
	warnOnce sync.Once
}

func newBlinker(name, role string) (*blinker, error) {
	pin := gpioreg.ByName(name)
	if pin == nil {
		return nil, fmt.Errorf("no gpio named %q on this host", name)
	}
	if err := pin.Out(gpio.Low); err != nil {
		return nil, err
	}
	b := &blinker{pin: pin, role: role}
	// AfterFunc is the only Timer whose Reset schedules a callback.
	b.timer = time.AfterFunc(ledPulse, b.off)
	b.timer.Stop()
	return b, nil
}

func (b *blinker) pulse() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.set(gpio.High)
	// Reset, so back-to-back traffic extends one pulse instead of racing two timers.
	b.timer.Reset(ledPulse)
}

func (b *blinker) off() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.set(gpio.Low)
}

func (b *blinker) close() {
	if b == nil {
		return
	}
	b.timer.Stop()
	b.off()
}

// set writes the pin, reporting a failure once. Callers hold b.mu.
func (b *blinker) set(level gpio.Level) {
	if err := b.pin.Out(level); err != nil {
		b.warnOnce.Do(func() {
			slog.Warn("activity LED write failed, LED will stay dark",
				"component", "modem", "role", b.role, "pin", b.pin.Name(), "error", err)
		})
	}
}

// ledModem blinks the RX LED on each received packet. The concrete type is
// embedded, not node.Modem, so Close and Dead still promote: hiding Dead would
// leave the supervisor's reconnect watcher dead with no log line saying why.
// TX needs no override, since the driver already calls outbound handlers.
type ledModem struct {
	*sx12xx.Modem
	leds *activityLEDs
}

func (m *ledModem) SetDataHandler(h func(data []byte, snr float32, rssi int8, hasSignalInfo bool)) {
	m.Modem.SetDataHandler(func(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
		m.leds.noteRx()
		h(data, snr, rssi, hasSignalInfo)
	})
}
