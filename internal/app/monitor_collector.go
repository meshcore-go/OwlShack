package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/client/repeater"
	"github.com/meshcore-go/OwlShack/internal/monitor"
	"github.com/meshcore-go/OwlShack/internal/node/companion"
	"github.com/meshcore-go/OwlShack/internal/store"
	"github.com/meshcore-go/OwlShack/internal/telemetry"
)

// Per-request timeouts for a monitor poll. Kept tighter than the interactive API
// path (10s) is generous enough; login (a flood round-trip) gets a bit more.
const (
	monitorLoginTimeout = 15 * time.Second
	monitorReqTimeout   = 12 * time.Second
	// monitorRequestGap spaces consecutive radio round-trips within one poll so
	// the repeater isn't hit with back-to-back requests — firing status,
	// telemetry and neighbours without a gap causes RF collisions and spurious
	// timeouts (the same requests succeed fine when spaced out).
	monitorRequestGap = 1500 * time.Millisecond
	// monitorProbeAttempts is how many times a best-effort probe (telemetry,
	// neighbours) is tried within one poll before giving up. RF round-trips drop
	// silently on collision/timeout, so a single in-poll retry (spaced by the
	// request gap) recovers most transient failures without waiting a full cycle.
	monitorProbeAttempts = 2
)

// gap pauses between sub-requests, returning early if the context is cancelled.
func gap(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(monitorRequestGap):
	}
}

// companionRegistry is a stable, reload-surviving handle to the current set of
// running companions, keyed by name. The monitor Service and its collectors are
// created once and live for the whole process, but the companion set is rebuilt
// on every SIGHUP reload / modem reconnect — so collectors resolve the live
// companion through this registry rather than capturing a slice.
type companionRegistry struct {
	mu     sync.RWMutex
	byName map[string]*companion.Companion
}

func newCompanionRegistry() *companionRegistry {
	return &companionRegistry{byName: map[string]*companion.Companion{}}
}

func (r *companionRegistry) set(companions []*companion.Companion) {
	m := make(map[string]*companion.Companion, len(companions))
	for _, c := range companions {
		m[c.Name()] = c
	}
	r.mu.Lock()
	r.byName = m
	r.mu.Unlock()
}

func (r *companionRegistry) find(name string) (*companion.Companion, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byName[name]
	return c, ok
}

// newContactLister builds a monitor.Lister that derives the current monitor
// targets from contact metadata: every running companion's contacts are scanned
// for the per-node monitor flag. Deduped by pubkey (first companion wins) so a
// node that's a contact of two companions isn't polled twice. Called once per
// poll cycle, so toggling monitoring in the UI takes effect on the next tick.
func newContactLister(reg *companionRegistry, db *store.Store) monitor.ListerFunc {
	return func(ctx context.Context) ([]monitor.Target, error) {
		reg.mu.RLock()
		comps := make(map[string]*companion.Companion, len(reg.byName))
		for n, c := range reg.byName {
			comps[n] = c
		}
		reg.mu.RUnlock()

		seen := make(map[string]bool)
		var targets []monitor.Target
		for name, c := range comps {
			contacts, err := db.Contacts.List(ctx, c.ID())
			if err != nil {
				// Don't silently drop a companion's nodes — that surfaces as a
				// misleading "not monitored" on a manual poll. Fail the listing so
				// the caller can report a retryable error (the scheduler just skips
				// the cycle and retries on the next tick).
				return nil, fmt.Errorf("listing contacts for %q: %w", name, err)
			}
			for _, ct := range contacts {
				if !ct.Metadata.Monitor {
					continue
				}
				key := hex.EncodeToString(ct.PeerPubKey)
				if seen[key] {
					continue
				}
				kind := monitorKind(c, ct)
				if kind == "" {
					continue
				}
				seen[key] = true
				targets = append(targets, monitor.Target{
					Pubkey:       ct.PeerPubKey,
					CompanionID:  name,
					Kind:         kind,
					IntervalSecs: ct.Metadata.MonitorIntervalSecs,
					RetrySecs:    ct.Metadata.MonitorRetrySecs,
					MaxRetries:   ct.Metadata.MonitorMaxRetries,
					Probes:       ct.Metadata.MonitorProbes,
				})
			}
		}
		return targets, nil
	}
}

