package repeater

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// Admin-over-mesh: the repeater answers login / status / CLI requests from
// clients (the official app, other companions, this bot's own repeater client).
// This is the server side of internal/client/repeater — the wire formats here
// mirror what that client builds/parses (see the firmware simple_repeater
// MyMesh.cpp handleLoginReq / handleRequest).

// Permission levels (firmware PERM_ACL_* — lower 2 bits are the role).
const (
	permGuest     = 0x00
	permReadOnly  = 0x01
	permReadWrite = 0x02
	permAdmin     = 0x03
	permRoleMask  = 0x03
)

const (
	respServerLoginOK = 0x00 // login response code
	firmwareVerLevel  = 2    // FIRMWARE_VER_LEVEL advertised in the login reply
	// serverReplyDelay mirrors the firmware SERVER_RESPONSE_DELAY: hold the reply
	// briefly so the (half-duplex) requester has switched back to RX.
	serverReplyDelay = 300 * time.Millisecond
)

// clientRoute is a learned return path to an admin client (the path hashes from
// its flood login, and their per-hop byte width).
type clientRoute struct {
	path     []byte
	hashSize uint8
}

// handleAnonReq answers an ANON_REQ (login). Fires for every ANON_REQ we hear;
// self-filters by destination hash + MAC (a request for another node won't
// decrypt with our shared secret).
func (r *Repeater) handleAnonReq(pkt *meshcore.Packet) {
	anon, err := meshcore.AnonReqFromBytes(pkt.Payload)
	if err != nil {
		return
	}
	me := r.node.Identity()
	if anon.Destination != me.PublicKey()[0] {
		return // not addressed to us
	}

	// The client puts its STATIC pubkey in EphemeralPubKey (so the repeater's
	// ACL recognises it across sessions); ECDH with it yields the session key.
	clientPub := anon.EphemeralPubKey
	secret, err := r.node.SharedSecret(meshcore.NewIdentity(clientPub))
	if err != nil {
		return
	}
	plain := anon.Decrypt(secret)
	if plain == nil || len(plain) < 5 {
		return // MAC failed (not for us) or too short
	}

	// [timestamp:4][password:N] — but a leading control byte (0 < b < ' ') marks
	// an unauthenticated sub-request (regions/owner/clock).
	ts := binary.LittleEndian.Uint32(plain[:4])
	if plain[4] != 0 && plain[4] < ' ' {
		r.handleAnonSubReq(pkt, clientPub, secret, ts, plain[4], plain[5:])
		return
	}
	password := cString(plain[4:])

	perms, ok := r.authLogin(clientPub, password, ts)
	if !ok {
		return // bad password / replay — silent, matching the firmware
	}

	now := uint32(time.Now().Unix())
	resp := make([]byte, 13)
	binary.LittleEndian.PutUint32(resp[:4], now)
	resp[4] = respServerLoginOK
	resp[5] = 0 // legacy keep-alive interval
	if perms&permRoleMask == permAdmin {
		resp[6] = 1
	}
	resp[7] = byte(perms)
	_, _ = rand.Read(resp[8:12]) // blob for packet-hash uniqueness
	resp[12] = firmwareVerLevel

	r.log.Info("admin login", "client", hex.EncodeToString(clientPub[:6]), "perms", perms, "flood", pkt.IsRouteFlood())
	if err := r.sendServerReply(pkt, clientPub, secret, resp); err != nil {
		r.log.Error("login reply failed", "error", err)
	}
}

// Anon ANON_REQ sub-request types (firmware ANON_REQ_TYPE_*): unauthenticated,
// direct-routed only, answered ahead of any login.
const (
	anonReqTypeRegions = 0x01
	anonReqTypeOwner   = 0x02
	anonReqTypeBasic   = 0x03 // our clock + disabled flag
)

