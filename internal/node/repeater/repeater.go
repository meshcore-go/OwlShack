// Package repeater implements the repeater NODE personality: a node the bot
// RUNS on the mesh that relays flood/direct packets, advertises itself as a
// REPEATER, and tracks its RF neighbours. It is the server-side mirror of
// internal/client/repeater (which drives *remote* repeaters).
//
// Relay mechanics live in meshcore-go's node router (the Go port of the base
// mesh::Mesh forwarding): it appends our path hash, dedups, and re-transmits.
// This package supplies only the *policy* — the allowForward handler mirroring
// the firmware's MyMesh::allowPacketForward (see examples/simple_repeater) —
// plus adverts and neighbour tracking.
package repeater

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/api"
	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/store"
)

// neighbor is a directly-heard (zero-hop) repeater advert — the firmware's
// NeighbourInfo. Kept in memory only, matching the firmware (lost on restart).
type neighbor struct {
	pubkey [32]byte
	name   string
	snr    float64
	heard  time.Time
}

type Repeater struct {
	cfg config.RepeaterConfig

	node  *node.Node
	radio node.MuxRadio
	log   *slog.Logger

	store *store.Store
	hub   *api.Hub

	startedAt time.Time
	recvCount atomic.Uint64 // raw packets received (radio raw handler)
	fwdCount  atomic.Uint64 // packets we relayed (allowForward returned true)

	// Signal + airtime, for the over-mesh STATUS response. lastRSSI/lastSNRx4
	// are the last heard values (SNR in firmware quarter-dB); noise floor is
	// derived as rssi-snr. rx/txAirtimeMs accumulate estimated LoRa time-on-air
	// for packets heard / relayed. airtime is the ToA estimator (nil when radio
	// params are unknown); set once at construction, before the node is live.
	lastRSSI    atomic.Int32
	lastSNRx4   atomic.Int32
	haveSignal  atomic.Bool
	rxAirtimeMs atomic.Uint64
	txAirtimeMs atomic.Uint64
	airtime     func(packetLen int) uint32
	logging     atomic.Bool // `log start/stop` — per-packet trace to the bot log

	// Device readings polled from the shared modem (noise floor + battery), for
	// the STATUS response and telemetry. Cached; refreshed by deviceStatsLoop.
	noiseFloor      atomic.Int32
	batteryMV       atomic.Uint32
	haveDeviceStats atomic.Bool
	pollStats       func(ctx context.Context) (noiseFloor int16, batteryMV uint16)

	// reconfigure applies an over-mesh CLI config change (set/password/region):
	// persist + validate + reload. nil disables config writes. Called off the
	// dispatch path (it restarts this node).
	reconfigure func(mutate func(*config.RepeaterConfig)) error

	// discover tracks an in-flight `discover.neighbors` request: responses whose
	// tag matches (within the window) are recorded as neighbours.
	discover struct {
		sync.Mutex
		tag   uint32
		until time.Time
	}

	// anonLimiter / discoverLimiter gate our unauthenticated replies (firmware
	// anon_limiter 4 per 3min for the anon sub-requests, discover_limiter 4 per
	// 2min for discovery responses) so a spammer can't turn us into a beacon.
	anonLimiter     *rateLimiter
	discoverLimiter *rateLimiter

	neighbors struct {
		sync.Mutex
		m map[[32]byte]*neighbor
	}

	// routes caches each admin client's return path, learned from its flood
	// login (the accumulated path hashes). Direct-request replies route along
	// it; unknown clients get a flooded reply. In-memory only — relearned on
	// the next flood login after a restart, so it needs no persistence.
	routes struct {
		sync.Mutex
		m map[[32]byte]clientRoute
	}

	// acl mirrors the persisted admin-client ACL in memory so the packet path
	// (aclClient runs on ~1/256 of all TXT/REQ traffic — anything colliding with
	// our destination-hash prefix) never hits the DB. Write-through:
	// aclPut/aclDelete update the map and persist async; loaded at construction.
	acl struct {
		sync.RWMutex
		m map[string]*store.RepeaterACLEntry // keyed by full pubkey hex
	}

	mu     sync.Mutex
	cancel context.CancelFunc
	runCtx context.Context
}