// monitorKind classifies a monitored contact into a collector kind. The
// advertised peer type is authoritative when known: a stray isRepeater flag
// must not route a chat node to the repeater collector — companion firmware
// has no login handler, so that poll can only time out. The metadata flag is
// the fallback for repeaters whose advert hasn't been heard (type unknown).
func monitorKind(c *companion.Companion, ct store.Contact) string {
	var peerType string
	if c != nil {
		if id, err := meshcore.NewIdentityFromBytes(ct.PeerPubKey); err == nil {
			if p := c.Node().Peers().Lookup(id.PublicKey()); p != nil {
				peerType = strings.ToUpper(p.Type)
			}
		}
	}
	switch peerType {
	case "REPEATER":
		return "repeater"
	case "SENSOR":
		return "sensor"
	case "ROOM", "ROOM_SERVER":
		return "room"
	case "CHAT", "COMPANION":
		return "companion"
	}
	if ct.Metadata.IsRepeater {
		return "repeater"
	}
	return ""
}

// repeaterCollector polls a remote repeater for status, telemetry and
// neighbours, mapping everything onto the generic monitor.Reading time-series.
// It owns session lifecycle: it logs in (with the contact's stored password, or
// blank) when there's no session, and re-logs in once if a stale session causes
// the status request to fail (e.g. the repeater rebooted and forgot us).
type repeaterCollector struct {
	reg *companionRegistry
	db  *store.Store
	log *slog.Logger
}

func newRepeaterCollector(reg *companionRegistry, db *store.Store, log *slog.Logger) *repeaterCollector {
	if log == nil {
		log = slog.Default()
	}
	return &repeaterCollector{reg: reg, db: db, log: log.With("component", "repeater-collector")}
}

func (rc *repeaterCollector) Collect(ctx context.Context, t monitor.Target) (*monitor.CollectResult, error) {
	c, ok := rc.reg.find(t.CompanionID)
	if !ok {
		return nil, fmt.Errorf("companion %q is not running", t.CompanionID)
	}
	client := c.Repeaters()
	pubkeyHex := hex.EncodeToString(t.Pubkey)

	doStatus := probeEnabled(t.Probes, "status")
	doTelemetry := probeEnabled(t.Probes, "telemetry")
	doNeighbors := probeEnabled(t.Probes, "neighbors")

	hadSession := client.Session(pubkeyHex) != nil
	if !hadSession {
		if err := rc.login(ctx, client, t, c.ID()); err != nil {
			return nil, fmt.Errorf("login: %w", err)
		}
	}

	res := &monitor.CollectResult{Name: nodeName(c, t.Pubkey)}
	first := true // first radio probe drives stale-session recovery

	if doStatus {
		status, err := client.SendStatusReq(pubkeyHex, monitorReqTimeout)
		if err != nil && hadSession {
			// A pre-existing session went stale (repeater reboot / expiry):
			// drop it, re-login once, and retry the request.
			client.Logout(pubkeyHex)
			if lerr := rc.login(ctx, client, t, c.ID()); lerr != nil {
				return nil, fmt.Errorf("re-login after stale session: %w", lerr)
			}
			status, err = client.SendStatusReq(pubkeyHex, monitorReqTimeout)
		}
		if err != nil {
			return nil, fmt.Errorf("status: %w", err)
		}
		res.Readings = append(res.Readings, statusReadings(status)...)
		first = false
	}

	// Telemetry and neighbours are best-effort: an admin-gated or transient
	// failure shouldn't drop readings we already collected. Each is retried once
	// in-poll (see monitorProbeAttempts) since RF round-trips drop silently.
	if doTelemetry {
		if !first {
			gap(ctx)
		}
		if tel, terr := retryProbe(ctx, rc.log, "telemetry", pubkeyHex, func() (*telemetry.Telemetry, error) {
			return client.SendTelemetryReq(pubkeyHex, monitorReqTimeout)
		}); terr == nil {
			res.Readings = append(res.Readings, telemetryReadings(tel)...)
		} else {
			rc.log.Debug("telemetry poll failed", "pubkey", pubkeyHex[:12], "error", terr)
		}
		first = false
	}

	if doNeighbors {
		if !first {
			gap(ctx)
		}
		if nb, nerr := retryProbe(ctx, rc.log, "neighbors", pubkeyHex, func() (*repeater.Neighbors, error) {
			return client.SendNeighborsReq(pubkeyHex, 32, 0, monitorReqTimeout)
		}); nerr == nil {
			res.Readings = append(res.Readings, monitor.Reading{Metric: "neighbor_count", Value: float64(nb.TotalCount)})
			res.Neighbors = neighborSamples(nb)
		} else {
			rc.log.Debug("neighbors poll failed", "pubkey", pubkeyHex[:12], "error", nerr)
		}
	}

	return res, nil
}

