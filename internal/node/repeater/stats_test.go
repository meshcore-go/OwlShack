package repeater

import "testing"

// A Linux host driving an SPI radio has no cell to read, and 0 mV would render
// as a flat battery: a node needing attention rather than one working exactly
// as designed.
func TestBatteryReading_AbsentWhenTheHostHasNoCell(t *testing.T) {
	r := &Repeater{}
	if got := r.batteryReading(); got != nil {
		t.Errorf("batteryReading() = %d, want nil when there is no battery to measure", *got)
	}

	// A board that does report one still publishes it, including a real 0.
	r.haveBattery.Store(true)
	r.batteryMV.Store(4168)
	if got := r.batteryReading(); got == nil || *got != 4168 {
		t.Errorf("batteryReading() = %v, want 4168 once a board reports one", got)
	}
}
