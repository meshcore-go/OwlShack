package modem

import (
	"context"
	"encoding/binary"
	"log/slog"
	"sync"
	"time"

	"github.com/meshcore-go/OwlShack/internal/logging"
	"github.com/meshcore-go/meshcore-go/hardware"
)

type RadioInfo struct {
	FreqHz  uint32
	BwHz    uint32
	SF      uint8
	CR      uint8
	TxPower uint8
}

type DeviceStats struct {
	NoiseFloor int16
	BatteryMV  uint16
	UptimeSecs uint32
	// MCUTempC is the modem board's MCU temperature. HaveMCUTemp is false when
	// the board can't measure one (the modem answers HW_ERR_NO_CALLBACK), so
	// 0 °C is distinguishable from unknown.
	MCUTempC    float64
	HaveMCUTemp bool
}

// LinkStats mirrors hardware.ModemStats so consumers don't take a dependency
// on the driver package. These are the library's own counters for faults we
// otherwise never see: dropped frames, hardware errors, stalled handlers, and
// signal metadata that could not be matched to a packet.
//
// ponytail: KISS-shaped despite the neutral name, and KISS is the only driver
// meshcore-go v1.3.0 has. Only InboundDropped* and HandlerSlow are
// driver-neutral (any modem has an inbound queue and a dispatch loop); the
// other six are KISS *protocol* concepts — HW_RESP_ERROR frames, SETHARDWARE
// frame decoding, TX_DONE waits, signal-report frame pairing — with no SPI
// analogue. When a direct SX126x/SPI driver lands, those six must become
// omittable on the wire rather than publishing 0, the way DeviceStats.
// HaveMCUTemp keeps "no sensor" distinguishable from 0 °C; and that driver's
// own counters (CRC errors, IRQ timeouts, FIFO overruns) will need somewhere
// to go. Don't design that seam before the driver exists.
type LinkStats struct {
	// InboundDropped* are frames discarded because our inbound queue was full.
	InboundDroppedOldest uint64
	InboundDroppedNew    uint64
	// RxMetaTimeouts is signal metadata that never arrived; RxMetaMisattributed
	// is metadata matched to the wrong packet — which means any SNR/RSSI we
	// publish for it is wrong, so it matters more than its rarity suggests.
	RxMetaTimeouts      uint64
	RxMetaMisattributed uint64
	// HandlerSlow counts dispatches over the watchdog threshold. DATA frames
	// dispatch serially, so one slow handler stalls RX for every consumer.
	HandlerSlow    uint64
	HwDecodeErrors uint64
	HwErrors       uint64 // HW_RESP_ERROR frames received
	TxOutcomeLost  uint64 // TX_DONE waits abandoned by a reconnect
}

type StatsProvider interface {
	RadioConfig() RadioInfo
	Stats(ctx context.Context) DeviceStats
	// LinkStats reports the driver's fault counters. No ctx: these are atomic
	// loads, unlike Stats which polls the board over the wire.
	LinkStats() LinkStats
	// EstAirtimeMs and PacketScore mirror the firmware's getEstAirtimeFor and
	// packetScore, which is what its packet log reports as time= and score=.
	// They live here so consumers stay off the driver package; both return 0
	// when the radio params are unknown. Pure arithmetic, so no ctx.
	EstAirtimeMs(packetLen int) uint32
	PacketScore(snrDB float64, packetLen int) float64
}

type kissStatsProvider struct {
	modem     *hardware.KissModem
	radio     RadioInfo
	startTime time.Time
	log       *slog.Logger

	mu          sync.Mutex
	noiseFloor  int16
	batteryMV   uint16
	mcuTempC    float64
	haveMCUTemp bool
}

