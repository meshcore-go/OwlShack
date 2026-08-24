package repeater

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/buildinfo"
	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/store"
)

const (
	txtTypePlain   = 0 // legacy plain text
	txtTypeCliData = 1 // CLI command / response
	cliReplyDelay  = 600 * time.Millisecond
	// txtAckDelay mirrors the firmware TXT_ACK_DELAY for legacy plain-text CLI.
	txtAckDelay = 200 * time.Millisecond
	// cliAdvertDelay matches the firmware's sendSelfAdvertisement(1500, ...) for
	// CLI-triggered adverts: hold the advert so the CLI reply (queued at
	// cliReplyDelay) transmits first.
	cliAdvertDelay = 1500 * time.Millisecond
)

// handleCLI answers a CLI command (get/set/setperm/advert...) from a logged-in
// ADMIN client over TXT_MSG. Mirrors the firmware onPeerDataRecv TXT_MSG path.
func (r *Repeater) handleCLI(pkt *meshcore.Packet) {
	txt, err := meshcore.TextMessageFromBytes(pkt.Payload)
	if err != nil {
		return
	}
	if txt.Destination != r.node.Identity().PublicKey()[0] {
		return
	}
	client, secret, ok := r.aclClient(txt.Source, txt.VerifyMAC)
	if !ok {
		return
	}
	if client.Permissions&permRoleMask != permAdmin {
		return // CLI is admin-only
	}
	plain := txt.Decrypt(secret)
	if plain == nil || len(plain) < 5 {
		return
	}
	ts := binary.LittleEndian.Uint32(plain[:4])
	if ts < client.LastTimestamp {
		return // stale / replay
	}
	isRetry := ts == client.LastTimestamp
	flags := plain[4] >> 2
	if flags != txtTypeCliData && flags != txtTypePlain {
		return
	}

	var clientPub [32]byte
	if pub, err := hex.DecodeString(client.PubKey); err == nil && len(pub) == 32 {
		copy(clientPub[:], pub)
	}
	r.touchClient(client, ts)
	if pkt.IsRouteFlood() {
		r.learnRoute(clientPub, pkt.Path, pkt.PathHashSize())
	}

	command := cString(plain[5:])
	if flags == txtTypePlain { // legacy CLI gets an ack (firmware TXT_TYPE_PLAIN branch), even on retries
		var attempt byte
		if len(plain) > 5+len(command)+1 { // attempt byte is the padding byte after the text
			attempt = plain[5+len(command)+1]
		}
		r.sendLegacyAck(pkt, clientPub, plain[:5+len(command)], attempt)
	}
	if isRetry {
		return // duplicate delivery — don't re-run side effects
	}

	reply := r.runCLI(command)
	if reply == "" {
		return
	}
	if err := r.sendText(pkt, clientPub, secret, reply); err != nil {
		r.log.Error("cli reply failed", "error", err)
	}
}

// sendLegacyAck ACKs a legacy plain-text CLI command (firmware TXT_TYPE_PLAIN
// branch of onPeerDataRecv): CRC over [timestamp][flags][text] + sender pubkey,
// sent along the learned route (flooded — with the request's scope — when
// unknown) after TXT_ACK_DELAY.
func (r *Repeater) sendLegacyAck(reqPkt *meshcore.Packet, clientPub [32]byte, ackPlaintext []byte, attempt byte) {
	var rnd [1]byte
	_, _ = crand.Read(rnd[:])
	payload := meshcore.BuildAckPayload(ackPlaintext, clientPub[:], attempt, rnd[0])

	routeType, pathLen, path := r.replyRoute(clientPub)
	out := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeAck, 0),
		PathLength: pathLen,
		Path:       path,
		Payload:    payload,
	}
	var err error
	if routeType == meshcore.RouteTypeFlood {
		err = r.sendFloodScoped(out, reqPkt, node.PrioritySend, txtAckDelay)
	} else {
		err = r.node.SendPacketDelayed(out, node.PrioritySend, txtAckDelay)
	}
	if err != nil {
		r.log.Error("legacy cli ack failed", "error", err)
	}
}

