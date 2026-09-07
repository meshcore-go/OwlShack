package repeater

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
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
	// neighborsTextMax mirrors the firmware's `dp - reply < 134` loop bound: it
	// keeps appending entries while the reply is still shorter than this.
	neighborsTextMax = 134

	// Firmware NodePrefs buffer sizes (StrHelper::strncpy truncates to size-1).
	maxNameLen          = 31
	maxPasswordLen      = 15
	maxOwnerInfoLen     = 119
	maxRegionNameLen    = 30
	notSupportedOnNode  = "ERR: not supported on this node"
	radioReadOnlyOnMesh = "ERR: radio settings are read-only over the mesh"
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
	plain := txt.Decrypt(secret)
	if plain == nil || len(plain) < 5 {
		return
	}
	pkt.MarkDoNotRetransmit()
	if client.Permissions&permRoleMask != permAdmin {
		return // CLI is admin-only
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
		r.learnFloodRoute(clientPub, pkt)
	}

	command := cString(plain[5:])
	if flags == txtTypePlain { // legacy CLI gets an ack (firmware TXT_TYPE_PLAIN branch), even on retries
		r.sendLegacyAck(pkt, clientPub, plain[:5+len(command)])
	}
	if isRetry {
		return // duplicate delivery — don't re-run side effects
	}

	reply := r.runCLI(command)
	if reply == "" {
		return
	}
	if err := r.sendText(pkt, clientPub, secret, ts, reply); err != nil {
		r.log.Error("cli reply failed", "error", err)
	}
}

// sendLegacyAck ACKs a legacy plain-text CLI command (firmware TXT_TYPE_PLAIN
// branch of onPeerDataRecv): a bare 4-byte CRC over [timestamp][flags][text] +
// the client's pubkey, sent along the learned route (flooded — with the
// request's scope — when unknown) after TXT_ACK_DELAY.
func (r *Repeater) sendLegacyAck(reqPkt *meshcore.Packet, clientPub [32]byte, ackPlaintext []byte) {
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint32(payload, meshcore.CalcAckHash(ackPlaintext, clientPub[:]))

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
		err = r.sendPkt(out, node.PrioritySend, txtAckDelay)
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
		return buildinfo.Version + " (Build: " + buildinfo.Date + ")"
	case cmd == "clock":
		return formatClock(time.Now())
	case strings.HasPrefix(cmd, "clock sync"):
		// Host time is authoritative and already correct, so there is nothing to
		// set; reply in the firmware's shape so a client sees our clock.
		return "OK - clock set: " + formatClock(time.Now())
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
	case strings.HasPrefix(cmd, "discover.neighbors "):
		return "Err - discover.neighbors has no options"
	case cmd == "neighbors":
		return r.neighborsList()
	case strings.HasPrefix(cmd, "neighbor.remove "):
		return r.cliNeighborRemove(strings.TrimSpace(cmd[len("neighbor.remove "):]))
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
		return r.cliSetPerm(cmd[len("setperm "):])
	case strings.HasPrefix(cmd, "password "):
		return r.cliPassword(cmd[len("password "):])
	case cmd == "region" || strings.HasPrefix(cmd, "region "):
		return r.cliRegion(cmd)
	case strings.HasPrefix(cmd, "get "):
		return r.cliGet(cmd[len("get "):])
	case strings.HasPrefix(cmd, "set radio "), strings.HasPrefix(cmd, "set freq "), strings.HasPrefix(cmd, "set tx "):
		return radioReadOnlyOnMesh
	case strings.HasPrefix(cmd, "set "):
		return r.cliSet(cmd[len("set "):])
	case isUnsupportedCmd(cmd):
		return notSupportedOnNode
	default:
		return "Unknown command"
	}
}

