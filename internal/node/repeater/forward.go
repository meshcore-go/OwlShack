package repeater

import meshcore "github.com/meshcore-go/meshcore-go"

// Loop-detect thresholds indexed by path hash size (1..3 bytes), mirroring the
// firmware's max_loop_{minimal,moderate,strict} tables. Index 0 is unused (hash
// size is never 0). Value N means: drop if our own hash already appears >= N
// times in the path (i.e. we've relayed this flood N times already).
var (
	maxLoopMinimal  = [4]int{0, 4, 2, 1}
	maxLoopModerate = [4]int{0, 2, 1, 1}
	maxLoopStrict   = [4]int{0, 1, 1, 1}
)

// allowForward is the repeater's relay policy — the Go port of the firmware's
// MyMesh::allowPacketForward. meshcore-go's router calls it before relaying a
// flood or direct packet; returning false makes the router deliver locally
// without re-transmitting. The router already handles dedup and path-size
// overflow, so this only layers on the repeater-specific policy.
func (r *Repeater) allowForward(pkt *meshcore.Packet) bool {
	if r.cfg.IsFwdDisabled() {
		return false
	}

	if pkt.IsRouteFlood() {
		// Region gating: the port of filterRecvFloodPacket + the firmware's
		// `if (isRouteFlood() && recv_pkt_region == NULL) return false`. With
		// only the default wildcard region this passes plain FLOOD and drops
		// scoped/transport FLOOD we have no transport key for.
		if r.node.Regions().FindFloodMatch(pkt) == nil {
			return false
		}

		// Hop caps, in the firmware's order: the general cap, then the extra
		// unscoped cap (plain ROUTE_TYPE_FLOOD only — transport-scoped floods
		// bypass it), then the advert-specific cap.
		hops := int(pkt.PathHashCount())
		if hops >= r.cfg.FloodMaxOr() {
			return false
		}
		if pkt.RouteType() == meshcore.RouteTypeFlood && hops >= r.cfg.FloodMaxUnscopedOr() {
			return false
		}
		if pkt.PayloadType() == meshcore.PayloadTypeAdvert && hops >= r.cfg.FloodMaxAdvertOr() {
			return false
		}

		if r.isLooped(pkt) {
			return false
		}
	}

	// Accumulate estimated TX airtime for the relay we're about to do (the bulk
	// of a repeater's transmissions; our own adverts/replies are negligible).
	// Wire size ~ header + pathLen byte + path + payload.
	if r.airtime != nil {
		r.txAirtimeMs.Add(uint64(r.airtime(2 + len(pkt.Path) + len(pkt.Payload))))
	}
	r.fwdCount.Add(1)
	return true
}

// loopThreshold returns the max number of times our own hash may appear in a
// path of the given hash size before the packet counts as looped, for a given
// loop-detect level. 0 means "no check" (level off, or an out-of-range size).
func loopThreshold(level string, hashSize int) int {
	if hashSize < 1 || hashSize > 3 {
		return 0
	}
	switch level {
	case "minimal":
		return maxLoopMinimal[hashSize]
	case "moderate":
		return maxLoopModerate[hashSize]
	case "strict":
		return maxLoopStrict[hashSize]
	default: // "off"
		return 0
	}
}

// isLooped ports MyMesh::isLooped: counts how many times our own path hash
// already appears in the packet's path and compares against the configured
// loop-detect threshold for the packet's hash size.
func (r *Repeater) isLooped(pkt *meshcore.Packet) bool {
	threshold := loopThreshold(r.cfg.LoopDetectOr(), int(pkt.PathHashSize()))
	if threshold == 0 {
		return false
	}

	id := r.node.Identity()
	n := 0
	for _, h := range pkt.PathHashes() {
		if id.IsHashMatch(h) {
			n++
		}
	}
	return n >= threshold
}
