package modem

import (
	"testing"

	"github.com/meshcore-go/meshcore-go/hardware"
	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
)

var _ StatsProvider = (*sx12xxStatsProvider)(nil)

// A Pi has no battery and no MCU temperature sensor, and there is no firmware
// to ask. Those readings must stay absent rather than arrive as zeroes that
// look like a flat battery and a freezing board: a fabricated healthy default
// is what made a battery-less board report 100%.
func TestSx12xxStats_ReportsNoBoardReadingsItCannotMeasure(t *testing.T) {
	p := NewSx12xxStatsProvider(RadioInfo{FreqHz: 917_375_000, BwHz: 62_500, SF: 7, CR: 5, TxPower: 22})

	ds := p.Stats(t.Context())
	if ds.BatteryMV != 0 || ds.HaveBattery {
		t.Errorf("battery = %d mV (have=%v), want absent: there is no battery to read",
			ds.BatteryMV, ds.HaveBattery)
	}
	if ds.HaveMCUTemp {
		t.Error("HaveMCUTemp = true, but there is no MCU sensor on this path")
	}
	if got := p.RadioConfig(); got.FreqHz != 917_375_000 || got.SF != 7 || got.TxPower != 22 {
		t.Errorf("RadioConfig() = %+v, want the modulation it was built with", got)
	}
}

// A CRC error is a noisy channel; a driver error is our fault. Folding them
// together would have an operator chasing software for an antenna problem, so
// they stay separate — and neither is silently reported as the other.
func TestSx12xxStats_KeepsCRCAndDriverErrorsSeparate(t *testing.T) {
	// Distinct values per field, so a cross-wired mapping shows up as the wrong
	// number rather than coincidentally matching.
	s := sx12xx.RadioStats{
		PacketsRecv: 100, PacketsSent: 20,
		PacketsRecvErrors: 3, PacketsCRCErrors: 7, PacketsDropped: 5,
	}
	ls := radioLinkStats(s)
	if ls.CRCErrors == nil || *ls.CRCErrors != 7 {
		t.Errorf("CRCErrors = %v, want 7 (the driver's CRC count, not another counter)", ls.CRCErrors)
	}
	if ls.InboundDroppedNew != 5 {
		t.Errorf("InboundDroppedNew = %d, want 5 (PacketsDropped)", ls.InboundDroppedNew)
	}
	if ls.HwDecodeErrors != 3 {
		t.Errorf("HwDecodeErrors = %d, want 3 (PacketsRecvErrors)", ls.HwDecodeErrors)
	}
	// The KISS-only fields stay zero rather than being filled with whatever
	// number was nearest.
	if ls.RxMetaTimeouts != 0 || ls.RxMetaMisattributed != 0 || ls.HwErrors != 0 ||
		ls.TxOutcomeLost != 0 || ls.InboundDroppedOldest != 0 || ls.HandlerSlow != 0 {
		t.Errorf("a KISS-only field was populated on the SPI path: %+v", ls)
	}
}

// A KISS modem counts none of the SPI driver's faults. Reporting them as 0
// would read as "measured, none happened" on every KISS node on the mesh, which
// is the failure this whole seam exists to avoid.
func TestKissLinkStats_LeavesTheSPICountersUnmeasured(t *testing.T) {
	ls := (&kissStatsProvider{modem: &hardware.KissModem{}}).LinkStats()
	if ls.CRCErrors != nil || ls.DriverErrors != nil || ls.RecvRecoveries != nil {
		t.Errorf("a KISS modem reported an SPI counter: crc=%v driver=%v recoveries=%v",
			ls.CRCErrors, ls.DriverErrors, ls.RecvRecoveries)
	}
}

// The error handler is wired up before the modem is attached, so a radio that
// never came up at all must still report why rather than looking unmeasured.
func TestSx12xxStats_ReportsDriverErrorsBeforeAttach(t *testing.T) {
	p := NewSx12xxStatsProvider(RadioInfo{})
	p.NoteError(errFake{})

	ls := p.LinkStats()
	if ls.DriverErrors == nil || *ls.DriverErrors != 1 {
		t.Errorf("DriverErrors = %v with no modem attached, want 1", ls.DriverErrors)
	}
	if ls.CRCErrors != nil || ls.RecvRecoveries != nil {
		t.Error("a counter that only the modem can supply was reported without one")
	}
}

// Before Attach, and after a driver teardown, the provider has no modem to ask.
// It must return zero rather than dereference one.
func TestSx12xxStats_SurvivesWithNoModemAttached(t *testing.T) {
	p := NewSx12xxStatsProvider(RadioInfo{})
	if got := p.EstAirtimeMs(64); got != 0 {
		t.Errorf("EstAirtimeMs = %d, want 0 with no modem attached", got)
	}
	if got := p.PacketScore(-4.75, 64); got != 0 {
		t.Errorf("PacketScore = %v, want 0 with no modem attached", got)
	}
	if got := p.Stats(t.Context()).NoiseFloor; got != 0 {
		t.Errorf("NoiseFloor = %d, want 0 with no modem attached", got)
	}
}

// Driver faults are the only fault signal this path has, so they must be
// counted rather than only logged.
func TestSx12xxStats_CountsDriverErrors(t *testing.T) {
	p := NewSx12xxStatsProvider(RadioInfo{})
	if got := p.DriverErrors(); got != 0 {
		t.Fatalf("DriverErrors = %d on a fresh provider, want 0", got)
	}
	p.NoteError(errFake{})
	p.NoteError(errFake{})
	if got := p.DriverErrors(); got != 2 {
		t.Errorf("DriverErrors = %d after two faults, want 2", got)
	}
}

type errFake struct{}

func (errFake) Error() string { return "spi transaction failed" }
