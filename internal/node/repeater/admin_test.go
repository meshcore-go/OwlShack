package repeater

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/config"
)

func TestCString(t *testing.T) {
	if got := cString([]byte{'h', 'i', 0, 0, 0}); got != "hi" {
		t.Errorf("cString padded = %q, want %q", got, "hi")
	}
	if got := cString([]byte("abc")); got != "abc" {
		t.Errorf("cString unterminated = %q, want %q", got, "abc")
	}
	if got := cString([]byte{0}); got != "" {
		t.Errorf("cString empty = %q, want %q", got, "")
	}
}

// TestRunCLIPrefix pins the "XX|" correlation-prefix handling: the prefix is
// stripped from the command and reflected onto the reply (the client matches
// its pending request by it). A wrong offset breaks every CLI reply on the air.
func TestRunCLIPrefix(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{Name: "rp"}}
	if got := r.runCLI("A5|get name"); got != "A5|> rp" {
		t.Errorf("prefixed = %q, want %q", got, "A5|> rp")
	}
	if got := r.runCLI("get name"); got != "> rp" {
		t.Errorf("unprefixed = %q, want %q", got, "> rp")
	}
	if got := r.runCLI("  get name"); got != "> rp" { // leading spaces skipped
		t.Errorf("indented = %q, want %q", got, "> rp")
	}
	if got := r.runCLI("bogus"); got != "ERR: unknown command" {
		t.Errorf("unknown = %q, want %q", got, "ERR: unknown command")
	}
}

// TestCLIAdvertIntervalDefaults: fetching advert intervals must report the
// effective values (incl. defaults), not 0 — zero-hop off, flood 30min→1h.
func TestCLIAdvertIntervalDefaults(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{}} // nil intervals → defaults
	if got := r.runCLI("get advert.interval"); got != "> 0" {
		t.Errorf("default advert.interval = %q, want %q", got, "> 0")
	}
	if got := r.runCLI("get flood.advert.interval"); got != "> 47" {
		t.Errorf("default flood.advert.interval = %q, want %q (firmware 47h)", got, "> 47")
	}
	m, h := 20*60, 4*3600
	r.cfg.AdvertInterval, r.cfg.FloodAdvertInterval = &m, &h
	if got := r.runCLI("get advert.interval"); got != "> 20" {
		t.Errorf("advert.interval = %q, want %q", got, "> 20")
	}
	if got := r.runCLI("get flood.advert.interval"); got != "> 4" {
		t.Errorf("flood.advert.interval = %q, want %q", got, "> 4")
	}
}

// TestCLIRadioReadOnly: radio settings can be fetched but never set over mesh.
func TestCLIRadioReadOnly(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{}}
	for _, cmd := range []string{"set radio 915,250,11,1", "set freq 915", "set tx 20"} {
		if got := r.runCLI(cmd); got != "ERR: radio settings are read-only over the mesh" {
			t.Errorf("%q = %q, want read-only error", cmd, got)
		}
	}
}

// TestSetMutation pins the `set` value validation + units against the firmware
// (ranges, error strings, minutes→seconds conversion).
func TestSetMutation(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{}}
	cases := []struct {
		key, val, reply string
		wantMutate      bool
	}{
		{"flood.max", "40", "OK", true},
		{"flood.max", "100", "Error, max 64", false},
		{"flood.max.unscoped", "0", "OK", true},
		{"flood.max.unscoped", "100", "Error, max 64", false},
		{"advert.interval", "120", "OK", true},
		{"advert.interval", "30", "Error: interval range is 60-240 minutes", false},
		{"flood.advert.interval", "47", "OK", true},
		{"flood.advert.interval", "200", "Error: interval range is 3-168 hours", false},
		{"loop.detect", "strict", "OK", true},
		{"loop.detect", "bogus", "Error, must be: off, minimal, moderate, or strict", false},
		{"repeat", "off", "OK - repeat is now OFF", true},
		{"repeat", "on", "OK - repeat is now ON", true},
		{"name", "good", "OK", true},
		{"name", "bad,name", "Error, bad chars", false},
		{"path.hash.mode", "3", "OK", true},
		{"path.hash.mode", "2", "Error, must be 0, 1 or 3", false},
		{"nonsense", "x", "ERR: not supported on this node", false},
	}
	for _, c := range cases {
		mutate, reply := r.setMutation(c.key, c.val)
		if reply != c.reply {
			t.Errorf("set %s %s: reply=%q want %q", c.key, c.val, reply, c.reply)
		}
		if (mutate != nil) != c.wantMutate {
			t.Errorf("set %s %s: mutate present=%v want %v", c.key, c.val, mutate != nil, c.wantMutate)
		}
	}
	// advert.interval is minutes on the wire, seconds in config.
	m, _ := r.setMutation("advert.interval", "120")
	var cfg config.RepeaterConfig
	m(&cfg)
	if cfg.AdvertInterval == nil || *cfg.AdvertInterval != 7200 {
		t.Errorf("advert.interval 120min → %v, want 7200s", cfg.AdvertInterval)
	}
}