// runCLI strips the optional "XX|" correlation prefix (reflected back on the
// reply so async clients can match responses), then dispatches the command.
func (r *Repeater) runCLI(command string) string {
	command = strings.TrimLeft(command, " ")
	prefix := ""
	if len(command) > 4 && command[2] == '|' {
		prefix = command[:3]
		command = command[3:]
	}
	return prefix + r.dispatchCLI(command)
}

// dispatchCLI runs one command and returns its reply text (firmware `get`
// replies are prefixed with "> "). Config-mutating commands (set/password/
// region writes) persist + reload asynchronously via r.reconfigure; the reply
// is optimistic (the value is validated inline first).
func (r *Repeater) dispatchCLI(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	switch {
	case cmd == "ver":
		return buildinfo.Version
	case cmd == "clock":
		return fmt.Sprintf("%d", time.Now().Unix())
	case strings.HasPrefix(cmd, "clock sync"):
		return "OK" // we keep host time; nothing to set
	case cmd == "advert.zerohop":
		r.advertAfterReply(false)
		return "OK - zerohop advert sent"
	case cmd == "advert":
		r.advertAfterReply(true)
		return "OK - Advert sent"
	case cmd == "discover.neighbors":
		if err := r.sendDiscover(); err != nil {
			return "ERR: " + err.Error()
		}
		return "OK - Discover sent"
	case cmd == "neighbors":
		return r.neighborsList()
	case cmd == "clear stats":
		r.clearStats()
		return "(OK - stats reset)"
	case cmd == "log start":
		r.logging.Store(true)
		return "   logging on"
	case cmd == "log stop":
		r.logging.Store(false)
		return "   logging off"
	case cmd == "log erase":
		return "   log erased" // no packet-log file on this node; nothing to erase
	case strings.HasPrefix(cmd, "setperm "):
		return r.cliSetPerm(strings.TrimSpace(cmd[len("setperm "):]))
	case strings.HasPrefix(cmd, "password "):
		return r.cliPassword(strings.TrimSpace(cmd[len("password "):]))
	case cmd == "region" || strings.HasPrefix(cmd, "region "):
		return r.cliRegion(strings.TrimSpace(strings.TrimPrefix(cmd, "region")))
	case strings.HasPrefix(cmd, "get "):
		return r.cliGet(strings.TrimSpace(cmd[len("get "):]))
	case strings.HasPrefix(cmd, "set radio"), strings.HasPrefix(cmd, "set freq"), strings.HasPrefix(cmd, "set tx"):
		return "ERR: radio settings are read-only over the mesh"
	case strings.HasPrefix(cmd, "set "):
		return r.cliSet(strings.TrimSpace(cmd[len("set "):]))
	case isUnsupportedCmd(cmd):
		return "ERR: not supported on this node"
	default:
		return "ERR: unknown command"
	}
}

