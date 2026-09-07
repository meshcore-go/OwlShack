package config

import (
	"fmt"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// RepeaterConfig is a repeater node personality the bot RUNS on the mesh: it
// relays flood/direct packets, advertises as a REPEATER, tracks neighbours and
// (Phase 2) answers login/status/CLI admin requests. It is a sibling of
// CompanionConfig, not a variant — a repeater has no channels, triggers or DMs,
// but carries relay policy and admin credentials a companion never needs.
//
// Fields mirror the firmware NodePrefs subset a repeater actually uses (see
// examples/simple_repeater). Relay-policy pointers are nil == "use default";
// the *Or accessors resolve the effective value so the node code stays clean.
type RepeaterConfig struct {
	Name string `json:"name" yaml:"name" toml:"name"`

	// PrivateKey is the repeater's identity as a hex ed25519 seed (64 hex
	// chars). Empty == generated when the config is persisted.
	PrivateKey string `json:"privateKey,omitempty" yaml:"privateKey,omitempty" toml:"privateKey,omitempty"`

	// Advert data. AdvertInterval is the local (zero-hop) advert period in
	// seconds; FloodAdvertInterval the mesh-wide flood advert period. nil ==
	// default, 0 == off (matches CompanionConfig.AdvertInterval semantics).
	Latitude            *float64 `json:"latitude" yaml:"latitude" toml:"latitude"`
	Longitude           *float64 `json:"longitude" yaml:"longitude" toml:"longitude"`
	AdvertInterval      *int     `json:"advertInterval,omitempty" yaml:"advertInterval,omitempty" toml:"advertInterval,omitempty"`
	FloodAdvertInterval *int     `json:"floodAdvertInterval,omitempty" yaml:"floodAdvertInterval,omitempty" toml:"floodAdvertInterval,omitempty"`

	// Relay policy (mirrors firmware allowPacketForward / NodePrefs).
	DisableFwd       *bool   `json:"disableFwd,omitempty" yaml:"disableFwd,omitempty" toml:"disableFwd,omitempty"`
	FloodMax         *int    `json:"floodMax,omitempty" yaml:"floodMax,omitempty" toml:"floodMax,omitempty"`                         // max flood path hops to relay (firmware default 64)
	FloodMaxUnscoped *int    `json:"floodMaxUnscoped,omitempty" yaml:"floodMaxUnscoped,omitempty" toml:"floodMaxUnscoped,omitempty"` // extra hop cap for PLAIN floods only, 0 = never relay them (firmware default 64)
	FloodMaxAdvert   *int    `json:"floodMaxAdvert,omitempty" yaml:"floodMaxAdvert,omitempty" toml:"floodMaxAdvert,omitempty"`       // advert-specific hop cap (firmware default 8)
	LoopDetect       *string `json:"loopDetect,omitempty" yaml:"loopDetect,omitempty" toml:"loopDetect,omitempty"`                   // off|minimal|moderate|strict

	// DefaultRegion names the region our own flood adverts are scoped to
	// (firmware default_scope). Empty = unscoped flood advert, which is valid
	// firmware behaviour. Must name a configured non-wildcard region when set.
	DefaultRegion string `json:"defaultRegion,omitempty" yaml:"defaultRegion,omitempty" toml:"defaultRegion,omitempty"`

	// HomeRegion labels one region as this node's home (firmware home_id). The
	// firmware stores and reports it but never routes on it; so do we. Must
	// name a configured region when set.
	HomeRegion string `json:"homeRegion,omitempty" yaml:"homeRegion,omitempty" toml:"homeRegion,omitempty"`

	// PathHashSize overrides the global Config.PathHashSize for this repeater:
	// the per-hop path hash width in BYTES for the flood packets it originates.
	// nil == inherit (resolved at startup). The firmware's `path.hash.mode` is
	// this minus one — convert only at that CLI boundary.
	PathHashSize *int `json:"pathHashSize,omitempty" yaml:"pathHashSize,omitempty" toml:"pathHashSize,omitempty"`

	// Relay timing (firmware NodePrefs tx_delay_factor / direct_tx_delay_factor /
	// rx_delay_base / multi_acks). nil == firmware default.
	TxDelayFactor       *float64 `json:"txDelayFactor,omitempty" yaml:"txDelayFactor,omitempty" toml:"txDelayFactor,omitempty"`
	DirectTxDelayFactor *float64 `json:"directTxDelayFactor,omitempty" yaml:"directTxDelayFactor,omitempty" toml:"directTxDelayFactor,omitempty"`
	RxDelayBase         *float64 `json:"rxDelayBase,omitempty" yaml:"rxDelayBase,omitempty" toml:"rxDelayBase,omitempty"`
	MultiAcks           *int     `json:"multiAcks,omitempty" yaml:"multiAcks,omitempty" toml:"multiAcks,omitempty"`

	// Admin surface (Phase 2: login / CLI). Blank admin password is allowed
	// (some repeaters have none). OwnerInfo is advertised in owner-info replies.
	AdminPassword string `json:"adminPassword,omitempty" yaml:"adminPassword,omitempty" toml:"adminPassword,omitempty"`
	GuestPassword string `json:"guestPassword,omitempty" yaml:"guestPassword,omitempty" toml:"guestPassword,omitempty"`
	OwnerInfo     string `json:"ownerInfo,omitempty" yaml:"ownerInfo,omitempty" toml:"ownerInfo,omitempty"`

	// Regions the repeater participates in: it relays transport-flood packets
	// whose scope matches one of these. The transport key is derived from the
	// region name (SHA256(name)[:16], firmware getAutoKeyFor) — not a hashtag.
	Regions []RepeaterRegion `json:"regions,omitempty" yaml:"regions,omitempty" toml:"region,omitempty"`
}

// RepeaterRegion is one transport scope the repeater serves. DenyFlood excludes
// the region from flood relaying (known but not re-flooded); default false =
// relay its scoped flood.
type RepeaterRegion struct {
	Name      string `json:"name" yaml:"name" toml:"name"`
	DenyFlood bool   `json:"denyFlood,omitempty" yaml:"denyFlood,omitempty" toml:"denyFlood,omitempty"`
}

// WildcardRegion ("*") is the unscoped flood scope: plain FLOOD packets that
// carry no transport code. Firmware models it as a permanent, implicit struct;
// we model it as an ordinary editable Regions entry so it can be listed,
// toggled, deleted (stop relaying unscoped flood) and re-added from the UI/CLI.
// Seeded into a new repeater on create; its absence means unscoped flood is not
// relayed (see regionsFromConfig).
const WildcardRegion = "*"

// validateRegionName checks a region name against the firmware's constraints
// (RegionMap::is_name_char, MAX_REGION_NAME): non-empty, within length, and
// only allowed characters. The wildcard "*" is exempt (it's not a named
// transport scope, so the firmware's is_name_char rules don't apply).
func validateRegionName(name string) error {
	if name == WildcardRegion {
		return nil
	}
	if name == "" {
		return fmt.Errorf("region name is required")
	}
	if len(name) > meshcore.MaxRegionName {
		return fmt.Errorf("region name %q exceeds %d chars", name, meshcore.MaxRegionName)
	}
	for i := 0; i < len(name); i++ {
		if !meshcore.IsValidRegionNameChar(name[i]) {
			return fmt.Errorf("region name %q has an invalid character %q", name, name[i])
		}
	}
	return nil
}

// Firmware defaults for relay policy (examples/simple_repeater MyMesh.cpp ctor).
const (
	DefaultFloodMax         = 64
	DefaultFloodMaxUnscoped = 64
	DefaultFloodMaxAdvert   = 8
	DefaultLoopDetect       = "off"
	// DefaultFloodAdvertIntervalSecs is the flood self-advert period used when
	// none is configured. Matches the firmware default (MyMesh.cpp:
	// flood_advert_interval = 47 hours) — flood adverts propagate mesh-wide, so
	// they're deliberately infrequent. Zero-hop adverts default OFF (0), also
	// matching the firmware: its 2-min new-install default is auto-disabled on
	// first config (MIN_LOCAL_ADVERT_INTERVAL = 60 min), and our repeater is
	// always configured. Kept here so the advert loop and the over-mesh
	// `get flood.advert.interval` report the same effective value.
	DefaultFloodAdvertIntervalSecs = 47 * 60 * 60

	// Advert interval bounds, in seconds, mirroring the firmware CommonCLI
	// ranges: zero-hop is 0 (off) or 60-240 minutes (MIN_LOCAL_ADVERT_INTERVAL),
	// flood is 0 (off) or 3-168 hours. Stored in seconds because that is what
	// the advert loop schedules on; the CLI converts to the firmware's units.
	MinAdvertIntervalSecs      = 60 * 60
	MaxAdvertIntervalSecs      = 240 * 60
	MinFloodAdvertIntervalSecs = 3 * 60 * 60
	MaxFloodAdvertIntervalSecs = 168 * 60 * 60

	// Path hash width bounds in bytes. The firmware's `set path.hash.mode`
	// accepts 0-2 (it checks `mode < 3`) and uses `path_hash_mode + 1` as the
	// width, so 1-3 bytes. The wire field could carry 4, but nothing in the
	// ecosystem selects it.
	DefaultPathHashSize = 1
	MinPathHashSize     = 1
	MaxPathHashSize     = 3

	// Firmware simple_repeater defaults and CommonCLI set-ranges for the timing knobs.
	DefaultTxDelayFactor       = 0.5
	DefaultDirectTxDelayFactor = 0.3
	MaxTxDelayFactor           = 2.0
	MaxRxDelayBase             = 20.0
)

// Valid loop-detect levels, matching the firmware's LOOP_DETECT_* enum.
var loopDetectLevels = map[string]bool{"off": true, "minimal": true, "moderate": true, "strict": true}

// IsValidLoopDetect reports whether s is a known loop-detect level. Shared by
// config validation and the repeater's over-mesh `set loop.detect` handler.
func IsValidLoopDetect(s string) bool { return loopDetectLevels[s] }

func (c *RepeaterConfig) HasLatLon() bool {
	if c.Latitude == nil || c.Longitude == nil {
		return false
	}
	return *c.Latitude != 0 && *c.Longitude != 0
}

// FloodMaxOr / FloodMaxAdvertOr / LoopDetectOr / IsFwdDisabled resolve the
// effective relay-policy value, applying firmware defaults for nil fields.
func (c *RepeaterConfig) FloodMaxOr() int {
	if c.FloodMax == nil {
		return DefaultFloodMax
	}
	return *c.FloodMax
}

func (c *RepeaterConfig) FloodMaxAdvertOr() int {
	if c.FloodMaxAdvert == nil {
		return DefaultFloodMaxAdvert
	}
	return *c.FloodMaxAdvert
}

func (c *RepeaterConfig) FloodMaxUnscopedOr() int {
	if c.FloodMaxUnscoped == nil {
		return DefaultFloodMaxUnscoped
	}
	return *c.FloodMaxUnscoped
}

func (c *RepeaterConfig) LoopDetectOr() string {
	if c.LoopDetect == nil || *c.LoopDetect == "" {
		return DefaultLoopDetect
	}
	return *c.LoopDetect
}

func (c *RepeaterConfig) IsFwdDisabled() bool {
	return c.DisableFwd != nil && *c.DisableFwd
}

// PathHashSizeOr resolves the effective path hash width in bytes (1 by default).
func (c *RepeaterConfig) PathHashSizeOr() int {
	if c.PathHashSize == nil {
		return DefaultPathHashSize
	}
	return *c.PathHashSize
}

func (c *RepeaterConfig) TxDelayFactorOr() float64 {
	if c.TxDelayFactor == nil {
		return DefaultTxDelayFactor
	}
	return *c.TxDelayFactor
}

func (c *RepeaterConfig) DirectTxDelayFactorOr() float64 {
	if c.DirectTxDelayFactor == nil {
		return DefaultDirectTxDelayFactor
	}
	return *c.DirectTxDelayFactor
}

// RxDelayBaseOr is the firmware rx_delay_base; 0 (the default) disables the receive delay.
func (c *RepeaterConfig) RxDelayBaseOr() float64 {
	if c.RxDelayBase == nil {
		return 0
	}
	return *c.RxDelayBase
}

func (c *RepeaterConfig) MultiAcksOr() int {
	if c.MultiAcks == nil {
		return 0
	}
	return *c.MultiAcks
}

// AdvertIntervalOr / FloodAdvertIntervalOr resolve the effective self-advert
// periods in SECONDS, applying defaults for nil (zero-hop off; flood 30 min).
// The advert loop and the over-mesh `get advert.interval` share these so a
// client fetching intervals sees the values actually in effect, not 0.
func (c *RepeaterConfig) AdvertIntervalOr() int {
	if c.AdvertInterval == nil {
		return 0 // zero-hop advert default: off
	}
	return *c.AdvertInterval
}

func (c *RepeaterConfig) FloodAdvertIntervalOr() int {
	if c.FloodAdvertInterval == nil {
		return DefaultFloodAdvertIntervalSecs
	}
	return *c.FloodAdvertInterval
}
