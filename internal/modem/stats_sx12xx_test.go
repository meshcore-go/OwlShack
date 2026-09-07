package modem

import (
	"testing"

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
	if ds.BatteryMV != 0 {
		t.Errorf("BatteryMV = %d, want 0: there is no battery to read", ds.BatteryMV)
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
	p := NewSx12xxStatsProvider(RadioInfo{})
	p.NoteError(errFake{})

	if got := p.Counters().DriverErrors; got != 1 {
		t.Errorf("DriverErrors = %d, want 1", got)
	}

	// Distinct values per field, so a cross-wired mapping shows up as the wrong
	// number rather than coincidentally matching.
	s := sx12xx.RadioStats{
		PacketsRecv: 100, PacketsSent: 20,
		PacketsRecvErrors: 3, PacketsCRCErrors: 7, PacketsDropped: 5,
	}
	c := radioCounters(s, 11)
	if c.PacketsRecv != 100 || c.PacketsSent != 20 {
		t.Errorf("counters = %+v, want recv 100 / sent 20", c)
	}
	if c.CRCErrors != 7 {
		t.Errorf("CRCErrors = %d, want 7 (the driver's CRC count, not another counter)", c.CRCErrors)
	}
	if c.DriverErrors != 11 {
		t.Errorf("DriverErrors = %d, want 11; a CRC error is a noisy channel, not our fault", c.DriverErrors)
	}

	ls := radioLinkStats(s)
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
