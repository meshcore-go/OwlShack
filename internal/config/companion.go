package config

type CompanionConfig struct {
	// ID is the store's surrogate key, never serialized; 0 means not yet persisted.
	ID int64 `json:"-" yaml:"-" toml:"-"`

	Name string `json:"name" yaml:"name" toml:"name"`

	// PrivateKey is a hex ed25519 seed (64 hex chars); empty = generated when persisted.
	PrivateKey string `json:"privateKey,omitempty" yaml:"privateKey,omitempty" toml:"privateKey,omitempty"`

	// Deprecated: read and inlined into PrivateKey when a file config is imported.
	KeyFile string `json:"keyFile,omitempty" yaml:"keyFile,omitempty" toml:"keyFile,omitempty"`

	// Advert Data
	Latitude       *float64 `json:"latitude" yaml:"latitude" toml:"latitude"`
	Longitude      *float64 `json:"longitude" yaml:"longitude" toml:"longitude"`
	AdvertInterval *int     `json:"advertInterval,omitempty" yaml:"advertInterval,omitempty" toml:"advertInterval,omitempty"` // nil == default, 0 == off

	// Overrides Config.PathHashSize for this companion (bytes); nil == inherit, resolved at startup.
	PathHashSize *int `json:"pathHashSize,omitempty" yaml:"pathHashSize,omitempty" toml:"pathHashSize,omitempty"`

	// Standalone channels (not tied to triggers)
	Channels *ChannelList `json:"channels,omitempty" yaml:"channels,omitempty" toml:"channels,omitempty"`

	Triggers *[]TriggerConfig `json:"triggers,omitempty" yaml:"triggers,omitempty" toml:"trigger,omitempty"`

	// Deprecated: mqtt lives at the top level of Config; legacy blocks here are hoisted by ApplyDefaults.
	Mqtt *MqttConfig `json:"mqtt,omitempty" yaml:"mqtt,omitempty" toml:"mqtt,omitempty"`
}

func (c *CompanionConfig) HasLatLon() bool {
	if c.Latitude == nil || c.Longitude == nil {
		return false
	}

	return *c.Latitude != 0 && *c.Longitude != 0
}