// handleAnonSubReq answers the anon sub-requests (firmware handleAnon{Regions,
// Owner,Clock}Req). Reply plaintext = [sender_ts:4][now:4][body] — the sender's
// timestamp is reflected as the tag and our clock rides along for easy clock
// sync. Sent as an encrypted RESPONSE datagram direct along the caller-supplied
// return path (request body = [pathLenByte][path], pathLenByte's upper 2 bits
// being hashSize-1 and lower 6 the hop count).
func (r *Repeater) handleAnonSubReq(pkt *meshcore.Packet, clientPub [32]byte, secret []byte, ts uint32, subType byte, params []byte) {
	if pkt.IsRouteFlood() || !r.anonLimiter.allow() {
		return // firmware answers these on direct requests only
	}
	if len(params) < 1 {
		return
	}
	pathLenByte := params[0]
	hashSize := int(pathLenByte>>6)&3 + 1
	pathLen := int(pathLenByte&63) * hashSize
	if len(params) < 1+pathLen {
		return
	}

	body := make([]byte, 8)
	binary.LittleEndian.PutUint32(body[:4], ts)                        // reflected tag
	binary.LittleEndian.PutUint32(body[4:], uint32(time.Now().Unix())) // our clock
	switch subType {
	case anonReqTypeRegions:
		body = append(body, r.regionsExport()...)
	case anonReqTypeOwner:
		body = append(body, r.cfg.Name+"\n"+r.cfg.OwnerInfo...)
	case anonReqTypeBasic:
		var feat byte
		if r.cfg.IsFwdDisabled() {
			feat |= 0x80 // "is disabled" bit; we have no bridge bits to set
		}
		body = append(body, feat)
	default:
		return
	}

	me := r.node.Identity().PublicKey()
	payload, err := encPacket(secret, func(mac [2]byte, enc []byte) ([]byte, error) {
		return (&meshcore.Response{Destination: clientPub[0], Source: me[0], MAC: mac, EncryptedPayload: enc}).ToBytes()
	}, body)
	if err != nil {
		return
	}
	// pathLenByte is already the wire encoding ((hashSize-1)<<6 | hops).
	if err := r.node.SendPacketDelayed(&meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeResponse, 0),
		PathLength: pathLenByte,
		Path:       params[1 : 1+pathLen],
		Payload:    payload,
	}, node.PrioritySend, serverReplyDelay); err != nil {
		r.log.Error("anon sub-request reply failed", "subType", subType, "error", err)
	}
}

// regionsExport renders the region names for the anon REGIONS sub-request,
// mirroring RegionMap::exportNamesTo(mask=DENY_FLOOD): flood-allowed names,
// comma-separated, "*" first when unscoped flood is allowed.
func (r *Repeater) regionsExport() string {
	var wildcard string
	var names []string
	for _, rg := range r.cfgRegions() {
		if rg.DenyFlood {
			continue
		}
		if rg.Name == config.WildcardRegion {
			wildcard = "*"
			continue
		}
		names = append(names, rg.Name)
	}
	if wildcard != "" {
		names = append([]string{wildcard}, names...)
	}
	return strings.Join(names, ",")
}

// handlePath learns/refreshes an ACL client's return route from an explicit PATH
// packet (firmware onPeerPathRecv): destination hash = us, source hash picks the
// ACL client via MAC verification, decrypted body =
// [pathLenByte][path][extraType][extra...] where the embedded path leads TO that
// client. No reciprocal path is sent (matching the firmware).
func (r *Repeater) handlePath(pkt *meshcore.Packet) {
	p, err := meshcore.PathFromBytes(pkt.Payload)
	if err != nil || p.Destination != r.node.Identity().PublicKey()[0] {
		return
	}
	client, secret, ok := r.aclClient(p.Source, p.VerifyMAC)
	if !ok {
		return // not one of our logged-in clients
	}
	plain := p.Decrypt(secret)
	if len(plain) < 1 {
		return
	}
	hashSize := int(plain[0]>>6)&3 + 1
	n := int(plain[0]&63) * hashSize
	if len(plain) < 1+n {
		return
	}
	pub, err := hex.DecodeString(client.PubKey)
	if err != nil || len(pub) != 32 {
		return
	}
	var clientPub [32]byte
	copy(clientPub[:], pub)
	r.learnRoute(clientPub, plain[1:1+n], uint8(hashSize))
}