// retryProbe runs a best-effort probe up to monitorProbeAttempts times, spacing
// retries by the request gap so a retried request doesn't collide with the one
// that just timed out. Returns the first success, or the last error. Aborts
// early if ctx is cancelled (the poll's per-request timeout elapsed).
func retryProbe[T any](ctx context.Context, log *slog.Logger, label, pubkeyHex string, fn func() (T, error)) (T, error) {
	var (
		zero T
		last error
	)
	for attempt := 1; attempt <= monitorProbeAttempts; attempt++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		last = err
		if attempt < monitorProbeAttempts {
			log.Debug(label+" probe failed, retrying", "pubkey", pubkeyHex[:12], "attempt", attempt, "error", err)
			gap(ctx)
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
		}
	}
	return zero, last
}

// probeEnabled reports whether a probe should run. An empty/nil set means "all"
// (the default for nodes whose monitoring hasn't been customised).
func probeEnabled(probes []string, name string) bool {
	if len(probes) == 0 {
		return true
	}
	for _, p := range probes {
		if p == name {
			return true
		}
	}
	return false
}

// login resolves the stored admin password from contact metadata (blank if
// none — some repeaters have no admin password) and authenticates.
func (rc *repeaterCollector) login(ctx context.Context, client *repeater.Client, t monitor.Target, companionID int64) error {
	password := ""
	if contact, err := rc.db.Contacts.Get(ctx, companionID, t.Pubkey); err == nil && contact != nil {
		password = contact.Metadata.RepeaterPassword
	}
	_, err := client.SendLogin(hex.EncodeToString(t.Pubkey), password, monitorLoginTimeout)
	return err
}

// nodeName returns a node's display name from the polling companion's peer
// table, or "" if unknown (in which case node_state keeps its previous name).
// Shared by all collectors.
func nodeName(c *companion.Companion, pubkey []byte) string {
	id, err := meshcore.NewIdentityFromBytes(pubkey)
	if err != nil {
		return ""
	}
	if p := c.Node().Peers().Lookup(id.PublicKey()); p != nil {
		return p.Name
	}
	return ""
}

