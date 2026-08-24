package repeater

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"sort"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/meshcore-bot/internal/buildinfo"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// Admin request types (firmware REQ_TYPE_*), mirroring internal/client/repeater.
const (
	reqTypeGetStatus        = 0x01
	reqTypeGetTelemetryData = 0x03
	reqTypeGetAccessList    = 0x05
	reqTypeGetNeighbours    = 0x06
	reqTypeGetOwnerInfo     = 0x07
)

// handleReq answers a REQ (status / neighbours / access-list / owner-info) from
// a logged-in client. Fires for every REQ we hear; self-filters by destination
// hash + MAC-against-ACL.
func (r *Repeater) handleReq(pkt *meshcore.Packet) {
	req, err := meshcore.RequestFromBytes(pkt.Payload)
	if err != nil {
		return
	}
	if req.Destination != r.node.Identity().PublicKey()[0] {
		return
	}
	client, secret, ok := r.aclClient(req.Source, req.VerifyMAC)
	if !ok {
		return
	}
	plain := req.Decrypt(secret)
	if plain == nil || len(plain) < 5 {
		return
	}
	tag := binary.LittleEndian.Uint32(plain[:4]) // client timestamp, reflected back
	if tag < client.LastTimestamp {
		return // stale/replayed (all REQs are read-only, so this only advances the guard)
	}
	reqType := plain[4]
	params := plain[5:]

	body, ok := r.buildReqResponse(client, reqType, params)
	if !ok {
		return
	}

	// Response plaintext: [reflected tag:4][body]. The client matches the reply
	// to its pending request by this echoed tag.
	reply := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(reply[:4], tag)
	copy(reply[4:], body)

	r.touchClient(client, tag)

	var clientPub [32]byte
	if pub, err := hex.DecodeString(client.PubKey); err == nil && len(pub) == 32 {
		copy(clientPub[:], pub)
	}
	if err := r.sendServerReply(pkt, clientPub, secret, reply); err != nil {
		r.log.Error("request reply failed", "reqType", reqType, "error", err)
	}
}

// buildReqResponse builds the response body for a request type (everything after
// the reflected timestamp tag). Returns false to answer nothing.
func (r *Repeater) buildReqResponse(client *store.RepeaterACLEntry, reqType byte, params []byte) ([]byte, bool) {
	switch reqType {
	case reqTypeGetStatus:
		return r.statusBody(), true
	case reqTypeGetNeighbours:
		return r.neighboursBody(params), true
	case reqTypeGetOwnerInfo:
		return r.ownerInfoBody(), true
	case reqTypeGetAccessList:
		if client.Permissions&permRoleMask != permAdmin {
			return nil, false // admin-only
		}
		return r.accessListBody(), true
	case reqTypeGetTelemetryData:
		// Base telemetry: battery voltage on the self channel (ch1), from the
		// modem's reading (0 until the first poll / if the radio has no battery).
		enc := meshcore.NewLPPEncoder()
		enc.AddVoltage(1, float64(r.batteryMV.Load())/1000)
		return enc.Bytes(), true
	default:
		return nil, false
	}
}

// statusBody builds the RepeaterStats blob (firmware layout, little-endian).
// Emits the full 56-byte form (incl. rx_air_time + recv_errors, which the
// client parses when present). Counters we don't track are left zero.
func (r *Repeater) statusBody() []byte {
	r.mu.Lock()
	started := r.startedAt
	r.mu.Unlock()
	uptime := uint32(0)
	if !started.IsZero() {
		uptime = uint32(time.Since(started).Seconds())
	}

	b := make([]byte, 56)
	if r.haveDeviceStats.Load() { // real modem readings (poll cache)
		binary.LittleEndian.PutUint16(b[0:2], uint16(r.batteryMV.Load()))         // batt_milli_volts
		binary.LittleEndian.PutUint16(b[4:6], uint16(int16(r.noiseFloor.Load()))) // noise_floor (radio getNoiseFloor)
	}
	binary.LittleEndian.PutUint16(b[2:4], uint16(r.node.TxQueueLen())) // curr_tx_queue_len
	if r.haveSignal.Load() {
		binary.LittleEndian.PutUint16(b[6:8], uint16(int16(r.lastRSSI.Load())))    // last_rssi
		binary.LittleEndian.PutUint16(b[42:44], uint16(int16(r.lastSNRx4.Load()))) // last_snr (quarter-dB)
	}
	binary.LittleEndian.PutUint32(b[8:12], uint32(r.recvCount.Load()))         // n_packets_recv
	binary.LittleEndian.PutUint32(b[12:16], uint32(r.fwdCount.Load()))         // n_packets_sent (relayed)
	binary.LittleEndian.PutUint32(b[16:20], uint32(r.txAirtimeMs.Load()/1000)) // total_air_time_secs (tx)
	binary.LittleEndian.PutUint32(b[20:24], uptime)                            // total_up_time_secs
	binary.LittleEndian.PutUint32(b[48:52], uint32(r.rxAirtimeMs.Load()/1000)) // rx_air_time_secs
	return b
}

