package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type ChannelRef struct {
	Name       string `json:"name" yaml:"name" toml:"name"`
	PrivateKey string `json:"privateKey,omitempty" yaml:"privateKey,omitempty" toml:"privateKey,omitempty"`
}

func (cr *ChannelRef) UnmarshalText(text []byte) error {
	cr.Name = string(text)
	return nil
}

type ChannelList []ChannelRef

func (cl *ChannelList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("channels must be an array: %w", err)
	}

	result := make(ChannelList, 0, len(raw))
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			result = append(result, ChannelRef{Name: s})
			continue
		}
		// Decode into a tag-equivalent struct: ChannelRef implements
		// encoding.TextUnmarshaler (for TOML string entries), which makes
		// encoding/json demand a string and reject the object form.
		var ref struct {
			Name       string `json:"name"`
			PrivateKey string `json:"privateKey"`
		}
		if err := json.Unmarshal(item, &ref); err != nil {
			return fmt.Errorf("channel entry must be a string or {name, privateKey} object: %w", err)
		}
		result = append(result, ChannelRef{Name: ref.Name, PrivateKey: ref.PrivateKey})
	}
	*cl = result
	return nil
}

func (cl *ChannelList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("channels must be a sequence")
	}

	result := make(ChannelList, 0, len(value.Content))
	for _, node := range value.Content {
		switch node.Kind {
		case yaml.ScalarNode:
			result = append(result, ChannelRef{Name: node.Value})
		case yaml.MappingNode:
			var ref ChannelRef
			if err := node.Decode(&ref); err != nil {
				return fmt.Errorf("channel entry decode error: %w", err)
			}
			result = append(result, ref)
		default:
			return fmt.Errorf("channel entry must be a string or mapping")
		}
	}
	*cl = result
	return nil
}

type Config struct {
	// Logging
	LogLevel *string `json:"logLevel" yaml:"logLevel" toml:"logLevel"`

	// ConnectionType selects the radio backend. "kiss" today; extensible
	// (e.g. "sx1262_hat") later. Empty defaults to "kiss".
	ConnectionType *string `json:"connectionType,omitempty" yaml:"connectionType,omitempty" toml:"connectionType,omitempty"`

	// Connection Settings (KISS modem)
	Connection *string `json:"connection" yaml:"connection" toml:"connection"` // serial://<path> or tcp://<host:port>
	BaudRate   *int    `json:"baudRate" yaml:"baudRate" toml:"baudRate"`       // Default 115200 if using serial

	// Radio Settings
	Freq *float64 `json:"freq" yaml:"freq" toml:"freq"` // e.g. 917.375
	Bw   *float64 `json:"bw" yaml:"bw" toml:"bw"`       // e.g. 62.50
	SF   *uint8   `json:"sf" yaml:"sf" toml:"sf"`       // e.g. 7
	CR   *uint8   `json:"cr" yaml:"cr" toml:"cr"`       // e.g. 8
	TX   *uint8   `json:"tx" yaml:"tx" toml:"tx"`       // TX Power e.g. 22

	// Web UI
	ListenAddr *string `json:"listenAddr" yaml:"listenAddr" toml:"listenAddr"`

	// SetupComplete is nil/false until the first-run web wizard finishes. A
	// fresh bootstrap leaves it false (so the UI shows the setup wizard);
	// imported configs are marked complete. Lets us tell "never configured"
	// apart from "deliberately observer-only" when there are no companions.
	SetupComplete *bool `json:"setupComplete,omitempty" yaml:"setupComplete,omitempty" toml:"setupComplete,omitempty"`

	// MQTT observer. Exactly one node feeds MQTT (Mqtt.Node selects it).
	Mqtt *MqttConfig `json:"mqtt,omitempty" yaml:"mqtt,omitempty" toml:"mqtt,omitempty"`

	// Companions
	Companions []CompanionConfig `json:"companions" yaml:"companions" toml:"companion"`

	// Legacy import aliases from the pre-relational "main" deployment format
	// (nodeType / [[bot]] / [[observer]]). migrateLegacyFormat folds these into
	// ConnectionType / Companions / Mqtt and clears them — see legacy.go.
	NodeType  *string          `json:"nodeType,omitempty" yaml:"nodeType,omitempty" toml:"nodeType,omitempty"`
	Bots      []BotConfig      `json:"bots,omitempty" yaml:"bots,omitempty" toml:"bot,omitempty"`
	Observers []legacyObserver `json:"observers,omitempty" yaml:"observers,omitempty" toml:"observer,omitempty"`
}

func DefaultConfig() Config {
	connection := "serial:///dev/ttyACM0"
	baudRate := 115200
	freq := 917.375
	bw := 62.50
	sf := uint8(7)
	cr := uint8(8)
	tx := uint8(22)

	return Config{
		Connection: &connection,
		BaudRate:   &baudRate,
		Freq:       &freq,
		Bw:         &bw,
		SF:         &sf,
		CR:         &cr,
		TX:         &tx,
	}
}