// statusReadings maps the 18 repeater status fields to time-series readings. All
// are channel 0 (no LPP channel). SNR is already real dB; RSSI/noise are dBm.
func statusReadings(s *repeater.Status) []monitor.Reading {
	return []monitor.Reading{
		{Metric: "battery_mv", Value: float64(s.BatteryMV)},
		{Metric: "queue_len", Value: float64(s.QueueLen)},
		{Metric: "noise_floor", Value: float64(s.NoiseFloor)},
		{Metric: "last_rssi", Value: float64(s.LastRSSI)},
		{Metric: "last_snr", Value: s.LastSNR},
		{Metric: "packets_recv", Value: float64(s.PacketsRecv)},
		{Metric: "packets_sent", Value: float64(s.PacketsSent)},
		{Metric: "tx_air_secs", Value: float64(s.TxAirSecs)},
		{Metric: "rx_air_secs", Value: float64(s.RxAirSecs)},
		{Metric: "uptime", Value: float64(s.UptimeSecs)},
		{Metric: "flood_tx", Value: float64(s.FloodTx)},
		{Metric: "direct_tx", Value: float64(s.DirectTx)},
		{Metric: "flood_rx", Value: float64(s.FloodRx)},
		{Metric: "direct_rx", Value: float64(s.DirectRx)},
		{Metric: "err_events", Value: float64(s.ErrEvents)},
		{Metric: "recv_errors", Value: float64(s.RecvErrors)},
		{Metric: "direct_dups", Value: float64(s.DirectDups)},
		{Metric: "flood_dups", Value: float64(s.FloodDups)},
		{Metric: "chan_util", Value: s.ChanUtil},
	}
}

// telemetryReadings adapts a decoded telemetry payload into monitor time-series
// readings. The LPP decode, naming and channel-faithful keying live in the
// shared internal/telemetry package (reused across node types); this only maps
// the result onto monitor.Reading.
func telemetryReadings(t *telemetry.Telemetry) []monitor.Reading {
	metrics := t.Metrics()
	out := make([]monitor.Reading, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, monitor.Reading{Metric: m.Key, Channel: m.Channel, Value: m.Value})
	}
	return out
}

// neighborSamples converts the repeater's neighbour list to topology samples.
// Firmware reports SNR scaled x4 (quarter-dB); we store real dB.
func neighborSamples(nb *repeater.Neighbors) []monitor.NeighborSample {
	out := make([]monitor.NeighborSample, 0, len(nb.Neighbors))
	for _, n := range nb.Neighbors {
		prefix, err := hex.DecodeString(n.PubkeyPrefix)
		if err != nil {
			continue
		}
		snr := float64(n.SNR) / 4.0
		out = append(out, monitor.NeighborSample{Pubkey: prefix, SNR: &snr})
	}
	return out
}

// companionCollector polls a remote companion node for telemetry. Companions
// answer sessionless contact-telemetry requests (ECDH-encrypted with the polling
// companion's identity — no login) but not the repeater status/neighbours admin
// requests, so this collector gathers telemetry only. It reuses the shared
// internal/telemetry decode + keying, so a companion's battery/temperature land
// in the same series shape as a repeater's.
type companionCollector struct {
	reg *companionRegistry
	db  *store.Store
	log *slog.Logger
}

func newCompanionCollector(reg *companionRegistry, log *slog.Logger) *companionCollector {
	if log == nil {
		log = slog.Default()
	}
	return &companionCollector{reg: reg, log: log.With("component", "companion-collector")}
}

func (cc *companionCollector) Collect(ctx context.Context, t monitor.Target) (*monitor.CollectResult, error) {
	c, ok := cc.reg.find(t.CompanionID)
	if !ok {
		return nil, fmt.Errorf("companion %q is not running", t.CompanionID)
	}
	res := &monitor.CollectResult{Name: nodeName(c, t.Pubkey)}

	// Telemetry is all a companion answers; an empty/nil probe set means "all".
	if !probeEnabled(t.Probes, "telemetry") {
		return res, nil
	}

	pubkeyHex := hex.EncodeToString(t.Pubkey)
	tel, err := retryProbe(ctx, cc.log, "telemetry", pubkeyHex, func() (*telemetry.Telemetry, error) {
		return c.Repeaters().SendContactTelemetryReq(pubkeyHex, monitorReqTimeout)
	})
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	res.Readings = telemetryReadings(tel)
	return res, nil
}

// ensure the collectors satisfy the monitor seam.
var (
	_ monitor.Collector = (*repeaterCollector)(nil)
	_ monitor.Collector = (*companionCollector)(nil)
)