// authLogin decides a client's permission from the login password, mirroring
// the firmware: a blank password re-authenticates an existing ACL client
// (keeping its role); otherwise the password must match the admin or guest
// password. Returns the granted permission and whether login is allowed.
func (r *Repeater) authLogin(clientPub [32]byte, password string, ts uint32) (int, bool) {
	pubHex := hex.EncodeToString(clientPub[:])
	existing := r.aclGet(pubHex)

	// Blank password: reauth a known client, keeping its stored role.
	if password == "" && existing != nil {
		return existing.Permissions, true
	}

	var perms int
	switch password {
	case r.cfg.AdminPassword:
		perms = permAdmin
	case r.cfg.GuestPassword:
		perms = permGuest
	default:
		return 0, false
	}

	// Replay guard: a re-login timestamp must strictly advance.
	if existing != nil && ts <= existing.LastTimestamp {
		return 0, false
	}

	r.aclPut(&store.RepeaterACLEntry{PubKey: pubHex, Permissions: perms, LastTimestamp: ts, LastSeen: time.Now()})
	return perms, true
}

// aclClient looks up the ACL entry whose pubkey-prefix byte matches src and
// whose shared secret verifies the packet's MAC, returning it plus the derived
// secret. It's how an authenticated REQ/TXT is tied back to a logged-in client.
// Reads the in-memory cache (this runs on the packet path); ECDH is done
// outside the lock since it's comparatively slow.
func (r *Repeater) aclClient(src byte, verify func(secret []byte) bool) (*store.RepeaterACLEntry, []byte, bool) {
	r.acl.RLock()
	candidates := make([]store.RepeaterACLEntry, 0, len(r.acl.m))
	for _, e := range r.acl.m {
		candidates = append(candidates, *e)
	}
	r.acl.RUnlock()

	for i := range candidates {
		pub, err := hex.DecodeString(candidates[i].PubKey)
		if err != nil || len(pub) != 32 || pub[0] != src {
			continue
		}
		var pubArr [32]byte
		copy(pubArr[:], pub)
		secret, err := r.node.SharedSecret(meshcore.NewIdentity(pubArr))
		if err != nil {
			continue
		}
		if verify(secret) {
			e := candidates[i]
			return &e, secret, true
		}
	}
	return nil, nil, false
}

// touchClient advances a client's replay timestamp + last-seen after a valid
// authenticated request.
func (r *Repeater) touchClient(e *store.RepeaterACLEntry, ts uint32) {
	e.LastTimestamp = ts
	e.LastSeen = time.Now()
	r.aclPut(e)
}

// aclLoad seeds the in-memory ACL cache from the DB (called at construction,
// before handlers register). The cache is the read source for the packet path;
// writes go through aclPut/aclDelete (write-through to the DB).
func (r *Repeater) aclLoad() {
	entries, err := r.store.RepeaterACL.List(context.Background())
	if err != nil {
		r.log.Error("acl load failed", "error", err)
		return
	}
	r.acl.Lock()
	for i := range entries {
		e := entries[i]
		r.acl.m[e.PubKey] = &e
	}
	r.acl.Unlock()
}

// aclGet returns a copy of the cached entry for pubHex, or nil.
func (r *Repeater) aclGet(pubHex string) *store.RepeaterACLEntry {
	r.acl.RLock()
	defer r.acl.RUnlock()
	if e := r.acl.m[pubHex]; e != nil {
		cp := *e
		return &cp
	}
	return nil
}

// aclPut caches an entry (a copy) and persists it asynchronously.
func (r *Repeater) aclPut(e *store.RepeaterACLEntry) {
	cp := *e
	r.acl.Lock()
	r.acl.m[cp.PubKey] = &cp
	r.acl.Unlock()
	r.store.WriteAsync(func() {
		if err := r.store.RepeaterACL.Upsert(context.Background(), &cp); err != nil {
			r.log.Error("acl upsert failed", "error", err)
		}
	})
}