// TestUnsupportedCmd: N/A firmware commands return a clear error, not "unknown".
func TestUnsupportedCmd(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{}}
	for _, cmd := range []string{"reboot", "gps on", "sensor list", "poweroff", "start ota"} {
		if got := r.runCLI(cmd); got != "ERR: not supported on this node" {
			t.Errorf("%q = %q, want not-supported error", cmd, got)
		}
	}
	if got := r.runCLI("totallybogus"); got != "ERR: unknown command" {
		t.Errorf("unknown = %q, want unknown-command error", got)
	}
}

// TestRegionReads: region list filters by flood state; remove of a missing
// region errors without touching config.
func TestRegionReads(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{Regions: []config.RepeaterRegion{
		{Name: "alpha"}, {Name: "bravo", DenyFlood: true},
	}}}
	if got := r.runCLI("region list allowed"); got != "alpha" {
		t.Errorf("region list allowed = %q, want %q", got, "alpha")
	}
	if got := r.runCLI("region list denied"); got != "bravo" {
		t.Errorf("region list denied = %q, want %q", got, "bravo")
	}
	if got := r.runCLI("region remove ghost"); got != "Err - not found" {
		t.Errorf("region remove ghost = %q, want %q", got, "Err - not found")
	}
}

// TestRegionsFromConfig pins how the "*" wildcard entry maps to the node's
// wildcard flags (governs plain unscoped flood) and is kept out of the named
// transport scopes. Absent "*" ⇒ unscoped flood denied.
func TestRegionsFromConfig(t *testing.T) {
	cases := []struct {
		name      string
		regions   []config.RepeaterRegion
		wantNamed []string
		wantDeny  bool // wildcard denies unscoped flood
	}{
		{"empty ⇒ deny", nil, nil, true},
		{"* allow", []config.RepeaterRegion{{Name: "*"}}, nil, false},
		{"* deny", []config.RepeaterRegion{{Name: "*", DenyFlood: true}}, nil, true},
		{"named + *", []config.RepeaterRegion{{Name: "alpha"}, {Name: "*"}}, []string{"alpha"}, false},
		{"named only ⇒ deny unscoped", []config.RepeaterRegion{{Name: "alpha"}}, []string{"alpha"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			named, flags := regionsFromConfig(c.regions)
			gotNames := make([]string, len(named))
			for i, r := range named {
				gotNames[i] = r.Name
				if r.Name == "*" {
					t.Errorf("wildcard leaked into named regions")
				}
			}
			if len(gotNames) != len(c.wantNamed) {
				t.Fatalf("named = %v, want %v", gotNames, c.wantNamed)
			}
			for i := range gotNames {
				if gotNames[i] != c.wantNamed[i] {
					t.Errorf("named[%d] = %q, want %q", i, gotNames[i], c.wantNamed[i])
				}
			}
			gotDeny := flags&meshcore.RegionDenyFlood != 0
			if gotDeny != c.wantDeny {
				t.Errorf("wildcard deny = %v, want %v", gotDeny, c.wantDeny)
			}
		})
	}
}

// TestClearStats zeroes the counters.
func TestClearStats(t *testing.T) {
	r := &Repeater{}
	r.recvCount.Store(5)
	r.fwdCount.Store(3)
	r.haveSignal.Store(true)
	r.clearStats()
	if r.recvCount.Load() != 0 || r.fwdCount.Load() != 0 || r.haveSignal.Load() {
		t.Errorf("clearStats left non-zero counters")
	}
}

// TestNeighboursBody pins the neighbours response layout the client decodes:
// [total:2][results:2] then [prefix:N][secsAgo:4][snr:i8] entries, SNR in
// firmware quarter-dB.
func TestNeighboursBody(t *testing.T) {
	r := &Repeater{}
	r.neighbors.m = map[[32]byte]*neighbor{}
	var a, b [32]byte
	a[0], a[1] = 0xAA, 0x01
	b[0], b[1] = 0xBB, 0x02
	now := time.Now()
	r.neighbors.m[a] = &neighbor{pubkey: a, snr: 5.0, heard: now}
	r.neighbors.m[b] = &neighbor{pubkey: b, snr: -2.5, heard: now.Add(-30 * time.Second)}

	// params: [version:1][count:1][offset:2][order_by:1][prefix_len:1]
	params := []byte{0, 10, 0, 0, 2, 6}
	body := r.neighboursBody(params)

	if total := binary.LittleEndian.Uint16(body[0:2]); total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	results := int(binary.LittleEndian.Uint16(body[2:4]))
	if results != 2 {
		t.Fatalf("results = %d, want 2", results)
	}
	const entrySize = 6 + 4 + 1
	if len(body) != 4+results*entrySize {
		t.Fatalf("body len = %d, want %d", len(body), 4+results*entrySize)
	}
	// Newest first: entry 0 is neighbour a (heard now, snr 5.0 → +20 quarter-dB).
	if body[4] != 0xAA {
		t.Errorf("entry0 prefix[0] = %#x, want 0xAA", body[4])
	}
	if snr := int8(body[4+6+4]); snr != 20 {
		t.Errorf("entry0 snr = %d, want 20 (quarter-dB)", snr)
	}
}

