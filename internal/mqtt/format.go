package mqtt

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/meshcore-go/OwlShack/internal/buildinfo"
	"github.com/meshcore-go/OwlShack/internal/modem"
	meshcore "github.com/meshcore-go/meshcore-go"
)

// rxTimeLayout is an RFC3339-style timestamp with microsecond precision. The
// trailing Z07:00 emits "Z" for UTC, making the value zone-aware. Combined with
// a UTC clock (time.Now().UTC()) this stops downstream observers from treating
// our feed as a naive local-time clock and clamping per-packet rxTime to their
// ingest time. Always format a UTC time with this layout.
const rxTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

type packetMessage struct {
	Timestamp  string `json:"timestamp"`
	OriginID   string `json:"origin_id"`
	Origin     string `json:"origin"`
	Type       string `json:"type"`
	Direction  string `json:"direction"`
	Time       string `json:"time"`
	Date       string `json:"date"`
	Len        string `json:"len"`
	PacketType string `json:"packet_type"`
	Route      string `json:"route"`
	PayloadLen string `json:"payload_len"`
	Raw        string `json:"raw"`
	// SNR, RSSI, score and duration are RX-only, matching meshcoretomqtt: they
	// come from the firmware's log line, which prints them only for a received
	// packet. Publishing them on a TX row would report a measured 0 dB / 0 dBm
	// for our own transmissions, which is worse than saying nothing.
	SNR      string `json:"SNR,omitempty"`
	RSSI     string `json:"RSSI,omitempty"`
	Score    string `json:"score,omitempty"`
	Duration string `json:"duration,omitempty"`
	Hash     string `json:"hash"`
	// Path is the firmware's "[src -> dst]" hash pair, which it logs for the
	// four addressed payload types; the bridge forwards it for direct routes.
	Path string `json:"path,omitempty"`
}

func formatPacket(pkt *meshcore.Packet, rawBytes []byte, originName, originID, direction string, sp modem.StatsProvider) ([]byte, error) {
	now := time.Now().UTC()

	route := "F"
	if pkt.IsRouteDirect() {
		route = "D"
	}

	hash := pkt.PacketHash()

	msg := packetMessage{
		Timestamp:  now.Format(rxTimeLayout),
		OriginID:   originID,
		Origin:     originName,
		Type:       "PACKET",
		Direction:  direction,
		Time:       now.Format("15:04:05"),
		Date:       fmt.Sprintf("%d/%d/%d", now.Day(), int(now.Month()), now.Year()),
		Len:        fmt.Sprintf("%d", len(rawBytes)),
		PacketType: fmt.Sprintf("%d", pkt.PayloadType()),
		Route:      route,
		PayloadLen: fmt.Sprintf("%d", len(pkt.Payload)),
		Raw:        strings.ToUpper(hex.EncodeToString(rawBytes)),
		Hash:       strings.ToUpper(hex.EncodeToString(hash[:])),
	}

	if direction == "rx" {
		// duration and path are derived from the frame itself, so they survive
		// missing signal metadata.
		if sp != nil {
			// Firmware: time=getEstAirtimeFor(len) — derived, not measured, so
			// ours is the same number.
			if ms := sp.EstAirtimeMs(len(rawBytes)); ms > 0 {
				msg.Duration = fmt.Sprintf("%d", ms)
			}
		}
		if pkt.IsRouteDirect() {
			msg.Path = addressedPathLabel(pkt)
		}
		// Signal metadata arrives in a separate KISS frame paired to the data
		// frame; when that pairing fails the modem reports none and
		// rx_meta_timeouts counts it. Publishing 0 dB / 0 dBm then would look
		// like a real reading, and we feed other people's link budgets — so an
		// unmeasured packet carries no measurement, and score goes with them
		// because it is computed FROM the SNR. Never yet observed on this
		// hardware (every rx row in the local log has signal info), which is
		// why it needs a guard and not a comment.
		if pkt.HasSignalInfo {
			// Integer dB, truncated toward zero — byte-faithful to the firmware
			// line meshcoretomqtt scrapes: Dispatcher.cpp prints
			// "(int)pkt->getSNR()" (quarter-dB int8 / 4), and its regex captures
			// SNR=(-?\d+), so a decimal point is a value consumers can't parse.
			msg.SNR = fmt.Sprintf("%d", int(pkt.SNR))
			msg.RSSI = fmt.Sprintf("%d", pkt.RSSI)
			if sp != nil {
				msg.Score = fmt.Sprintf("%d", int(sp.PacketScore(float64(pkt.SNR), len(rawBytes))*1000))
			}
		}
	}

	return json.Marshal(msg)
}

