package main

type CompanionConfig struct {
	Name    string `json:"name" yaml:"name" toml:"name"`
	KeyFile string `json:"keyFile" yaml:"keyFile" toml:"keyFile"`

	// Advert Data
	Latitude       *float64 `json:"latitude" yaml:"latitude" toml:"latitude"`
	Longitude      *float64 `json:"longitude" yaml:"longitude" toml:"longitude"`
	AdvertInterval *int     `json:"advertInterval,omitempty" yaml:"advertInterval,omitempty" toml:"advertInterval,omitempty"` // nil == default, 0 == off

	// Standalone channels (not tied to triggers)
	Channels *ChannelList `json:"channels,omitempty" yaml:"channels,omitempty" toml:"channels,omitempty"`

	// Bots
	Triggers *[]TriggerConfig `json:"triggers" yaml:"triggers" toml:"trigger"`

	// MQTT Out Config
	Mqtt *MqttConfig `json:"mqtt" yaml:"mqtt" toml:"mqtt"`
}

func (c *CompanionConfig) hasLatLon() bool {
	if c.Latitude == nil || c.Longitude == nil {
		return false
	}

	return *c.Latitude != 0 && *c.Longitude != 0
}