// Hooks are the app-provided callbacks the repeater needs but can't build
// itself (they reach into the config/reload machinery and the modem). Kept as a
// struct so the node stays decoupled from those packages (plain func fields).
type Hooks struct {
	// Reconfigure persists + validates + reloads a repeater config change (the
	// over-mesh CLI set/password/region commands). nil disables config writes.
	Reconfigure func(mutate func(*config.RepeaterConfig)) error
	// PollStats reads the shared modem's device stats (noise floor + battery).
	// nil when unavailable (those STATUS/telemetry fields then stay 0).
	PollStats func(ctx context.Context) (noiseFloor int16, batteryMV uint16)
}

func NewRepeater(cfg config.RepeaterConfig, mux *node.RadioMux, st *store.Store, hub *api.Hub, hooks Hooks) (*Repeater, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("repeater name is required")
	}

	id, err := config.LocalIdentityFromHex(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("repeater identity: %w", err)
	}

	radio := mux.NewRadio()
	log := slog.Default().With("component", "repeater", "name", name)

	r := &Repeater{
		cfg:         cfg,
		radio:       radio,
		log:         log,
		store:       st,
		hub:         hub,
		reconfigure: hooks.Reconfigure,
		pollStats:   hooks.PollStats,
	}
	r.neighbors.m = make(map[[32]byte]*neighbor)
	r.routes.m = make(map[[32]byte]clientRoute)
	r.acl.m = make(map[string]*store.RepeaterACLEntry)
	r.anonLimiter = newRateLimiter(4, 3*time.Minute)     // firmware anon_limiter(4, 180)
	r.discoverLimiter = newRateLimiter(4, 2*time.Minute) // firmware discover_limiter(4, 120)
	r.aclLoad()                                          // seed the ACL cache from the DB before handlers register
	// Build the LoRa time-on-air estimator from the shared radio settings, the
	// same way the modem does. Set before the node goes live so the raw handler
	// and allowForward (both airtime accumulators) never see a torn value.
	r.airtime = buildAirtimeEstimator(st)

	opts := []node.Option{
		node.WithMaxPeers(100_000),
		node.WithErrorHandler(func(err error) { log.Error("node error", "error", err) }),
		// The relay policy — this is what makes the node a repeater. A node
		// with no allowForward handler never relays (the router's canForward
		// returns false), which is why a companion doesn't forward.
		node.WithAllowForwardHandler(r.allowForward),
	}
	// Regions the repeater serves: named scopes' transport keys derive from the
	// region name (SHA256(name)[:16]), matching the firmware. Adding them lets
	// the repeater relay those scoped (transport-flood) packets via
	// FindFloodMatch. The "*" wildcard governs plain unscoped flood separately.
	named, wildcardFlags := regionsFromConfig(cfg.Regions)
	if len(named) > 0 {
		opts = append(opts, node.WithRegions(named...))
	}
	r.node = node.New(id, radio, opts...)
	r.node.Regions().SetWildcardFlags(wildcardFlags) // "*" entry ⇒ relay unscoped flood; absent ⇒ don't

	r.registerHandlers()

	return r, nil
}

// buildAirtimeEstimator builds a LoRa time-on-air estimator from the shared
// radio Settings, mirroring how the modem builds its own. Returns nil when the
// radio params aren't set (no relaying happens without them, so airtime stays
// 0). Used to accumulate rx/tx airtime for the over-mesh STATUS response.
func buildAirtimeEstimator(st *store.Store) func(int) uint32 {
	s, err := st.Settings.Get(context.Background())
	if err != nil || s.Freq == nil || s.BW == nil || s.SF == nil || s.CR == nil {
		return nil
	}
	return hardware.LoRaAirtimeEstimator(&hardware.RadioConfig{
		FreqHz: uint32(*s.Freq * 1_000_000),
		BwHz:   uint32(*s.BW * 1000),
		SF:     uint8(*s.SF),
		CR:     uint8(*s.CR),
	})
}

func (r *Repeater) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.runCtx = ctx
	r.startedAt = time.Now()
	r.mu.Unlock()

	// advertLoop announces on start and runs both the zero-hop and flood
	// schedules; each is independently disabled by a 0 interval.
	go r.advertLoop(ctx)
	if r.pollStats != nil {
		go r.deviceStatsLoop(ctx)
	}

	r.log.Info("repeater started", "pubkey", hex.EncodeToString(r.node.Identity().PublicKeyBytes()[:8]))
	return nil
}