// addressedPathLabel reproduces Dispatcher.cpp's "[%02X -> %02X]" trailer:
// payload[1] then payload[0], and only for the four payload types that carry a
// destination and source hash. Empty for anything else.
func addressedPathLabel(pkt *meshcore.Packet) string {
	switch pkt.PayloadType() {
	case meshcore.PayloadTypePath, meshcore.PayloadTypeReq,
		meshcore.PayloadTypeResponse, meshcore.PayloadTypeTxtMsg:
	default:
		return ""
	}
	if len(pkt.Payload) < 2 {
		return ""
	}
	return fmt.Sprintf("%02X -> %02X", pkt.Payload[1], pkt.Payload[0])
}

// statsBlock is the published status payload. Field names are a shared schema
// with meshcore-bot — change them in both or downstream consumers see two
// dialects.
//
// Sent and QueueLen are PROCESS-wide, not per-node: one RadioMux serves the
// companion, the repeater and the observer, so sent is everything this process
// transmitted, not what this companion sent.
type statsBlock struct {
	UptimeSecs uint32 `json:"uptime_secs"`
	// Published under BOTH names because two real readers disagree and each
	// name has one. recv/sent are the firmware's stats-packets names, also the
	// vocabulary of CoreScope's client-RF topic. But CoreScope's *status*
	// ingest reads packets_sent / packets_recv and accepts no alternative
	// (extractObserverMeta, cmd/ingestor/main.go), so publishing only
	// recv/sent silently drops our packet counters there. The old
	// packets_received was never right either: the key it needed is
	// packets_recv.
	Recv        uint64 `json:"recv"`
	Sent        uint64 `json:"sent"`
	PacketsRecv uint64 `json:"packets_recv"`
	PacketsSent uint64 `json:"packets_sent"`
	FloodRx     uint64 `json:"flood_rx"`
	DirectRx    uint64 `json:"direct_rx"`
	// flood_tx / direct_tx complete the firmware's stats-packets set. Like sent,
	// they are process-wide: every transmission through the shared mux.
	FloodTx    uint64 `json:"flood_tx"`
	DirectTx   uint64 `json:"direct_tx"`
	FloodDups  uint64 `json:"flood_dups"`
	DirectDups uint64 `json:"direct_dups"`
	RecvErrors uint64 `json:"recv_errors"`
	QueueLen   int    `json:"queue_len"`

	// Board readings, named and placed as the firmware's stats-core /
	// stats-radio replies are: meshcoretomqtt forwards those JSON objects
	// verbatim as this block, so a consumer looks for stats.battery_mv, not a
	// percentage of our own invention at the top level. BatteryMV is 0 when the
	// board cannot measure one; MCUTempC is omitted then, keeping "no sensor"
	// distinct from 0 °C.
	BatteryMV  uint16   `json:"battery_mv"`
	MCUTempC   *float64 `json:"mcu_temp_c,omitempty"`
	NoiseFloor int16    `json:"noise_floor"`
	LastRSSI   int16    `json:"last_rssi"`
	LastSNR    float64  `json:"last_snr"`
	// Both accumulate the same per-packet estimate the firmware accumulates into
	// rx_air_time / total_air_time. TX arrives via Observer.NoteTx on the modem's
	// outbound handler, so it counts what this process actually transmitted.
	RxAirSecs uint32 `json:"rx_air_secs"`
	TxAirSecs uint32 `json:"tx_air_secs"`

	// TX outcomes. Busy and queue drops are kept apart on purpose: busy means
	// the radio kept reporting congestion and we gave up (nothing the operator
	// can change locally), queue means we enqueued faster than the radio drains
	// (reduce our own send rate). Summing them yields a number nobody can act on.
	TxRequeued     uint64 `json:"tx_requeued"`
	TxDroppedBusy  uint64 `json:"tx_dropped_busy"`
	TxDroppedQueue uint64 `json:"tx_dropped_queue"`
	TxFailed       uint64 `json:"tx_failed"`
	TxOutcomeLost  uint64 `json:"tx_outcome_lost"`

	// RX / driver faults.
	RxDropped uint64 `json:"rx_dropped"`
	// RxMetaMisattributed means signal metadata was matched to the wrong
	// packet, so any snr/rssi we published for it was wrong. Rare, but it
	// corrupts other people's link budgets, not just our own dashboard.
	RxMetaMisattributed uint64 `json:"rx_meta_misattributed"`
	RxMetaTimeouts      uint64 `json:"rx_meta_timeouts"`
	HwErrors            uint64 `json:"hw_errors"`
	HwDecodeErrors      uint64 `json:"hw_decode_errors"`
	HandlerSlow         uint64 `json:"handler_slow"`
}