// isUnsupportedCmd reports firmware commands that don't apply to a Linux-hosted
// Go relay (power/board/GPS/sensors/firmware-flash/chip-specific) — answered
// with a clear error rather than "Unknown command".
func isUnsupportedCmd(cmd string) bool {
	for _, p := range []string{
		"poweroff", "shutdown", "reboot", "clkreboot", "board",
		"powersaving", "gps", "sensor", "start ota", "tempradio",
		"erase", "time", "log",
		"stats-core", "stats-packets", "stats-radio", // serial-only in firmware
	} {
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}

// Firmware config keys we don't model (chip/board/bridge tuning, rekeying,
// serial-only reads): answered "not supported" instead of the firmware's
// unknown-key reply, which would misreport them as non-existent.
var unsupportedGetKeys = []string{
	"dutycycle", "af", "int.thresh", "cad", "agc.reset.interval",
	"allow.read.only", "prv.key", "acl", "radio.rxgain", "radio.fem.rxgain", "radio.fem.txgain",
	"bridge.", "bootloader.ver", "adc.multiplier",
	"pwrmgt.", "extra.sf",
}

var unsupportedSetKeys = []string{
	"dutycycle", "af", "int.thresh", "cad", "agc.reset.interval",
	"allow.read.only", "prv.key", "radio.rxgain", "radio.fem.rxgain", "radio.fem.txgain",
	"bridge.", "adc.multiplier", "extra.sf",
}

func isUnsupportedKey(keys []string, key string) bool {
	return slices.ContainsFunc(keys, func(k string) bool {
		return key == k || (strings.HasSuffix(k, ".") && strings.HasPrefix(key, k))
	})
}

// cliGet reads a config value. Radio params come from the shared Settings (the
// repeater shares the modem); the rest from the repeater's own config.
func (r *Repeater) cliGet(key string) string {
	cfg := r.cfgSnapshot()
	switch key {
	case "name":
		return "> " + cfg.Name
	case "lat":
		return "> " + floatOrZero(cfg.Latitude)
	case "lon":
		return "> " + floatOrZero(cfg.Longitude)
	case "owner.info":
		return "> " + strings.ReplaceAll(cfg.OwnerInfo, "\n", "|")
	case "repeat":
		if cfg.IsFwdDisabled() {
			return "> off"
		}
		return "> on"
	case "loop.detect":
		return "> " + cfg.LoopDetectOr()
	case "path.hash.mode": // firmware unit: bytes-1
		return fmt.Sprintf("> %d", cfg.PathHashSizeOr()-1)
	case "flood.max":
		return fmt.Sprintf("> %d", cfg.FloodMaxOr())
	case "flood.max.advert":
		return fmt.Sprintf("> %d", cfg.FloodMaxAdvertOr())
	case "flood.max.unscoped":
		return fmt.Sprintf("> %d", cfg.FloodMaxUnscopedOr())
	case "guest.password":
		return "> " + cfg.GuestPassword
	case "txdelay":
		v := cfg.TxDelayFactorOr()
		return "> " + floatOrZero(&v)
	case "direct.txdelay":
		v := cfg.DirectTxDelayFactorOr()
		return "> " + floatOrZero(&v)
	case "rxdelay":
		v := cfg.RxDelayBaseOr()
		return "> " + floatOrZero(&v)
	case "multi.acks":
		return fmt.Sprintf("> %d", cfg.MultiAcksOr())
	case "public.key":
		return "> " + hex.EncodeToString(r.node.Identity().PublicKeyBytes())
	case "role":
		return "> repeater"
	case "bridge.type":
		return "> none"
	case "advert.interval": // minutes (firmware unit); config stores seconds
		return fmt.Sprintf("> %d", (cfg.AdvertIntervalOr()+30)/60) // effective (incl. default), rounded
	case "flood.advert.interval": // hours (firmware unit)
		return fmt.Sprintf("> %d", (cfg.FloodAdvertIntervalOr()+1800)/3600) // effective (incl. default), rounded
	case "radio", "tx", "freq":
		return r.cliGetRadio(key)
	default:
		if isUnsupportedKey(unsupportedGetKeys, key) {
			return notSupportedOnNode
		}
		return "??: " + key
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

var (
	errBadPubKey     = errors.New("bad pubkey")
	errInvalidParams = errors.New("invalid params")
)

// cliSetPerm handles `setperm <pubkey-hex> <perms-int8>` (firmware MyMesh
// handleCommand): a role of 0 removes the client (prefix allowed), anything
// else needs the full key and stores the whole permission byte.
func (r *Repeater) cliSetPerm(args string) string {
	sp := strings.IndexByte(args, ' ')
	if sp < 0 {
		return "Err - bad params"
	}
	pubHex := args[:sp]
	if len(pubHex) > 64 || len(pubHex)%2 != 0 {
		return "Err - bad pubkey" // Utils::fromHex demands exactly hex_len/2 bytes
	}
	switch err := r.SetACL(pubHex, int(uint8(atoi(args[sp+1:])))); {
	case err == nil:
		return "OK"
	case errors.Is(err, errBadPubKey):
		return "Err - bad pubkey"
	default:
		return "Err - invalid params"
	}
}

// SetACL applies setperm semantics (firmware ClientACL::applyPermissions): a
// guest role revokes and may name the client by a pubkey prefix; any other role
// needs the full key. The permission byte is stored whole.
func (r *Repeater) SetACL(pubHex string, perms int) error {
	pubHex = strings.ToLower(strings.TrimSpace(pubHex))
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) == 0 || len(pub) > 32 {
		return errBadPubKey
	}
	if perms&permRoleMask == permGuest {
		full, ok := r.aclMatchPrefix(pubHex)
		if !ok {
			return fmt.Errorf("%w: client not found", errInvalidParams)
		}
		r.aclDelete(full)
		return nil
	}
	if len(pub) != 32 {
		return fmt.Errorf("%w: full pubkey required to grant a role", errInvalidParams)
	}
	e := r.aclGet(pubHex) // preserve an existing client's replay timestamp
	if e == nil {
		e = &store.RepeaterACLEntry{PubKey: pubHex}
	}
	e.Permissions = perms & 0xFF
	e.LastSeen = time.Now()
	r.aclPut(e)
	return nil
}

func (r *Repeater) ClearStats() { r.clearStats() }

// advertAfterReply sends a CLI-triggered self-advert after cliAdvertDelay so
// the CLI reply (queued at cliReplyDelay) transmits first — matching the
// firmware's sendSelfAdvertisement(1500, ...).
func (r *Repeater) advertAfterReply(flood bool) {
	go func() {
		t := time.NewTimer(cliAdvertDelay)
		defer t.Stop()
		select {
		case <-r.runContext().Done():
		case <-t.C:
			if err := r.SendAdvert(flood); err != nil {
				r.log.Error("cli advert failed", "error", err)
			}
		}
	}()
}

// sendText sends a CLI reply as a TXT_MSG datagram (never a PATH-return, per
// firmware), direct along the learned route or flooded — with the request's
// scope — when unknown. The reply timestamp is unique and never equal to the
// command's (the firmware's CLI-view workaround).
func (r *Repeater) sendText(reqPkt *meshcore.Packet, clientPub [32]byte, secret []byte, senderTS uint32, text string) error {
	ts := r.uniqueTimestamp()
	if ts == senderTS {
		ts = r.uniqueTimestamp()
	}
	me := r.node.Identity().PublicKey()
	plaintext := meshcore.BuildTextPlaintext(time.Unix(int64(ts), 0), txtTypeCliData<<2, []byte(text))
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
	return r.sendPkt(out, node.PrioritySend, cliReplyDelay)
}

// cliSet handles `set <key> <value>` for the repeater's own config. The value
// is validated inline (firmware-style errors); on success the change is
// persisted + reloaded asynchronously via reconfigure.
func (r *Repeater) cliSet(kv string) string {
	key, val, ok := strings.Cut(kv, " ")
	if !ok {
		return "unknown config: " + kv
	}
	mutate, reply := r.setMutation(key, val)
	if mutate == nil {
		return reply // validation error / unsupported key
	}
	return r.applyCfg(mutate, reply)
}

// setMutation validates a `set` value and returns the config mutation + success
// reply, or (nil, errorReply). Mirrors the firmware CommonCLI set-branch
// parsing (atoi/atof leniency, uint8 truncation, prefix keywords), units,
// ranges and messages.
func (r *Repeater) setMutation(key, val string) (func(*config.RepeaterConfig), string) {
	switch key {
	case "name":
		if !isValidRepeaterName(val) {
			return nil, "Error, bad chars"
		}
		name := truncate(val, maxNameLen)
		return func(c *config.RepeaterConfig) { c.Name = name }, "OK"
	case "lat", "lon":
		f := atof(val)
		// Divergence: atof accepts "nan", which would make Stats() un-marshalable.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, "Error, invalid number"
		}
		if key == "lat" {
			return func(c *config.RepeaterConfig) { c.Latitude = &f }, "OK"
		}
		return func(c *config.RepeaterConfig) { c.Longitude = &f }, "OK"
	case "advert.interval": // minutes; 0 (off) or 60-240 (firmware range)
		m := int(digits(val))
		if (m > 0 && m < config.MinAdvertIntervalSecs/60) || m > config.MaxAdvertIntervalSecs/60 {
			return nil, "Error: interval range is 60-240 minutes"
		}
		secs := (m / 2) * 2 * 60 // firmware stores this in 2-minute units
		return func(c *config.RepeaterConfig) { c.AdvertInterval = &secs }, "OK"
	case "flood.advert.interval": // hours; 0 (off) or 3-168
		h := int(digits(val))
		if (h > 0 && h < config.MinFloodAdvertIntervalSecs/3600) || h > config.MaxFloodAdvertIntervalSecs/3600 {
			return nil, "Error: interval range is 3-168 hours"
		}
		secs := h * 3600
		return func(c *config.RepeaterConfig) { c.FloodAdvertInterval = &secs }, "OK"
	case "flood.max", "flood.max.advert", "flood.max.unscoped":
		n := int(uint8(atoi(val))) // firmware: uint8_t m = atoi(...)
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
		mode := uint8(atoi(val))
		if mode >= 3 {
			return nil, "Error, must be 0,1, or 2"
		}
		size := int(mode) + 1
		return func(c *config.RepeaterConfig) { c.PathHashSize = &size }, "OK"
	case "loop.detect":
		var v string
		for _, level := range []string{"off", "minimal", "moderate", "strict"} { // firmware memcmp: prefix match
			if strings.HasPrefix(val, level) {
				v = level
				break
			}
		}
		if v == "" {
			return nil, "Error, must be: off, minimal, moderate, or strict"
		}
		return func(c *config.RepeaterConfig) { c.LoopDetect = &v }, "OK"
	case "repeat":
		disable := strings.HasPrefix(val, "off") // firmware: anything else is ON
		msg := "OK - repeat is now ON"
		if disable {
			msg = "OK - repeat is now OFF"
		}
		return func(c *config.RepeaterConfig) { c.DisableFwd = &disable }, msg
	case "owner.info":
		info := truncate(strings.ReplaceAll(val, "|", "\n"), maxOwnerInfoLen) // firmware stores | as newline
		return func(c *config.RepeaterConfig) { c.OwnerInfo = info }, "OK"
	case "guest.password":
		pass := truncate(val, maxPasswordLen)
		return func(c *config.RepeaterConfig) { c.GuestPassword = pass }, "OK"
	case "txdelay", "direct.txdelay":
		f := atof(val)
		if !(f >= 0 && f <= config.MaxTxDelayFactor) { // NaN-rejecting, like the firmware gate
			return nil, "Error, must be 0-2"
		}
		if key == "txdelay" {
			return func(c *config.RepeaterConfig) { c.TxDelayFactor = &f }, "OK"
		}
		return func(c *config.RepeaterConfig) { c.DirectTxDelayFactor = &f }, "OK"
	case "rxdelay":
		f := atof(val)
		if !(f >= 0 && f <= config.MaxRxDelayBase) {
			return nil, "Error, must be 0-20"
		}
		return func(c *config.RepeaterConfig) { c.RxDelayBase = &f }, "OK"
	case "multi.acks":
		n := int(uint8(atoi(val))) // firmware: uint8_t, no range check on set
		return func(c *config.RepeaterConfig) { c.MultiAcks = &n }, "OK"
	default:
		if isUnsupportedKey(unsupportedSetKeys, key) {
			return nil, notSupportedOnNode
		}
		return nil, "unknown config: " + key + " " + val
	}
}

// cliPassword handles `password <new>` — change the admin password (firmware
// reply echoes the stored, 15-char-truncated password). Persisted + reloaded
// via reconfigure.
func (r *Repeater) cliPassword(pass string) string {
	pass = truncate(pass, maxPasswordLen)
	return r.applyCfg(func(c *config.RepeaterConfig) { c.AdminPassword = pass }, "password now: "+pass)
}

// cliNeighborRemove handles `neighbor.remove <pubkey-hex>`, matching on the
// bytes supplied (the firmware accepts a prefix).
func (r *Repeater) cliNeighborRemove(pubHex string) string {
	pub, err := hex.DecodeString(strings.ToLower(pubHex))
	if err != nil || len(pub) == 0 || len(pub) > 32 {
		return "ERR: bad pubkey"
	}
	r.removeNeighbor(pub)
	return "OK"
}

// cliRegion ports CommonCLI::handleRegionCmd onto our flat name+denyFlood list
// (no parent tree, so `region def` isn't supported and parents are only
// validated). Lookups use the firmware's exact-then-prefix match; the "*"
// wildcard always exists. Reads are immediate; writes go through reconfigure.
func (r *Repeater) cliRegion(cmd string) string {
	if cmd == "region def" || strings.HasPrefix(cmd, "region def ") {
		return notSupportedOnNode
	}
	parts := strings.SplitN(cmd, " ", 5)
	if len(parts) > 4 {
		parts = parts[:4] // Utils::parseTextParts(max=4) drops the rest
	}
	n := len(parts)
	sub := ""
	if n >= 2 {
		sub = parts[1]
	}
	arg := ""
	if n >= 3 {
		arg = parts[2]
	}
	cfg := r.cfgSnapshot()

	switch {
	case n == 1:
		return r.regionTree()
	case sub == "load":
		return "" // firmware reloads asynchronously and replies nothing
	case sub == "save":
		return "OK" // every change is already persisted by reconfigure
	case n >= 3 && (sub == "allowf" || sub == "denyf"):
		name, ok := regionByPrefix(cfg.Regions, arg)
		if !ok {
			return "Err - unknown region"
		}
		deny := sub == "denyf"
		return r.applyCfg(func(c *config.RepeaterConfig) { setRegionDeny(c, name, deny) }, "OK")
	case n >= 3 && sub == "get":
		name, ok := regionByPrefix(cfg.Regions, arg)
		if !ok {
			return "Err - unknown region"
		}
		flag := "F"
		if regionDenies(cfg.Regions, name) {
			flag = ""
		}
		return " " + name + " " + flag
	case n >= 3 && sub == "home":
		name, ok := regionByPrefix(cfg.Regions, arg)
		if !ok {
			return "Err - unknown region"
		}
		home := name
		if home == config.WildcardRegion {
			home = "" // home_id 0 is the wildcard
		}
		return r.applyCfg(func(c *config.RepeaterConfig) { c.HomeRegion = home }, " home is now "+name)
	case n == 2 && sub == "home":
		if cfg.HomeRegion == "" {
			return " home is *"
		}
		return " home is " + cfg.HomeRegion
	case n >= 3 && sub == "default": // the default advert scope (firmware default_scope)
		if arg == "<null>" {
			return r.applyCfg(func(c *config.RepeaterConfig) { c.DefaultRegion = "" }, " default scope is now <null>")
		}
		name, ok := regionByPrefix(cfg.Regions, arg)
		if !ok {
			if !isValidRegionName(arg) {
				return "Err - region table full" // putRegion refused it
			}
			name = truncate(arg, maxRegionNameLen)
		}
		if name == config.WildcardRegion {
			return "Err - region table full" // our config can't scope adverts to "*"
		}
		return r.applyCfg(func(c *config.RepeaterConfig) {
			setRegionDeny(c, name, false) // firmware: def->flags = 0, auto-creating if missing
			c.DefaultRegion = name
		}, " default scope is now "+name)
	case n == 2 && sub == "default":
		if cfg.DefaultRegion == "" {
			return " default scope is <null>"
		}
		return " default scope is " + cfg.DefaultRegion
	case n >= 3 && sub == "put":
		parent := config.WildcardRegion
		if n >= 4 {
			p, ok := regionByPrefix(cfg.Regions, parts[3])
			if !ok {
				return "Err - unknown parent"
			}
			parent = p
		}
		name := truncate(arg, maxRegionNameLen)
		if !isValidRegionName(name) || name == config.WildcardRegion || name == parent {
			return "Err - unable to put"
		}
		return r.applyCfg(func(c *config.RepeaterConfig) { setRegionDeny(c, name, false) }, "OK - (flood allowed)")
	case n >= 3 && sub == "remove":
		if arg == config.WildcardRegion {
			return "Err - not empty" // the wildcard can't be removed
		}
		name, ok := regionByName(cfg.Regions, arg)
		if !ok {
			return "Err - not found"
		}
		return r.applyCfg(func(c *config.RepeaterConfig) {
			c.Regions = slices.DeleteFunc(c.Regions, func(rg config.RepeaterRegion) bool { return rg.Name == name })
			if c.DefaultRegion == name {
				c.DefaultRegion = "" // a dangling reference fails validation
			}
			if c.HomeRegion == name {
				c.HomeRegion = ""
			}
		}, "OK")
	case n >= 3 && sub == "list":
		switch arg {
		case "allowed", "denied":
			if names := regionNames(cfg.Regions, arg == "denied"); names != "" {
				return names
			}
			return "-none-"
		default:
			return "Err - use 'allowed' or 'denied'"
		}
	default:
		return "Err - ??"
	}
}

// regionByName is RegionMap::findByName: exact match, "#" ignored.
func regionByName(regions []config.RepeaterRegion, name string) (string, bool) {
	if name == config.WildcardRegion {
		return name, true
	}
	name = strings.TrimPrefix(name, "#")
	for _, rg := range regions {
		if strings.TrimPrefix(rg.Name, "#") == name {
			return rg.Name, true
		}
	}
	return "", false
}

// regionByPrefix is RegionMap::findByNamePrefix: an exact match wins, else the
// last region whose name starts with the prefix.
func regionByPrefix(regions []config.RepeaterRegion, prefix string) (string, bool) {
	if name, ok := regionByName(regions, prefix); ok {
		return name, true
	}
	prefix = strings.TrimPrefix(prefix, "#")
	partial := ""
	for _, rg := range regions {
		if rg.Name != config.WildcardRegion && strings.HasPrefix(strings.TrimPrefix(rg.Name, "#"), prefix) {
			partial = rg.Name
		}
	}
	return partial, partial != ""
}

// regionDenies reports a region's deny-flood flag; the wildcard denies when it
// has no config entry.
func regionDenies(regions []config.RepeaterRegion, name string) bool {
	for _, rg := range regions {
		if rg.Name == name {
			return rg.DenyFlood
		}
	}
	return true
}

// setRegionDeny sets a region's deny-flood flag, creating the entry if needed
// (so `allowf *` materialises the wildcard entry).
func setRegionDeny(c *config.RepeaterConfig, name string, deny bool) {
	for i := range c.Regions {
		if c.Regions[i].Name == name {
			c.Regions[i].DenyFlood = deny
			return
		}
	}
	c.Regions = append(c.Regions, config.RepeaterRegion{Name: name, DenyFlood: deny})
}

// isValidRegionName is RegionMap::is_name_char over every byte: alnum, "-",
// "$", "#" and anything at or above 'A' (accented UTF-8 bytes included).
func isValidRegionName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '-' || c == '$' || c == '#' || (c >= '0' && c <= '9') || c >= 'A') {
			return false
		}
	}
	return true
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