// deviceStatsLoop refreshes the cached noise floor + battery from the shared
// modem (pollStats blocks ~500ms, so it can't run on the packet path). Polled
// periodically since STATUS/telemetry requests are infrequent and these values
// vary slowly.
func (r *Repeater) deviceStatsLoop(ctx context.Context) {
	const interval = 60 * time.Second
	refresh := func() {
		noise, batt := r.pollStats(ctx)
		if ctx.Err() != nil {
			return
		}
		r.noiseFloor.Store(int32(noise))
		r.batteryMV.Store(uint32(batt))
		r.haveDeviceStats.Store(true)
	}
	refresh()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

func (r *Repeater) Stop() error {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	r.node.Stop()
	return nil
}

// regionsFromConfig splits the config regions into the named transport scopes
// and the "*" wildcard scope (plain unscoped flood). Named scopes derive their
// transport key from the name (SHA256(name)[:16] — firmware getAutoKeyFor, not
// the '#'-prefixed hashtag scheme). The wildcard is returned as flags for the
// node's built-in wildcard region: a "*" entry maps to its DenyFlood, and no
// "*" entry means unscoped flood is NOT relayed (the entry was deleted). "*" is
// never a named region, so it's excluded from the returned slice.
func regionsFromConfig(cfg []config.RepeaterRegion) (named []*meshcore.Region, wildcardFlags uint8) {
	wildcardFlags = meshcore.RegionDenyFlood // no "*" entry ⇒ don't relay unscoped flood
	for _, rg := range cfg {
		if rg.Name == config.WildcardRegion {
			if rg.DenyFlood {
				wildcardFlags = meshcore.RegionDenyFlood
			} else {
				wildcardFlags = 0
			}
			continue
		}
		reg := meshcore.NewRegionFromKey(rg.Name, meshcore.DeriveRegionKey(rg.Name))
		if rg.DenyFlood {
			reg.Flags |= meshcore.RegionDenyFlood
		}
		named = append(named, reg)
	}
	return named, wildcardFlags
}

// ApplyRegions updates the running node's region set (and default advert
// scope) in place. The reload path uses it when ONLY regions/default-region
// changed, so such an edit doesn't restart the node (a restart would wipe the
// neighbour list, learned routes and relay counters). It reconciles the live
// RegionMap and the config snapshot that regionList/regionHas read.
func (r *Repeater) ApplyRegions(regions []config.RepeaterRegion, defaultRegion string) {
	named, wildcardFlags := regionsFromConfig(regions)
	rm := r.node.Regions()
	rm.SetWildcardFlags(wildcardFlags)

	want := make(map[string]*meshcore.Region, len(named))
	for _, rg := range named {
		want[rg.Name] = rg
	}
	// Drop regions no longer wanted; a deny-flood change is a remove+re-add (the
	// RegionMap has no in-place flag setter, and mutating a returned *Region
	// would race FindFloodMatch on the packet path).
	for _, cur := range rm.All() {
		w, keep := want[cur.Name]
		if !keep {
			rm.Remove(cur.Name)
			continue
		}
		if cur.Flags == w.Flags {
			delete(want, cur.Name) // unchanged — leave in place
		} else {
			rm.Remove(cur.Name) // re-added below with the new flags
		}
	}
	for _, rg := range want {
		rm.Add(rg)
	}

	r.mu.Lock()
	r.cfg.Regions = regions
	r.cfg.DefaultRegion = defaultRegion
	r.mu.Unlock()
}

func (r *Repeater) Name() string { return r.cfg.Name }

// rateLimiter is a sliding-window limiter mirroring the firmware's Limiter
// helper: at most max allows per window. A nil limiter never blocks (tests).
type rateLimiter struct {
	mu     sync.Mutex
	stamps []time.Time
	max    int
	window time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window}
}

func (l *rateLimiter) allow() bool {
	if l == nil {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.stamps[:0]
	for _, t := range l.stamps {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.stamps = kept
		return false
	}
	l.stamps = append(kept, now)
	return true
}