// isUnsupportedCmd reports firmware commands that don't apply to a Linux-hosted
// Go relay (power/board/GPS/sensors/firmware-flash/chip-specific) — answered
// with a clear error rather than "unknown command".
func isUnsupportedCmd(cmd string) bool {
	for _, p := range []string{
		"poweroff", "shutdown", "reboot", "clkreboot", "board",
		"powersaving", "gps", "sensor", "start ota", "tempradio",
		"erase", "time", "log",
	} {
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}

// cliGet reads a config value. Radio params come from the shared Settings (the
// repeater shares the modem); the rest from the repeater's own config.
func (r *Repeater) cliGet(key string) string {
	switch key {
	case "name":
		return "> " + r.cfg.Name
	case "lat":
		return "> " + floatOrZero(r.cfg.Latitude)
	case "lon":
		return "> " + floatOrZero(r.cfg.Longitude)
	case "owner.info":
		return "> " + strings.ReplaceAll(r.cfg.OwnerInfo, "\n", "|")
	case "repeat":
		if r.cfg.IsFwdDisabled() {
			return "> off"
		}
		return "> on"
	case "loop.detect":
		return "> " + r.cfg.LoopDetectOr()
	case "path.hash.mode":
		return fmt.Sprintf("> %d", r.cfg.PathHashModeOr())
	case "flood.max":
		return fmt.Sprintf("> %d", r.cfg.FloodMaxOr())
	case "flood.max.advert":
		return fmt.Sprintf("> %d", r.cfg.FloodMaxAdvertOr())
	case "flood.max.unscoped":
		return fmt.Sprintf("> %d", r.cfg.FloodMaxUnscopedOr())
	case "guest.password":
		return "> " + r.cfg.GuestPassword
	case "public.key":
		return "> " + hex.EncodeToString(r.node.Identity().PublicKeyBytes())
	case "role":
		return "> repeater"
	case "advert.interval": // minutes (firmware unit); config stores seconds
		return fmt.Sprintf("> %d", (r.cfg.AdvertIntervalOr()+30)/60) // effective (incl. default), rounded
	case "flood.advert.interval": // hours (firmware unit)
		return fmt.Sprintf("> %d", (r.cfg.FloodAdvertIntervalOr()+1800)/3600) // effective (incl. default), rounded
	case "radio", "tx", "freq":
		return r.cliGetRadio(key)
	default:
		return "ERR: unknown key"
	}
}

func (r *Repeater) cliGetRadio(key string) string {
	s, err := r.store.Settings.Get(context.Background())
	if err != nil {
		return "ERR: no radio settings"
	}
	switch key {
	case "freq":
		return "> " + floatOrZero(s.Freq)
	case "tx":
		return fmt.Sprintf("> %d", intPtrOr(s.TX, 0))
	default: // radio: freq,bw,sf,cr
		return fmt.Sprintf("> %s,%s,%d,%d", floatOrZero(s.Freq), floatOrZero(s.BW), intPtrOr(s.SF, 0), intPtrOr(s.CR, 0))
	}
}

// cliSetPerm handles `setperm <pubkey-hex> <perms-int>` (admin-only; perms 0
// removes the client). Persists to the ACL asynchronously.
func (r *Repeater) cliSetPerm(args string) string {
	sp := strings.IndexByte(args, ' ')
	if sp < 0 {
		return "Err - bad params"
	}
	pubHex := strings.TrimSpace(args[:sp])
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != 32 {
		return "Err - bad pubkey"
	}
	perms, err := strconv.Atoi(strings.TrimSpace(args[sp+1:]))
	if err != nil || perms < 0 || perms > permAdmin {
		return "Err - bad perms"
	}
	pubHex = strings.ToLower(pubHex)
	if perms == 0 { // perms 0 removes the client (firmware semantics)
		r.aclDelete(pubHex)
		return "OK"
	}
	e := r.aclGet(pubHex) // preserve an existing client's replay timestamp
	if e == nil {
		e = &store.RepeaterACLEntry{PubKey: pubHex}
	}
	e.Permissions = perms
	e.LastSeen = time.Now()
	r.aclPut(e)
	return "OK"
}

// advertAfterReply sends a CLI-triggered self-advert after cliAdvertDelay so
// the CLI reply (queued at cliReplyDelay) transmits first — matching the
// firmware's sendSelfAdvertisement(1500, ...).
func (r *Repeater) advertAfterReply(flood bool) {
	go func() {
		t := time.NewTimer(cliAdvertDelay)
		defer t.Stop()
		select {
		case <-r.runCtx.Done():
		case <-t.C:
			if err := r.SendAdvert(flood); err != nil {
				r.log.Error("cli advert failed", "error", err)
			}
		}
	}()
}

// sendText sends a CLI reply as a TXT_MSG datagram (never a PATH-return, per
// firmware), direct along the learned route or flooded — with the request's
// scope — when unknown.
func (r *Repeater) sendText(reqPkt *meshcore.Packet, clientPub [32]byte, secret []byte, text string) error {
	me := r.node.Identity().PublicKey()
	plaintext := meshcore.BuildTextPlaintext(time.Now(), txtTypeCliData<<2, []byte(text))
	payload, err := encPacket(secret, func(mac [2]byte, enc []byte) ([]byte, error) {
		return (&meshcore.TextMessage{Destination: clientPub[0], Source: me[0], MAC: mac, EncryptedPayload: enc}).ToBytes()
	}, plaintext)
	if err != nil {
		return err
	}
	routeType, pathLen, path := r.replyRoute(clientPub)
	out := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeTxtMsg, 0),
		PathLength: pathLen,
		Path:       path,
		Payload:    payload,
	}
	if routeType == meshcore.RouteTypeFlood {
		return r.sendFloodScoped(out, reqPkt, node.PrioritySend, cliReplyDelay)
	}
	return r.node.SendPacketDelayed(out, node.PrioritySend, cliReplyDelay)
}