// neighboursBody builds the neighbours response: [total:2][results:2] then
// [prefix:prefixLen][secsAgo:4][snr:i8] entries, ordered per the request's
// order_by selector (newest first by default).
func (r *Repeater) neighboursBody(params []byte) []byte {
	// params: [version:1][count:1][offset:2][order_by:1][prefix_len:1][rand:4]
	count := 255
	offset := 0
	prefixLen := 6
	var orderBy byte
	if len(params) >= 6 {
		count = int(params[1])
		offset = int(binary.LittleEndian.Uint16(params[2:4]))
		orderBy = params[4]
		if p := int(params[5]); p >= 1 && p <= 32 {
			prefixLen = p
		}
	}

	now := time.Now()
	list := r.snapshotNeighbors() // newest-first native order
	sortNeighbours(list, orderBy)

	total := len(list)
	if offset > total {
		offset = total
	}
	page := list[offset:]
	if count < len(page) {
		page = page[:count]
	}

	body := make([]byte, 4, 4+len(page)*(prefixLen+5))
	binary.LittleEndian.PutUint16(body[0:2], uint16(total))
	binary.LittleEndian.PutUint16(body[2:4], uint16(len(page)))
	for _, n := range page {
		body = append(body, n.pubkey[:prefixLen]...)
		var secs [4]byte
		binary.LittleEndian.PutUint32(secs[:], uint32(now.Sub(n.heard).Seconds()))
		body = append(body, secs[:]...)
		body = append(body, byte(int8(math.Round(n.snr*4)))) // firmware SNR is quarter-dB
	}
	return body
}

// sortNeighbours orders a neighbour list per the request's order_by selector
// (firmware REQ_TYPE_GET_NEIGHBOURS): 0=newest first (the snapshot's native
// order), 1=oldest first, 2=strongest first, 3=weakest first.
func sortNeighbours(list []neighbor, orderBy byte) {
	switch orderBy {
	case 1:
		sort.Slice(list, func(i, j int) bool { return list[i].heard.Before(list[j].heard) })
	case 2:
		sort.Slice(list, func(i, j int) bool { return list[i].snr > list[j].snr })
	case 3:
		sort.Slice(list, func(i, j int) bool { return list[i].snr < list[j].snr })
	}
}

// accessListBody builds the ACL response: [pubkey-prefix:6][permissions:1] per
// non-guest client (firmware returns 6-byte prefixes only).
func (r *Repeater) accessListBody() []byte {
	entries, err := r.store.RepeaterACL.List(context.Background())
	if err != nil {
		r.log.Error("acl list failed", "error", err)
		return nil
	}
	body := make([]byte, 0, len(entries)*7)
	for _, e := range entries {
		if e.Permissions == 0 {
			continue // guest / deleted
		}
		pub, err := hex.DecodeString(e.PubKey)
		if err != nil || len(pub) < 6 {
			continue
		}
		body = append(body, pub[:6]...)
		body = append(body, byte(e.Permissions))
	}
	return body
}

// ownerInfoBody builds the owner-info response: "VERSION\nNODE_NAME\nOWNER_INFO".
func (r *Repeater) ownerInfoBody() []byte {
	return []byte(buildinfo.Version + "\n" + r.cfg.Name + "\n" + r.cfg.OwnerInfo)
}