// aclDelete removes an entry from the cache and the DB.
func (r *Repeater) aclDelete(pubHex string) {
	r.acl.Lock()
	delete(r.acl.m, pubHex)
	r.acl.Unlock()
	r.store.WriteAsync(func() {
		if err := r.store.RepeaterACL.Delete(context.Background(), pubHex); err != nil {
			r.log.Error("acl delete failed", "error", err)
		}
	})
}

// ACLEntry is an admin-client ACL row for the API, with the pubkey resolved to a
// display name where known.
type ACLEntry struct {
	PubKey     string `json:"pubkey"`
	Name       string `json:"name"`       // resolved from peers/companions; "" if unknown
	Permission int    `json:"permission"` // 0=guest 1=read-only 2=read-write 3=admin
	LastSeen   int64  `json:"lastSeen"`   // unix seconds
}

// ACLList returns the current admin clients, most-recently-seen first, for the
// Repeater page's access-control view.
func (r *Repeater) ACLList() []ACLEntry {
	r.acl.RLock()
	rows := make([]store.RepeaterACLEntry, 0, len(r.acl.m))
	for _, e := range r.acl.m {
		rows = append(rows, *e)
	}
	r.acl.RUnlock()

	out := make([]ACLEntry, len(rows))
	for i, e := range rows {
		out[i] = ACLEntry{
			PubKey:     e.PubKey,
			Name:       r.resolveName(e.PubKey),
			Permission: e.Permissions,
			LastSeen:   e.LastSeen.Unix(),
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

// RevokeACL removes a client's access (drops it from the cache + DB). Mirrors an
// admin `setperm <pubkey> 0`.
func (r *Repeater) RevokeACL(pubHex string) error {
	r.aclDelete(strings.ToLower(pubHex))
	return nil
}

// resolveName maps a full pubkey hex to a display name via discovered_peers,
// falling back to the companions table (a companion's own pubkey isn't in its
// peers list). Returns "" when unknown.
func (r *Repeater) resolveName(pubHex string) string {
	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		return ""
	}
	if p, err := r.store.Peers.GetByPubKey(context.Background(), pub); err == nil && p != nil && p.Name != "" {
		return p.Name
	}
	if comps, err := r.store.Companions.List(context.Background()); err == nil {
		for _, c := range comps {
			if strings.EqualFold(c.PubKey, pubHex) {
				return c.Name
			}
		}
	}
	return ""
}

// sendServerReply routes an encrypted reply (a login response, or a REQ
// response) back to the client, mirroring the firmware:
//   - flood request  → PATH-return (teaches the client our route) sent flooded.
//     The client's accumulated path is also cached so later direct replies
//     can route straight back.
//   - direct request → RESPONSE datagram sent direct along the cached route, or
//     flooded when we have no route yet (firmware's out_path==UNKNOWN case).
//
// respPlaintext is the decrypted reply body (login: 13 bytes; REQ: the reflected
// [timestamp:4] tag followed by the response data).
func (r *Repeater) sendServerReply(reqPkt *meshcore.Packet, clientPub [32]byte, secret, respPlaintext []byte) error {
	me := r.node.Identity().PublicKey()

	if reqPkt.IsRouteFlood() {
		r.learnRoute(clientPub, reqPkt.Path, reqPkt.PathHashSize())

		// PATH-return payload: [pathLenByte][path][extraType=RESPONSE][resp].
		inner := make([]byte, 0, 1+len(reqPkt.Path)+1+len(respPlaintext))
		inner = append(inner, reqPkt.PathLength)
		inner = append(inner, reqPkt.Path...)
		inner = append(inner, meshcore.PayloadTypeResponse)
		inner = append(inner, respPlaintext...)

		payload, err := encPacket(secret, func(mac [2]byte, enc []byte) ([]byte, error) {
			return (&meshcore.Path{Destination: clientPub[0], Source: me[0], MAC: mac, EncryptedPayload: enc}).ToBytes()
		}, inner)
		if err != nil {
			return err
		}
		out := &meshcore.Packet{
			Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0),
			Payload: payload,
		}
		return r.sendFloodScoped(out, reqPkt, node.PriorityFloodRelay, serverReplyDelay)
	}

	// Direct request → RESPONSE datagram.
	payload, err := encPacket(secret, func(mac [2]byte, enc []byte) ([]byte, error) {
		return (&meshcore.Response{Destination: clientPub[0], Source: me[0], MAC: mac, EncryptedPayload: enc}).ToBytes()
	}, respPlaintext)
	if err != nil {
		return err
	}
	routeType, pathLen, path := r.replyRoute(clientPub)
	out := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeResponse, 0),
		PathLength: pathLen,
		Path:       path,
		Payload:    payload,
	}
	if routeType == meshcore.RouteTypeFlood { // no learned route — flooded fallback gets the request's scope too
		return r.sendFloodScoped(out, reqPkt, node.PrioritySend, serverReplyDelay)
	}
	return r.node.SendPacketDelayed(out, node.PrioritySend, serverReplyDelay)
}