// PublicChannelName is the well-known public channel every companion joins.
const PublicChannelName = "Public"

// ensureTriggerChannels guarantees every channel a trigger references also
// exists in the companion's channel list. Channels are owned at the companion
// level; triggers only reference them by name. This keeps the model consistent
// and migrates older configs where a trigger named a channel the companion's
// channel list didn't include. A referenced channel's private key (if any) is
// carried over so encrypted channels keep working.
func ensureTriggerChannels(comp *CompanionConfig) {
	if comp.Triggers == nil {
		return
	}
	// Channel names are case-sensitive: a hashtag channel's key is SHA256 of the
	// exact "#name", so "#Foo" and "#foo" are different channels on the air
	// (matches the firmware and meshcore-go, which never case-fold names).
	have := map[string]bool{}
	if comp.Channels != nil {
		for _, ch := range *comp.Channels {
			have[ch.Name] = true
		}
	}
	for _, t := range *comp.Triggers {
		if t.Channels == nil {
			continue
		}
		for _, ref := range *t.Channels {
			if strings.TrimSpace(ref.Name) == "" || have[ref.Name] {
				continue
			}
			have[ref.Name] = true
			if comp.Channels == nil {
				comp.Channels = &ChannelList{}
			}
			*comp.Channels = append(*comp.Channels, ChannelRef{Name: ref.Name, PrivateKey: ref.PrivateKey})
		}
	}
}

// ensurePublicChannel guarantees a companion is a member of the public channel.
// Every companion must be in Public; this normalises configs from the wizard,
// the add-companion form, imports, and hand-crafted PUTs alike.
func ensurePublicChannel(comp *CompanionConfig) {
	if comp.Channels == nil {
		comp.Channels = &ChannelList{{Name: PublicChannelName}}
		return
	}
	for _, ch := range *comp.Channels {
		if ch.Name == PublicChannelName {
			return
		}
	}
	*comp.Channels = append(ChannelList{{Name: PublicChannelName}}, *comp.Channels...)
}

func (c *Config) ApplyDefaults() {
	// Fold pre-relational legacy keys (nodeType / [[bot]] / [[observer]]) into the
	// current fields first, so the folded companions/mqtt go through the normal
	// normalization below (Public channel, key generation, topicPrefix migration).
	c.migrateLegacyFormat()

	defaults := DefaultConfig()
	// Normalise to a non-nil slice so JSON serialises companions as [] not
	// null; the frontend (and its TS contract) treats companions as an array.
	if c.Companions == nil {
		c.Companions = []CompanionConfig{}
	}
	if c.ConnectionType == nil || *c.ConnectionType == "" {
		kiss := "kiss"
		c.ConnectionType = &kiss
	}
	// nil and false both mean "setup not finished" (only true matters); pin it to
	// false so the value is representable in the relational schema and consistent
	// across GET/DB/runtime.
	if c.SetupComplete == nil {
		f := false
		c.SetupComplete = &f
	}
	if c.Connection == nil {
		c.Connection = defaults.Connection
	}
	if c.BaudRate == nil {
		c.BaudRate = defaults.BaudRate
	}
	if c.Freq == nil {
		c.Freq = defaults.Freq
	}
	if c.Bw == nil {
		c.Bw = defaults.Bw
	}
	if c.SF == nil {
		c.SF = defaults.SF
	}
	if c.CR == nil {
		c.CR = defaults.CR
	}
	if c.TX == nil {
		c.TX = defaults.TX
	}

	// Migrate legacy per-companion mqtt blocks to the single top-level block:
	// the first one wins and its companion becomes the selected node.
	for i := range c.Companions {
		comp := &c.Companions[i]
		ensureTriggerChannels(comp)
		ensurePublicChannel(comp)
		if comp.Mqtt == nil {
			continue
		}
		if c.Mqtt == nil {
			hoisted := *comp.Mqtt
			name := comp.Name
			hoisted.Node = &name
			c.Mqtt = &hoisted
		}
		comp.Mqtt = nil
	}

	if c.Mqtt != nil {
		for i := range c.Mqtt.Brokers {
			c.Mqtt.Brokers[i].migrateTopicPrefix()
		}
	}
}

func unmarshalConfig(data []byte, fn func([]byte, any) error) (*Config, error) {
	var cfg Config
	if err := fn(data, &cfg); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	return &cfg, nil
}

func UnmarshalConfigJson(data []byte) (*Config, error) { return unmarshalConfig(data, json.Unmarshal) }
func UnmarshalConfigYaml(data []byte) (*Config, error) { return unmarshalConfig(data, yaml.Unmarshal) }
func UnmarshalConfigToml(data []byte) (*Config, error) { return unmarshalConfig(data, toml.Unmarshal) }
