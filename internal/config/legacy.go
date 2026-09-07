package config

import "strings"

// Pre-relational config format: `nodeType`, `[[bot]]`, `[[observer]]`, folded into the current fields by migrateLegacyFormat and then cleared.

// BotConfig is a legacy `[[bot]]` block; it folds into a CompanionConfig on import.
type BotConfig struct {
	Name     *string         `json:"name" yaml:"name" toml:"name"`
	Triggers []TriggerConfig `json:"triggers" yaml:"triggers" toml:"trigger"`
}

// legacyObserver is a legacy `[[observer]]` block; with one top-level MQTT feed now, the first observer wins.
type legacyObserver struct {
	Name           *string        `json:"name" yaml:"name" toml:"name"`
	IataCode       *string        `json:"iataCode" yaml:"iataCode" toml:"iataCode"`
	StatusInterval *int           `json:"statusInterval" yaml:"statusInterval" toml:"statusInterval"`
	Owner          *string        `json:"owner" yaml:"owner" toml:"owner"`
	Email          *string        `json:"email" yaml:"email" toml:"email"`
	Brokers        []BrokerConfig `json:"brokers" yaml:"brokers" toml:"broker"`
	Advert         *legacyAdvert  `json:"advert" yaml:"advert" toml:"advert"`
	// KeyFile named a standalone MQTT identity; MQTT now uses the selected companion's, so it is dropped.
	KeyFile *string `json:"keyFile" yaml:"keyFile" toml:"keyFile"`
}

// legacyAdvert is the observer's `[observer.advert]` position, mapped onto the companion it feeds.
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

// normalizeLegacyTransport rewrites the legacy ws/wss transports to "websockets"; wss also implies TLS.
func normalizeLegacyTransport(b *BrokerConfig) {
	switch strings.ToLower(b.Transport) {
	case "wss":
		b.Transport = "websockets"
		b.TlsEnabled = true
	case "ws":
		b.Transport = "websockets"
	}
}

// attachObserverToCompanion moves a legacy observer's identity and advert position onto the companion it names, creating one if none matches.
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