// regionNames renders RegionMap::exportNamesTo(mask=DENY_FLOOD): the regions
// whose deny-flood flag matches `denied`, comma-separated, "*" first (the
// wildcard denies when it has no entry), leading "#" stripped.
func regionNames(regions []config.RepeaterRegion, denied bool) string {
	var names []string
	if regionDenies(regions, config.WildcardRegion) == denied {
		names = append(names, config.WildcardRegion)
	}
	for _, rg := range regions {
		if rg.Name != config.WildcardRegion && rg.DenyFlood == denied {
			names = append(names, strings.TrimPrefix(rg.Name, "#"))
		}
	}
	return strings.Join(names, ",")
}

// regionTree renders the region map the way the firmware's exportTo does: the
// wildcard first, then its children indented one space, each line suffixed "^"
// for the home region (the wildcard when none is set) and " F" when flood is
// allowed, every line newline-terminated. Our model is flat, so every named
// region is a direct child of the wildcard.
func (r *Repeater) regionTree() string {
	cfg := r.cfgSnapshot()
	home := cfg.HomeRegion
	if home == "" {
		home = config.WildcardRegion
	}
	line := func(name string, deny bool, indent string) string {
		s := indent + strings.TrimPrefix(name, "#")
		if name == home {
			s += "^"
		}
		if !deny {
			s += " F"
		}
		return s + "\n"
	}

	var b strings.Builder
	b.WriteString(line(config.WildcardRegion, regionDenies(cfg.Regions, config.WildcardRegion), ""))
	for _, rg := range cfg.Regions {
		if rg.Name != config.WildcardRegion {
			b.WriteString(line(rg.Name, rg.DenyFlood, " "))
		}
	}
	return b.String()
}