// cliSet handles `set <key> <value>` for the repeater's own config. The value
// is validated inline (firmware-style errors); on success the change is
// persisted + reloaded asynchronously via reconfigure.
func (r *Repeater) cliSet(kv string) string {
	key, val, ok := strings.Cut(kv, " ")
	if !ok {
		return "ERR: usage: set <key> <value>"
	}
	mutate, reply := r.setMutation(key, strings.TrimSpace(val))
	if mutate == nil {
		return reply // validation error / unsupported key
	}
	return r.applyCfg(mutate, reply)
}

// setMutation validates a `set` value and returns the config mutation + success
// reply, or (nil, errorReply). Mirrors the firmware CommonCLI set-branch units,
// ranges and messages.
func (r *Repeater) setMutation(key, val string) (func(*config.RepeaterConfig), string) {
	switch key {
	case "name":
		if !isValidRepeaterName(val) {
			return nil, "Error, bad chars"
		}
		return func(c *config.RepeaterConfig) { c.Name = val }, "OK"
	case "lat":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, "Error, bad value"
		}
		return func(c *config.RepeaterConfig) { c.Latitude = &f }, "OK"
	case "lon":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, "Error, bad value"
		}
		return func(c *config.RepeaterConfig) { c.Longitude = &f }, "OK"
	case "advert.interval": // minutes; 0 (off) or 60-240 (firmware range)
		m, err := strconv.Atoi(val)
		if err != nil {
			return nil, "Error, bad value"
		}
		if m != 0 && (m < 60 || m > 240) {
			return nil, "Error: interval range is 60-240 minutes"
		}
		secs := m * 60
		return func(c *config.RepeaterConfig) { c.AdvertInterval = &secs }, "OK"
	case "flood.advert.interval": // hours; 0 (off) or 3-168
		h, err := strconv.Atoi(val)
		if err != nil {
			return nil, "Error, bad value"
		}
		if h != 0 && (h < 3 || h > 168) {
			return nil, "Error: interval range is 3-168 hours"
		}
		secs := h * 3600
		return func(c *config.RepeaterConfig) { c.FloodAdvertInterval = &secs }, "OK"
	case "flood.max", "flood.max.advert", "flood.max.unscoped":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return nil, "Error, bad value"
		}
		if n > 64 {
			return nil, "Error, max 64"
		}
		switch key {
		case "flood.max":
			return func(c *config.RepeaterConfig) { c.FloodMax = &n }, "OK"
		case "flood.max.advert":
			return func(c *config.RepeaterConfig) { c.FloodMaxAdvert = &n }, "OK"
		default:
			return func(c *config.RepeaterConfig) { c.FloodMaxUnscoped = &n }, "OK"
		}
	case "path.hash.mode":
		n, err := strconv.Atoi(val)
		if err != nil || (n != 0 && n != 1 && n != 3) {
			return nil, "Error, must be 0, 1 or 3"
		}
		return func(c *config.RepeaterConfig) { c.PathHashMode = &n }, "OK"
	case "loop.detect":
		if !config.IsValidLoopDetect(val) {
			return nil, "Error, must be: off, minimal, moderate, or strict"
		}
		v := val
		return func(c *config.RepeaterConfig) { c.LoopDetect = &v }, "OK"
	case "repeat":
		if val != "on" && val != "off" {
			return nil, "Error, must be on or off"
		}
		disable := val == "off"
		msg := "OK - repeat is now ON"
		if disable {
			msg = "OK - repeat is now OFF"
		}
		return func(c *config.RepeaterConfig) { c.DisableFwd = &disable }, msg
	case "owner.info":
		info := strings.ReplaceAll(val, "|", "\n") // firmware stores | as newline
		return func(c *config.RepeaterConfig) { c.OwnerInfo = info }, "OK"
	case "guest.password":
		return func(c *config.RepeaterConfig) { c.GuestPassword = val }, "OK"
	default:
		return nil, "ERR: not supported on this node"
	}
}

