package modem

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/meshcore-go/meshcore-go/hardware/sx12xx"
)

// sx12xxStatsProvider reports what a directly-attached SPI radio can measure.
// There is no MeshCore firmware in front of the chip, so the board readings a
// KISS modem answers for — battery, MCU temperature — have no source here: the
// host is a Raspberry Pi, not a battery-powered board.
type sx12xxStatsProvider struct {
	// modem is attached after NewModem, which needs this provider's error
	// handler, so the two cannot be constructed in one order.
	modem     atomic.Pointer[sx12xx.Modem]
	radio     RadioInfo
	startTime time.Time
	log       *slog.Logger

	// driverErrors counts faults the driver reported: SPI transaction failures,
	// busy-line timeouts, IRQ reads that failed. The only fault counter this
	// path has, so it is the one signal that the radio is misbehaving.
	driverErrors atomic.Uint64
}

// NewSx12xxStatsProvider wraps a directly-attached radio. Pass NoteError as the
// modem's error handler so driver faults are counted, then Attach the modem.
func NewSx12xxStatsProvider(radio RadioInfo) *sx12xxStatsProvider {
	return &sx12xxStatsProvider{
		radio:     radio,
		startTime: time.Now(),
		log:       slog.Default().With("component", "stats", "type", "sx12xx"),
	}
}

// Attach supplies the modem the readings come from.
func (p *sx12xxStatsProvider) Attach(m *sx12xx.Modem) { p.modem.Store(m) }

// NoteError records a driver fault.
func (p *sx12xxStatsProvider) NoteError(err error) {
	p.driverErrors.Add(1)
	p.log.Warn("radio driver error", "error", err)
}

// DriverErrors is the count of faults the driver has reported.
func (p *sx12xxStatsProvider) DriverErrors() uint64 { return p.driverErrors.Load() }

func (p *sx12xxStatsProvider) RadioConfig() RadioInfo { return p.radio }

// Stats reports the noise floor the modem measures and our own uptime. Battery
// and MCU temperature are left at zero with HaveMCUTemp false: the Pi has
// neither sensor, and inventing a reading here is how a board with no battery
// came to report a healthy 100%.
func (p *sx12xxStatsProvider) Stats(context.Context) DeviceStats {
	ds := DeviceStats{UptimeSecs: uint32(time.Since(p.startTime).Seconds())}
	if m := p.modem.Load(); m != nil {
		ds.NoiseFloor = int16(m.NoiseFloor())
	}
	return ds
}

// LinkStats maps the driver's own counters onto the fields that mean the same
// thing, and leaves the rest at zero. Six of the eight are KISS protocol
// concepts — HW_RESP_ERROR frames, SETHARDWARE decoding, TX_DONE waits, signal
// metadata paired to a packet — with no SPI analogue: the chip hands us a
// decoded packet with its RSSI and SNR already attached, so metadata can
// neither time out nor be misattributed.
//
// PacketsDropped is a real inbound-queue overflow, so it maps onto
// InboundDroppedNew: the driver drops the newest packet when the consumer
// cannot keep up, which is what that field counts. PacketsRecvErrors is a
// packet that raised an interrupt and could not be read out — the closest
// analogue to a hardware decode error. CRC errors have no KISS field at all
// and are reported through CRCErrors instead of being folded in, because a
// noisy channel and a driver fault call for different responses.
//
// ponytail: the remaining zeroes are still indistinguishable from "measured,
// none happened", the same ambiguity battery_mv carries. Making them omittable
// is a wire change shared with meshcore-bot and CoreScope, so it is Wesley's
// call, not this file's.
func (p *sx12xxStatsProvider) LinkStats() LinkStats {
	m := p.modem.Load()
	if m == nil {
		return LinkStats{}
	}
	return radioLinkStats(m.Stats())
}

// radioLinkStats maps the driver's counters onto the KISS-shaped fields. Pure,
// so the mapping is testable without a radio: it is three assignments among
// eight similar-looking fields, which is exactly the kind of thing that gets
// cross-wired and then reads as a plausible number.
func radioLinkStats(s sx12xx.RadioStats) LinkStats {
	return LinkStats{
		InboundDroppedNew: s.PacketsDropped,
		HwDecodeErrors:    s.PacketsRecvErrors,
	}
}

// RadioCounters are the driver's own counters, for the parts of the SPI path
// that have no KISS field to land in.
type RadioCounters struct {
	PacketsRecv uint64
	PacketsSent uint64
	CRCErrors   uint64
	// DriverErrors is faults the driver reported through its error handler:
	// SPI transaction failures, busy-line timeouts, IRQ reads that failed.
	DriverErrors uint64
}

// Counters reports the driver's counters. Zero-valued when no modem is
// attached, which the caller can tell apart by checking the provider type.
func (p *sx12xxStatsProvider) Counters() RadioCounters {
	driver := p.driverErrors.Load()
	m := p.modem.Load()
	if m == nil {
		return RadioCounters{DriverErrors: driver}
	}
	return radioCounters(m.Stats(), driver)
}

// radioCounters maps the driver's counters onto ours. Pure, for the same reason
// as radioLinkStats.
func radioCounters(s sx12xx.RadioStats, driverErrors uint64) RadioCounters {
	return RadioCounters{
		PacketsRecv:  s.PacketsRecv,
		PacketsSent:  s.PacketsSent,
		CRCErrors:    s.PacketsCRCErrors,
		DriverErrors: driverErrors,
	}
}

// EstAirtimeMs and PacketScore come from the modem, which holds the modulation
// it was configured with. Both mirror the firmware's getEstAirtimeFor and
// packetScore, and fill the packet log's time= and score= fields.
func (p *sx12xxStatsProvider) EstAirtimeMs(packetLen int) uint32 {
	m := p.modem.Load()
	if m == nil {
		return 0
	}
	return m.AirtimeEstimator()(packetLen)
}

func (p *sx12xxStatsProvider) PacketScore(snrDB float64, packetLen int) float64 {
	m := p.modem.Load()
	if m == nil {
		return 0
	}
	return m.PacketScore(snrDB, packetLen)
}
