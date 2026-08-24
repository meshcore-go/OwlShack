package repeater

import (
	crand "crypto/rand"
	"encoding/binary"
	"math"
	mrand "math/rand/v2"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

// Node discovery (firmware CTL_TYPE_NODE_DISCOVER_REQ/RESP): `discover.neighbors`
// broadcasts a zero-hop control request asking repeater neighbours to announce
// themselves; matching responses are recorded in the neighbour list.

const (
	advTypeRepeater    = 2                                             // firmware ADV_TYPE_REPEATER
	discoverReqFlags   = byte(meshcore.ControlSubTypeDiscoverReq << 4) // 0x80, prefix_only=0
	discoverWindow     = 60 * time.Second                              // collect responses this long (firmware)
	discoverTypeFilter = byte(1 << advTypeRepeater)                    // discover repeaters
)

// sendDiscover broadcasts a zero-hop NODE_DISCOVER_REQ (firmware
// sendNodeDiscoverReq): control data [filter:1][tag:4][since:4], sent direct to
// neighbours only. Responses arriving within discoverWindow with a matching tag
// are added as neighbours by handleControl.
func (r *Repeater) sendDiscover() error {
	var tagB [4]byte
	_, _ = crand.Read(tagB[:])
	tag := binary.LittleEndian.Uint32(tagB[:])

	data := make([]byte, 9)
	data[0] = discoverTypeFilter
	binary.LittleEndian.PutUint32(data[1:5], tag)
	// data[5:9] = since (0)

	r.discover.Lock()
	r.discover.tag = tag
	r.discover.until = time.Now().Add(discoverWindow)
	r.discover.Unlock()

	ctl := meshcore.Control{Flags: discoverReqFlags, Data: data}
	payload, err := ctl.ToBytes()
	if err != nil {
		return err
	}
	// Zero-hop: direct route, no path — reaches direct neighbours only.
	return r.node.SendPacket(&meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeControl, 0),
		Payload: payload,
	})
}

// handleControl answers NODE_DISCOVER_REQs and records discover RESPONSES that
// match our in-flight tag as neighbours (firmware onControlDataRecv).
func (r *Repeater) handleControl(pkt *meshcore.Packet) {
	ctl, err := meshcore.ControlFromBytes(pkt.Payload)
	if err != nil {
		return
	}
	switch ctl.SubType() {
	case meshcore.ControlSubTypeDiscoverReq:
		r.answerDiscover(pkt, ctl)
	case meshcore.ControlSubTypeDiscoverResp:
		r.recordDiscoverResp(ctl)
	}
}

// answerDiscover responds to a NODE_DISCOVER_REQ asking for repeaters (firmware
// REQ branch of onControlDataRecv): gated on relaying being enabled, rate-
// limited 4/2min; the response is zero-hop control data
// [flags=0x90|type][snr x4][tag:4][pubkey] sent after a randomized delay so
// simultaneous responders don't collide.
func (r *Repeater) answerDiscover(pkt *meshcore.Packet, ctl *meshcore.Control) {
	req, err := ctl.DiscoverRequest()
	if err != nil || req.TypeFilter&(1<<advTypeRepeater) == 0 {
		return // not asking about repeaters
	}
	if r.cfg.IsFwdDisabled() || !r.discoverLimiter.allow() {
		return
	}
	// ponytail: firmware's discovery_mod_timestamp freshness gate isn't modelled — we always answer.

	pub := r.node.Identity().PublicKeyBytes()
	data := make([]byte, 5, 5+len(pub))
	if pkt.HasSignalInfo { // let the sender know our inbound SNR (quarter-dB wire form)
		data[0] = byte(int8(math.Round(float64(pkt.SNR) * 4)))
	}
	binary.LittleEndian.PutUint32(data[1:5], req.Tag)
	data = append(data, pub[:]...)
	if req.PrefixOnly {
		data = data[:5+8] // firmware replies with an 8-byte pubkey prefix
	}
	payload, err := (&meshcore.Control{
		Flags: byte(meshcore.ControlSubTypeDiscoverResp<<4 | advTypeRepeater),
		Data:  data,
	}).ToBytes()
	if err != nil {
		return
	}

	// Random retransmit delay, widened ×4 as multiple nodes answer the same
	// request (firmware getRetransmitDelay*4, tx_delay_factor 0.5).
	est := uint32(100) // ms fallback when no ToA estimator is available
	if r.airtime != nil {
		est = r.airtime(len(payload))
	}
	delay := time.Duration(mrand.IntN(int(5*est/2)+1)) * 4 * time.Millisecond
	if err := r.node.SendPacketDelayed(&meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeControl, 0),
		Payload: payload,
	}, node.PrioritySend, delay); err != nil {
		r.log.Error("discover response failed", "error", err)
	}
}

// recordDiscoverResp records a discover RESPONSE as a neighbour when it matches
// our in-flight discover tag (firmware onControlDataRecv → putNeighbour).
func (r *Repeater) recordDiscoverResp(ctl *meshcore.Control) {
	resp, err := ctl.DiscoverResponse()
	if err != nil || len(resp.PubKey) < 32 {
		return
	}

	r.discover.Lock()
	active := r.discover.tag != 0 && resp.Tag == r.discover.tag && time.Now().Before(r.discover.until)
	r.discover.Unlock()
	if !active {
		return
	}

	var pub [32]byte
	copy(pub[:], resp.PubKey)
	if pub == r.node.Identity().Identity.PublicKey() {
		return // don't record ourselves
	}

	r.neighbors.Lock()
	name := ""
	if existing := r.neighbors.m[pub]; existing != nil {
		name = existing.name // keep a name already learned from an advert
	}
	r.neighbors.m[pub] = &neighbor{pubkey: pub, name: name, snr: float64(resp.SNR), heard: time.Now()}
	r.neighbors.Unlock()
}