// cliPassword handles `password <new>` — change the admin password (firmware
// reply echoes the new password). Persisted + reloaded via reconfigure.
func (r *Repeater) cliPassword(pass string) string {
	return r.applyCfg(func(c *config.RepeaterConfig) { c.AdminPassword = pass }, "password now: "+pass)
}

// cliRegion handles the region subcommands we model (a flat name+denyFlood list,
// not the firmware's parent-tree hierarchy). Reads are immediate; writes go
// through reconfigure. Hierarchy subcommands (put-with-parent/load/save/home)
// aren't supported.
func (r *Repeater) cliRegion(args string) string {
	if args == "" {
		return r.regionList(nil)
	}
	sub, rest, _ := strings.Cut(args, " ")
	rest = strings.TrimSpace(rest)
	switch sub {
	case "list":
		switch rest {
		case "allowed":
			denied := false
			return r.regionList(&denied)
		case "denied":
			denied := true
			return r.regionList(&denied)
		default:
			return "Err - use 'allowed' or 'denied'"
		}
	case "put", "add":
		if rest == "" {
			return "Err - empty name"
		}
		name := rest
		if i := strings.IndexAny(name, " ,|"); i >= 0 {
			name = strings.TrimSpace(name[:i]) // ignore parent (flat model)
		}
		if r.regionHas(name) {
			return "OK" // already present
		}
		return r.applyCfg(func(c *config.RepeaterConfig) {
			c.Regions = append(c.Regions, config.RepeaterRegion{Name: name})
		}, "OK - (flood allowed)")
	case "remove":
		if !r.regionHas(rest) {
			return "Err - not found"
		}
		return r.applyCfg(func(c *config.RepeaterConfig) {
			c.Regions = slices.DeleteFunc(c.Regions, func(rg config.RepeaterRegion) bool { return rg.Name == rest })
		}, "OK")
	case "allowf", "denyf":
		if !r.regionHas(rest) {
			return "Err - unknown region"
		}
		deny := sub == "denyf"
		return r.applyCfg(func(c *config.RepeaterConfig) {
			for i := range c.Regions {
				if c.Regions[i].Name == rest {
					c.Regions[i].DenyFlood = deny
				}
			}
		}, "OK")
	case "default": // the default advert scope (firmware default_scope)
		switch {
		case rest == "": // read form
			if d := r.cfgSnapshot().DefaultRegion; d == "" {
				return " default scope is <null>"
			} else {
				return " default scope is " + d
			}
		case rest == "<null>":
			return r.applyCfg(func(c *config.RepeaterConfig) { c.DefaultRegion = "" }, " default scope is now <null>")
		case rest == config.WildcardRegion:
			return "Err - unknown region"
		default:
			name := rest
			// Auto-create a missing region, flood-allowed (firmware putRegion).
			return r.applyCfg(func(c *config.RepeaterConfig) {
				if !slices.ContainsFunc(c.Regions, func(rg config.RepeaterRegion) bool { return rg.Name == name }) {
					c.Regions = append(c.Regions, config.RepeaterRegion{Name: name})
				}
				c.DefaultRegion = name
			}, " default scope is now "+name)
		}
	default:
		return "ERR: not supported on this node"
	}
}