// clearStats resets what the firmware's `clear stats` does: the radio's
// recv/sent counters, the Dispatcher's route counters and the dup counters —
// not airtime and not the last signal reading.
func (r *Repeater) clearStats() {
	r.recvCount.Store(0)
	r.fwdCount.Store(0)
	r.sentFlood.Store(0)
	r.sentDirect.Store(0)
	if r.routeStats != nil {
		base := r.routeStats()
		r.mu.Lock()
		r.statsBase = base
		r.mu.Unlock()
	}
}

// neighborsList renders the neighbours as text (firmware `neighbors`):
// "hexprefix:secsAgo:snrX4" per line, newest first, or "-none-".
func (r *Repeater) neighborsList() string {
	list := r.snapshotNeighbors()
	if len(list) == 0 {
		return "-none-"
	}
	now := time.Now()
	var b strings.Builder
	for _, n := range list {
		if b.Len() >= neighborsTextMax {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s:%d:%d", hex.EncodeToString(n.pubkey[:4]), int64(now.Sub(n.heard).Seconds()), int8(n.snr*4))
	}
	return b.String()
}

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

// reconfigureAfterReply persists + reloads a config change after the CLI reply
// has had time to transmit (reload restarts this node, so we can't do it inline).
func (r *Repeater) reconfigureAfterReply(mutate func(*config.RepeaterConfig)) {
	go func() {
		t := time.NewTimer(cliReplyDelay + 400*time.Millisecond)
		defer t.Stop()
		select {
		case <-r.runContext().Done():
			return
		case <-t.C:
		}
		if err := r.reconfigure(mutate); err != nil {
			r.log.Error("cli reconfigure failed", "error", err)
		}
	}()
}

// isValidRepeaterName mirrors the firmware isValidName (rejects the chars the
// mesh name syntax reserves). Unlike the firmware we also refuse an empty name,
// which this node cannot run under.
func isValidRepeaterName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "[]\\:,?*")
}

