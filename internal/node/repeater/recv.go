package repeater

import (
	"encoding/hex"
	"math"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/store"
)

// registerHandlers wires the radio raw-packet counter and the packet handlers.
// A repeater intentionally handles far fewer payload types than a companion:
// it has no channels, DMs or triggers — just adverts (neighbours) and the
// admin surface (login, requests, CLI).
func (r *Repeater) registerHandlers() {
	// Count every raw packet, capture last signal, and accumulate RX airtime —
	// all surfaced in the over-mesh STATUS response.
	r.radio.SetRawDataHandler(func(data []byte, snr float32, rssi int8, hasSignal bool) {
		r.recvCount.Add(1)
		if hasSignal {
			r.lastRSSI.Store(int32(rssi))
			r.lastSNRx4.Store(int32(math.Round(float64(snr) * 4))) // firmware quarter-dB
			r.haveSignal.Store(true)
		}
		if r.airtime != nil {
			r.rxAirtimeMs.Add(uint64(r.airtime(len(data))))
		}
		if r.logging.Load() { // `log start` — per-packet trace
			r.log.Info("pkt rx", "len", len(data), "snr", snr, "rssi", rssi)
		}
	})

	r.node.OnPacket(meshcore.PayloadTypeAdvert, r.handleAdvert)
	r.node.OnPacket(meshcore.PayloadTypeAnonReq, r.handleAnonReq) // login
	r.node.OnPacket(meshcore.PayloadTypeReq, r.handleReq)         // status/neighbours/acl/owner
	r.node.OnPacket(meshcore.PayloadTypeTxtMsg, r.handleCLI)      // get/set CLI
	r.node.OnPacket(meshcore.PayloadTypePath, r.handlePath)       // learn admin clients' return routes
	r.node.OnPacket(meshcore.PayloadTypeControl, r.handleControl) // discover req/resp
}

// handleAdvert records RF neighbours and persists discovered peers. A companion
// running alongside also stores adverts, but a repeater-only deployment has no
// companion, so the repeater keeps discovered_peers (and thus the Map / Peers
// pages) populated on its own. Upsert is idempotent, so the overlap is harmless.
func (r *Repeater) handleAdvert(pkt *meshcore.Packet) {
	adv, err := meshcore.AdvertFromBytes(pkt.Payload)
	if err != nil || !adv.Verify() {
		return
	}
	appData := adv.AppData()

	// Neighbour tracking (firmware onAdvertRecv): a zero-hop REPEATER advert is
	// a direct RF neighbour. Kept in memory, matching the firmware.
	if pkt.PathHashCount() == 0 && appData.Type == "REPEATER" {
		pub := adv.PublicKey.PublicKey()                   // [32]byte
		if pub != r.node.Identity().Identity.PublicKey() { // don't record ourselves
			snr := 0.0
			if pkt.HasSignalInfo {
				snr = float64(pkt.SNR)
			}
			r.neighbors.Lock()
			r.neighbors.m[pub] = &neighbor{pubkey: pub, name: appData.Name, snr: snr, heard: time.Now()}
			r.neighbors.Unlock()
		}
	}

	p := &store.Peer{
		PubKey:          adv.PublicKey.PublicKeyBytes(),
		Name:            appData.Name,
		Type:            appData.Type,
		Lat:             appData.Lat,
		Lon:             appData.Lon,
		Feat1:           appData.Feat1,
		Feat2:           appData.Feat2,
		OutPath:         pkt.Path,
		OutPathHashSize: pkt.PathHashSize(),
		LastAdvertTS:    adv.Timestamp,
		LastSeen:        time.Now(),
	}
	if pkt.HasSignalInfo {
		snr := float64(pkt.SNR)
		p.SNR = &snr
		p.RSSI = &pkt.RSSI
	}

	r.store.WriteAsync(func() {
		if err := r.store.Peers.Upsert(r.runCtx, p); err != nil {
			r.log.Error("failed to persist peer", "error", err)
			return
		}
		if r.hub != nil {
			r.hub.Broadcast("peers", map[string]any{
				"pubkey":          hex.EncodeToString(p.PubKey[:]),
				"name":            p.Name,
				"type":            p.Type,
				"lat":             p.Lat,
				"lon":             p.Lon,
				"snr":             p.SNR,
				"rssi":            p.RSSI,
				"outPath":         hex.EncodeToString(p.OutPath),
				"outPathHashSize": p.OutPathHashSize,
				"lastAdvertTs":    p.LastAdvertTS,
				"lastSeen":        p.LastSeen.Format(time.RFC3339),
			})
		}
	})
}