// TxCounts is the mux's transmit counters, mirroring node.TxStats plus the
// current queue depth. Process-wide, like statsBlock says.
type TxCounts struct {
	Sent          uint64
	QueueLen      int
	BusyRequeued  uint64
	BusyDropped   uint64
	QueueRejected uint64
	Failed        uint64
}

type statusMessage struct {
	Status          string `json:"status"`
	Timestamp       string `json:"timestamp"`
	Origin          string `json:"origin"`
	OriginID        string `json:"origin_id"`
	Model           string `json:"model"`
	FirmwareVersion string `json:"firmware_version"`
	Radio           string `json:"radio"`
	ClientVersion   string `json:"client_version"`
	// repeat is where firmware 1.16 puts its relay flag and where CoreScope
	// reads it (top level, not inside stats).
	Repeat bool       `json:"repeat"`
	Stats  statsBlock `json:"stats"`
}

// ObserverCounts is what the observer itself tallies: airtime in milliseconds
// for the packets this process sent and received, the TX route split, the last
// signal reading, and whether this node relays.
type ObserverCounts struct {
	RxMs     uint64
	TxMs     uint64
	FloodTx  uint64
	DirectTx uint64
	Relaying bool
	// LastSNR / LastRSSI are the most recent received packet's, mirroring the
	// firmware's stats-radio, which reports the driver's last values.
	LastSNR  float64
	LastRSSI int16
}

type PacketCounts struct {
	Received   uint64
	FloodRx    uint64
	DirectRx   uint64
	FloodDups  uint64
	DirectDups uint64
}

func formatStatus(status, originName, originID string, radio modem.RadioInfo, ds modem.DeviceStats, packets PacketCounts, tx TxCounts, link modem.LinkStats, obs ObserverCounts, recvErrors uint64) ([]byte, error) {
	var radioStr string
	if radio.FreqHz > 0 {
		radioStr = fmt.Sprintf("%.3f,%.1f,%d,%d",
			float64(radio.FreqHz)/1_000_000,
			float64(radio.BwHz)/1_000,
			radio.SF,
			radio.CR,
		)
	}

	var mcuTemp *float64
	if ds.HaveMCUTemp {
		mcuTemp = &ds.MCUTempC
	}

	msg := statusMessage{
		Status:          status,
		Timestamp:       time.Now().UTC().Format(rxTimeLayout),
		Origin:          originName,
		OriginID:        originID,
		Model:           "OwlShack",
		FirmwareVersion: buildinfo.Version,
		Radio:           radioStr,
		ClientVersion:   "OwlShack/" + buildinfo.Version,
		Repeat:          obs.Relaying,
		Stats: statsBlock{
			UptimeSecs:  ds.UptimeSecs,
			Recv:        packets.Received,
			PacketsRecv: packets.Received,
			FloodRx:     packets.FloodRx,
			DirectRx:    packets.DirectRx,
			FloodDups:   packets.FloodDups,
			DirectDups:  packets.DirectDups,
			RecvErrors:  recvErrors,

			BatteryMV:  ds.BatteryMV,
			MCUTempC:   mcuTemp,
			NoiseFloor: ds.NoiseFloor,
			LastSNR:    obs.LastSNR,
			LastRSSI:   obs.LastRSSI,
			RxAirSecs:  uint32(obs.RxMs / 1000),
			TxAirSecs:  uint32(obs.TxMs / 1000),
			FloodTx:    obs.FloodTx,
			DirectTx:   obs.DirectTx,

			Sent:           tx.Sent,
			PacketsSent:    tx.Sent,
			QueueLen:       tx.QueueLen,
			TxRequeued:     tx.BusyRequeued,
			TxDroppedBusy:  tx.BusyDropped,
			TxDroppedQueue: tx.QueueRejected,
			TxFailed:       tx.Failed,
			TxOutcomeLost:  link.TxOutcomeLost,

			RxDropped:           link.InboundDroppedOldest + link.InboundDroppedNew,
			RxMetaMisattributed: link.RxMetaMisattributed,
			RxMetaTimeouts:      link.RxMetaTimeouts,
			HwErrors:            link.HwErrors,
			HwDecodeErrors:      link.HwDecodeErrors,
			HandlerSlow:         link.HandlerSlow,
		},
	}
	return json.Marshal(msg)
}