// TestRateLimiter: max allows per window, denied until it slides.
func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(2, 30*time.Millisecond)
	if !l.allow() || !l.allow() {
		t.Fatal("first two allows should pass")
	}
	if l.allow() {
		t.Fatal("third allow within window should be denied")
	}
	time.Sleep(35 * time.Millisecond)
	if !l.allow() {
		t.Fatal("allow after window expiry should pass")
	}
}

// TestRegionsExport pins the anon REGIONS reply body (firmware
// exportNamesTo(mask=DENY_FLOOD)): flood-allowed names, comma-separated,
// "*" first when unscoped flood is allowed.
func TestRegionsExport(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{Regions: []config.RepeaterRegion{
		{Name: "alpha"}, {Name: "bravo", DenyFlood: true}, {Name: "*"},
	}}}
	if got := r.regionsExport(); got != "*,alpha" {
		t.Errorf("export = %q, want %q", got, "*,alpha")
	}
	r.cfg.Regions = nil // no "*" entry ⇒ unscoped flood denied ⇒ nothing exported
	if got := r.regionsExport(); got != "" {
		t.Errorf("empty export = %q, want \"\"", got)
	}
}

// TestNeighboursOrderBy pins the order_by selector (firmware semantics):
// 0/absent=newest first, 1=oldest first, 2=strongest, 3=weakest.
func TestNeighboursOrderBy(t *testing.T) {
	r := &Repeater{}
	r.neighbors.m = map[[32]byte]*neighbor{}
	var a, b, c [32]byte
	a[0], b[0], c[0] = 0x01, 0x02, 0x03
	now := time.Now()
	r.neighbors.m[a] = &neighbor{pubkey: a, snr: 5.0, heard: now.Add(-60 * time.Second)} // oldest, strongest
	r.neighbors.m[b] = &neighbor{pubkey: b, snr: -3.0, heard: now}                       // newest, weakest
	r.neighbors.m[c] = &neighbor{pubkey: c, snr: 0.0, heard: now.Add(-30 * time.Second)}

	firstPrefix := func(orderBy byte) byte {
		params := []byte{0, 10, 0, 0, orderBy, 6}
		return r.neighboursBody(params)[4]
	}
	if got := firstPrefix(0); got != 0x02 {
		t.Errorf("order_by 0 (newest): first=%#x, want b(0x02)", got)
	}
	if got := firstPrefix(1); got != 0x01 {
		t.Errorf("order_by 1 (oldest): first=%#x, want a(0x01)", got)
	}
	if got := firstPrefix(2); got != 0x01 {
		t.Errorf("order_by 2 (strongest): first=%#x, want a(0x01)", got)
	}
	if got := firstPrefix(3); got != 0x02 {
		t.Errorf("order_by 3 (weakest): first=%#x, want b(0x02)", got)
	}
}

// TestRegionDefaultCLI pins `region default` (the firmware default_scope):
// read form reports <null>, set auto-creates the region flood-allowed,
// <null> clears. Runs against an in-process reconfigure hook; the config
// write lands asynchronously (after the reply-TX delay), so poll for it.
func TestRegionDefaultCLI(t *testing.T) {
	r := &Repeater{cfg: config.RepeaterConfig{Name: "rp"}}
	r.reconfigure = func(m func(*config.RepeaterConfig)) error { m(&r.cfg); return nil }
	ctx, cancel := context.WithCancel(context.Background()) // applyCfg's goroutine selects on runCtx
	defer cancel()
	r.runCtx = ctx

	await := func(region string, nRegions int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if r.cfg.DefaultRegion == region && len(r.cfg.Regions) == nRegions {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("cfg never became {region=%q nRegions=%d}; have region=%q regions=%+v",
			region, nRegions, r.cfg.DefaultRegion, r.cfg.Regions)
	}

	if got := r.runCLI("region default"); got != " default scope is <null>" {
		t.Fatalf("read empty = %q", got)
	}
	if got := r.runCLI("region default alpha"); got != " default scope is now alpha" {
		t.Fatalf("set = %q", got)
	}
	await("alpha", 1)
	if r.cfg.Regions[0].Name != "alpha" || r.cfg.Regions[0].DenyFlood {
		t.Fatalf("auto-created region wrong: %+v", r.cfg.Regions[0])
	}
	if got := r.runCLI("region default"); got != " default scope is alpha" {
		t.Fatalf("read set = %q", got)
	}
	if got := r.runCLI("region default <null>"); got != " default scope is now <null>" {
		t.Fatalf("clear = %q", got)
	}
	await("", 1) // clearing keeps the region itself
}