// cfgRegions snapshots the region list under the lock. ApplyRegions replaces
// the slice (never mutates it in place) from the reload goroutine, so a reader
// can iterate the returned snapshot safely after unlocking.
func (r *Repeater) cfgRegions() []config.RepeaterRegion {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Regions
}

// cfgSnapshot copies the config under the lock (scalar reads like DefaultRegion).
func (r *Repeater) cfgSnapshot() config.RepeaterConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// regionList returns region names, optionally filtered by deny-flood state.
func (r *Repeater) regionList(filterDenied *bool) string {
	var names []string
	for _, rg := range r.cfgRegions() {
		if filterDenied != nil && rg.DenyFlood != *filterDenied {
			continue
		}
		names = append(names, rg.Name)
	}
	if len(names) == 0 {
		return "-none-"
	}
	return strings.Join(names, "\n")
}

func (r *Repeater) regionHas(name string) bool {
	return slices.ContainsFunc(r.cfgRegions(), func(rg config.RepeaterRegion) bool { return rg.Name == name })
}

// clearStats resets the relay counters + last-signal (firmware `clear stats`).
func (r *Repeater) clearStats() {
	r.recvCount.Store(0)
	r.fwdCount.Store(0)
	r.rxAirtimeMs.Store(0)
	r.txAirtimeMs.Store(0)
	r.haveSignal.Store(false)
	r.lastRSSI.Store(0)
	r.lastSNRx4.Store(0)
}

// neighborsList renders the neighbours as text (firmware `neighbors`):
// "hexprefix:secsAgo:snrX4" per line, newest first, or "-none-".
func (r *Repeater) neighborsList() string {
	list := r.snapshotNeighbors()
	if len(list) == 0 {
		return "-none-"
	}
	now := time.Now()
	lines := make([]string, len(list))
	for i, n := range list {
		lines[i] = fmt.Sprintf("%s:%d:%d",
			hex.EncodeToString(n.pubkey[:4]), int64(now.Sub(n.heard).Seconds()), int(n.snr*4))
	}
	return strings.Join(lines, "\n")
}

// reconfigureAfterReply persists + reloads a config change after the CLI reply
// has had time to transmit (reload restarts this node, so we can't do it inline).
// applyCfg is the common tail of every config-mutating CLI command: guard the
// missing-reconfigure case, then schedule the mutation to persist + reload after
// the reply TXes. Returns ok on success, or the unavailable-reply.
func (r *Repeater) applyCfg(mutate func(*config.RepeaterConfig), ok string) string {
	if r.reconfigure == nil {
		return "ERR: config changes not available"
	}
	r.reconfigureAfterReply(mutate)
	return ok
}

func (r *Repeater) reconfigureAfterReply(mutate func(*config.RepeaterConfig)) {
	go func() {
		t := time.NewTimer(cliReplyDelay + 400*time.Millisecond)
		defer t.Stop()
		select {
		case <-r.runCtx.Done():
			return
		case <-t.C:
		}
		if err := r.reconfigure(mutate); err != nil {
			r.log.Error("cli reconfigure failed", "error", err)
		}
	}()
}

// isValidRepeaterName mirrors the firmware isValidName (rejects the chars the
// mesh name syntax reserves).
func isValidRepeaterName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "[]\\:,?*")
}

func floatOrZero(f *float64) string {
	if f == nil {
		return "0"
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func intPtrOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
