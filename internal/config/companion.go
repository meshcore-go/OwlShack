package config

type CompanionConfig struct {
	Name string `json:"name" yaml:"name" toml:"name"`

	// PrivateKey is the companion's identity as a hex ed25519 seed (64 hex
	// chars). Empty = generated when the config is persisted.
	PrivateKey string `json:"privateKey,omitempty" yaml:"privateKey,omitempty" toml:"privateKey,omitempty"`

	// Deprecated: identities live inline in PrivateKey now. Key files named
	// here are read and inlined when a file config is imported.
	KeyFile string `json:"keyFile,omitempty" yaml:"keyFile,omitempty" toml:"keyFile,omitempty"`

	// Advert Data
	Latitude       *float64 `json:"latitude" yaml:"latitude" toml:"latitude"`
	Longitude      *float64 `json:"longitude" yaml:"longitude" toml:"longitude"`
	AdvertInterval *int     `json:"advertInterval,omitempty" yaml:"advertInterval,omitempty" toml:"advertInterval,omitempty"` // nil == default, 0 == off

	// Standalone channels (not tied to triggers)
	Channels *ChannelList `json:"channels,omitempty" yaml:"channels,omitempty" toml:"channels,omitempty"`

	// Bots
	Triggers *[]TriggerConfig `json:"triggers,omitempty" yaml:"triggers,omitempty" toml:"trigger,omitempty"`

	// Deprecated: mqtt lives at the top level of Config (one observer, one
	// node, selected by mqtt.node). Legacy blocks here are hoisted by
	// ApplyDefaults; at runtime the field is only set by startCompanions for
	// the selected node.
	Mqtt *MqttConfig `json:"mqtt,omitempty" yaml:"mqtt,omitempty" toml:"mqtt,omitempty"`
}

func (c *CompanionConfig) HasLatLon() bool {
	if c.Latitude == nil || c.Longitude == nil {
		return false
	}

	return *c.Latitude != 0 && *c.Longitude != 0
}
