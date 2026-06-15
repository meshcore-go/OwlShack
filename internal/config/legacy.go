package config

import "strings"

// Legacy config-file format. The pre-relational "main" deployment used
// `nodeType`, `[[bot]]`/`[[bot.trigger]]`, and `[[observer]]`/`[observer.broker]`
// where the current schema uses `connectionType`, `[[companion]]`, and a single
// top-level `[mqtt]`. The alias fields on Config (NodeType / Bots / Observers)
// plus the types here let an existing deployed config import without losing the
// bot or the MQTT feed. migrateLegacyFormat folds them into the canonical
// fields (called first in ApplyDefaults) and then clears them, so a config
// assembled from the relational tables — which never sets these — is a no-op.

// BotConfig is a legacy `[[bot]]` block: a named node with triggers. It folds
// into a CompanionConfig on import; new configs use `[[companion]]`.
type BotConfig struct {
	Name     *string         `json:"name" yaml:"name" toml:"name"`
	Triggers []TriggerConfig `json:"triggers" yaml:"triggers" toml:"trigger"`
}

// legacyObserver is a legacy `[[observer]]` block. The deployed format allowed
// several; the current model has one top-level MQTT feed, so the first observer
// wins (matching the legacy per-companion `[companion.mqtt]` hoisting rule).
type legacyObserver struct {
	Name           *string        `json:"name" yaml:"name" toml:"name"`
	IataCode       *string        `json:"iataCode" yaml:"iataCode" toml:"iataCode"`
	StatusInterval *int           `json:"statusInterval" yaml:"statusInterval" toml:"statusInterval"`
	Owner          *string        `json:"owner" yaml:"owner" toml:"owner"`
	Email          *string        `json:"email" yaml:"email" toml:"email"`
	Brokers        []BrokerConfig `json:"brokers" yaml:"brokers" toml:"broker"`
	Advert         *legacyAdvert  `json:"advert" yaml:"advert" toml:"advert"`
	// KeyFile named a standalone MQTT identity. The relational model feeds MQTT
	// from the selected companion's identity, so it has no equivalent — dropped.
	KeyFile *string `json:"keyFile" yaml:"keyFile" toml:"keyFile"`
}

// legacyAdvert is the observer's `[observer.advert]` position broadcast. It maps
// onto the companion the observer feeds (that companion now carries the on-mesh
// identity + position).
type legacyAdvert struct {
	Enabled  bool     `json:"enabled" yaml:"enabled" toml:"enabled"`
	Interval *int     `json:"interval,omitempty" yaml:"interval,omitempty" toml:"interval,omitempty"`
	Lat      *float64 `json:"lat,omitempty" yaml:"lat,omitempty" toml:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty" yaml:"lon,omitempty" toml:"lon,omitempty"`
}

func (c *Config) migrateLegacyFormat() {
	// nodeType -> connectionType (only when the new key wasn't given).
	if (c.ConnectionType == nil || *c.ConnectionType == "") && c.NodeType != nil && *c.NodeType != "" {
		ct := *c.NodeType
		c.ConnectionType = &ct
	}
	c.NodeType = nil

	// [[bot]] -> companions (a bot is a companion with just a name + triggers).
	for _, b := range c.Bots {
		comp := CompanionConfig{}
		if b.Name != nil {
			comp.Name = *b.Name
		}
		if len(b.Triggers) > 0 {
			trigs := b.Triggers
			comp.Triggers = &trigs
		}
		c.Companions = append(c.Companions, comp)
	}
	c.Bots = nil

	// [[observer]] -> the single top-level mqtt block (first observer wins).
	if c.Mqtt == nil && len(c.Observers) > 0 {
		obs := c.Observers[0]
		for i := range obs.Brokers {
			normalizeLegacyTransport(&obs.Brokers[i])
		}
		c.Mqtt = &MqttConfig{
			Node:           obs.Name,
			IataCode:       obs.IataCode,
			StatusInterval: obs.StatusInterval,
			Owner:          obs.Owner,
			Email:          obs.Email,
			Brokers:        obs.Brokers,
		}
		if obs.Name != nil && *obs.Name != "" {
			attachObserverToCompanion(c, *obs.Name, obs)
		}
	}
	c.Observers = nil
}

// normalizeLegacyTransport rewrites the legacy ws/wss broker transports to the
// canonical "websockets" (the runtime client and BrokerConfig.Validate only
// speak tcp/websockets); wss also implies TLS.
func normalizeLegacyTransport(b *BrokerConfig) {
	switch strings.ToLower(b.Transport) {
	case "wss":
		b.Transport = "websockets"
		b.TlsEnabled = true
	case "ws":
		b.Transport = "websockets"
	}
}

// attachObserverToCompanion binds a legacy observer to the companion it feeds,
// matched by name: the observer's standalone keyFile identity becomes that
// companion's (read + inlined later by MigrateKeyFiles) and its advert position
// carries over, without overriding values the companion already sets. When no
// companion matches the observer's name, an observer-only companion is created
// so the mqtt node reference resolves and the identity/position are preserved.
func attachObserverToCompanion(c *Config, name string, obs legacyObserver) {
	var comp *CompanionConfig
	for i := range c.Companions {
		if c.Companions[i].Name == name {
			comp = &c.Companions[i]
			break
		}
	}
	if comp == nil {
		c.Companions = append(c.Companions, CompanionConfig{Name: name})
		comp = &c.Companions[len(c.Companions)-1]
	}

	if comp.PrivateKey == "" && comp.KeyFile == "" && obs.KeyFile != nil {
		comp.KeyFile = *obs.KeyFile
	}
	if obs.Advert != nil {
		if comp.Latitude == nil {
			comp.Latitude = obs.Advert.Lat
		}
		if comp.Longitude == nil {
			comp.Longitude = obs.Advert.Lon
		}
		if comp.AdvertInterval == nil && obs.Advert.Enabled && obs.Advert.Interval != nil {
			comp.AdvertInterval = obs.Advert.Interval
		}
	}
}