func NewKissStatsProvider(modem *hardware.KissModem, radio RadioInfo) *kissStatsProvider {
	p := &kissStatsProvider{
		modem:     modem,
		radio:     radio,
		startTime: time.Now(),
		log:       slog.Default().With("component", "stats", "type", "kiss"),
	}

	modem.OnHwResponse(hardware.HwResp(hardware.HW_CMD_GET_NOISE_FLOOR), p.onNoiseFloor)
	modem.OnHwResponse(hardware.HwResp(hardware.HW_CMD_GET_BATTERY), p.onBattery)
	modem.OnHwResponse(hardware.HwResp(hardware.HW_CMD_GET_MCU_TEMP), p.onMCUTemp)

	return p
}

func (p *kissStatsProvider) LinkStats() LinkStats {
	s := p.modem.Stats()
	return LinkStats{
		InboundDroppedOldest: s.InboundDroppedOldest,
		InboundDroppedNew:    s.InboundDroppedNew,
		RxMetaTimeouts:       s.RxMetaTimeouts,
		RxMetaMisattributed:  s.RxMetaMisattributed,
		HandlerSlow:          s.HandlerSlow,
		HwDecodeErrors:       s.HwDecodeErrors,
		HwErrors:             s.HwErrors,
		TxOutcomeLost:        s.TxOutcomeLost,
	}
}

func (p *kissStatsProvider) RadioConfig() RadioInfo {
	return p.radio
}

func (p *kissStatsProvider) EstAirtimeMs(packetLen int) uint32 {
	if p.radio.BwHz == 0 || p.radio.SF == 0 {
		return 0
	}
	return hardware.LoRaAirtimeEstimator(&hardware.RadioConfig{
		FreqHz: p.radio.FreqHz, BwHz: p.radio.BwHz, SF: p.radio.SF, CR: p.radio.CR,
	})(packetLen)
}

func (p *kissStatsProvider) PacketScore(snrDB float64, packetLen int) float64 {
	return hardware.PacketScore(snrDB, p.radio.SF, packetLen)
}

func (p *kissStatsProvider) Stats(ctx context.Context) DeviceStats {
	if err := p.modem.GetNoiseFloor(); err != nil {
		p.log.Error("get noise floor", "error", err)
	}
	if err := p.modem.GetBattery(); err != nil {
		p.log.Error("get battery", "error", err)
	}
	if err := p.modem.GetMCUTemp(); err != nil {
		p.log.Error("get mcu temp", "error", err)
	}

	// Give the modem a moment to respond.
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	ds := DeviceStats{
		NoiseFloor:  p.noiseFloor,
		BatteryMV:   p.batteryMV,
		UptimeSecs:  uint32(time.Since(p.startTime).Seconds()),
		MCUTempC:    p.mcuTempC,
		HaveMCUTemp: p.haveMCUTemp,
	}
	p.log.Log(ctx, logging.LevelTrace, "stats polled",
		"noise_floor", ds.NoiseFloor, "battery_mv", ds.BatteryMV,
		"mcu_temp_c", ds.MCUTempC, "uptime_secs", ds.UptimeSecs)
	return ds
}

func (p *kissStatsProvider) onNoiseFloor(_ byte, data []byte) {
	if len(data) < 2 {
		return
	}
	p.mu.Lock()
	p.noiseFloor = int16(binary.LittleEndian.Uint16(data[:2]))
	p.mu.Unlock()
}

// onMCUTemp decodes the modem's int16 tenths-of-a-degree reply
// (KissModem::handleGetMCUTemp).
func (p *kissStatsProvider) onMCUTemp(_ byte, data []byte) {
	if len(data) < 2 {
		return
	}
	p.mu.Lock()
	p.mcuTempC = float64(int16(binary.LittleEndian.Uint16(data[:2]))) / 10
	p.haveMCUTemp = true
	p.mu.Unlock()
}

func (p *kissStatsProvider) onBattery(_ byte, data []byte) {
	if len(data) < 2 {
		return
	}
	p.mu.Lock()
	p.batteryMV = binary.LittleEndian.Uint16(data[:2])
	p.mu.Unlock()
}