// sendFloodScoped ports MyMesh::sendFloodReply: when the request arrived inside
// a known non-wildcard region, a flooded reply carries that same transport
// scope (code 1; code 2 stays 0 — the firmware's "REVISIT" home-region slot).
// Otherwise it goes out as a plain unscoped flood.
func (r *Repeater) sendFloodScoped(out, reqPkt *meshcore.Packet, priority uint8, delay time.Duration) error {
	rm := r.node.Regions()
	if rg := rm.FindFloodMatch(reqPkt); rg != nil && rg != rm.Wildcard() {
		out.Header = meshcore.MakeHeader(meshcore.RouteTypeTransportFlood, out.PayloadType(), 0)
		out.TransportCode1 = rg.CalcTransportCode(out)
	}
	return r.node.SendPacketDelayed(out, priority, delay)
}

// learnRoute caches a client's return path (from its flood login/request).
func (r *Repeater) learnRoute(clientPub [32]byte, path []byte, hashSize uint8) {
	cp := append([]byte(nil), path...)
	r.routes.Lock()
	r.routes.m[clientPub] = clientRoute{path: cp, hashSize: hashSize}
	r.routes.Unlock()
}

// replyRoute picks the send route for a direct reply to clientPub: direct along
// the cached path when known, otherwise flood (always reaches the client).
func (r *Repeater) replyRoute(clientPub [32]byte) (routeType byte, pathLen uint8, path []byte) {
	r.routes.Lock()
	route, ok := r.routes.m[clientPub]
	r.routes.Unlock()
	if !ok {
		return meshcore.RouteTypeFlood, 0, nil
	}
	if len(route.path) == 0 {
		return meshcore.RouteTypeDirect, 0, nil // direct neighbour, 0 hops
	}
	hashSize := int(route.hashSize)
	if hashSize == 0 {
		hashSize = int(meshcore.PathHashSize)
	}
	return meshcore.RouteTypeDirect, uint8(hashSize-1)<<6 | uint8(len(route.path)/hashSize), route.path
}

// encPacket encrypts plaintext with the session secret, splits the 2-byte MAC
// prefix from the ciphertext, and hands both to build the wire packet payload.
func encPacket(secret []byte, build func(mac [2]byte, enc []byte) ([]byte, error), plaintext []byte) ([]byte, error) {
	encrypted, err := meshcore.EncryptThenMAC(secret, plaintext)
	if err != nil {
		return nil, err
	}
	var mac [2]byte
	copy(mac[:], encrypted[:2])
	return build(mac, encrypted[2:])
}

// cString reads a NUL-terminated string from a (block-padded) plaintext buffer.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