// formatClock renders a time the way the firmware's DateTime replies do:
// "HH:MM - D/M/YYYY UTC" (only the time is zero-padded).
func formatClock(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("%02d:%02d - %d/%d/%d UTC", t.Hour(), t.Minute(), t.Day(), int(t.Month()), t.Year())
}

// floatOrZero renders a float the way the firmware's StrHelper::ftoa does:
// always with a decimal point, so 915 reads "915.0" and nil reads "0.0".
func floatOrZero(f *float64) string {
	v := 0.0
	if f != nil {
		v = *f
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

func intPtrOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// truncate is StrHelper::strncpy into a buffer of n+1 bytes: at most n bytes.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// atoi is C atoi: leading whitespace, optional sign, then digits; 0 otherwise.
func atoi(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := int(digits(s))
	if neg {
		return -n
	}
	return n
}

// digits is the firmware's _atoi: unsigned decimal digits from the start, 0
// otherwise — no sign, no whitespace.
func digits(s string) uint32 {
	var n uint32
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + uint32(s[i]-'0')
	}
	return n
}

// atof is C atof: the longest leading decimal prefix, 0 when there is none.
func atof(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	for i := len(s); i > 0; i-- {
		if f, err := strconv.ParseFloat(s[:i], 64); err == nil {
			return f
		}
	}
	return 0
}
