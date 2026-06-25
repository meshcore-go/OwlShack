package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type MqttConfig struct {
	// Node names the configured node (companion) whose identity feeds the
	// observer. Empty = the first companion. Only one node publishes to MQTT;
	// the block lives at the top level of the config (legacy per-companion
	// blocks are hoisted there on load).
	Node    *string `json:"node,omitempty" yaml:"node,omitempty" toml:"node,omitempty"`
	Enabled *bool   `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"` // nil = enabled

	IataCode       *string        `json:"iataCode" yaml:"iataCode" toml:"iataCode"`
	StatusInterval *int           `json:"statusInterval" yaml:"statusInterval" toml:"statusInterval"`
	Owner          *string        `json:"owner" yaml:"owner" toml:"owner"`
	Email          *string        `json:"email" yaml:"email" toml:"email"`
	Brokers        []BrokerConfig `json:"brokers" yaml:"brokers" toml:"broker"`
}

func (c *MqttConfig) IsEnabled() bool {
	return c != nil && (c.Enabled == nil || *c.Enabled)
}

type BrokerConfig struct {
	Name      string `json:"name" yaml:"name" toml:"name"`
	Enabled   bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Dedup     bool   `json:"dedup" yaml:"dedup" toml:"dedup"`             // Do we enable dedup checks
	Transport string `json:"transport" yaml:"transport" toml:"transport"` // websockets or tcp
	Host      string `json:"host" yaml:"host" toml:"host"`
	Port      int    `json:"port" yaml:"port" toml:"port"`
	// Deprecated: folded into the topic templates by ApplyDefaults.
	TopicPrefix string `json:"topicPrefix,omitempty" yaml:"topicPrefix,omitempty" toml:"topicPrefix,omitempty"`
	// Topic templates. Placeholders: {iata} {pubkey} {name} —
	// meshcoretomqtt-style uppercase tokens ({IATA}, {PUBLIC_KEY}) also work.
	// Empty = "meshcore/{iata}/{pubkey}/packets" (resp. "/status").
	PacketTopic           string   `json:"packetTopic,omitempty" yaml:"packetTopic,omitempty" toml:"packetTopic,omitempty"`
	StatusTopic           string   `json:"statusTopic,omitempty" yaml:"statusTopic,omitempty" toml:"statusTopic,omitempty"`
	DisallowedPacketTypes []string `json:"disallowedPacketTypes" yaml:"disallowedPacketTypes" toml:"disallowedPacketTypes"`
	RetainStatus          bool     `json:"retainStatus" yaml:"retainStatus" toml:"retainStatus"`
	TlsEnabled            bool     `json:"tlsEnabled" yaml:"tlsEnabled" toml:"tlsEnabled"`
	TlsInsecure           bool     `json:"tlsInsecure" yaml:"tlsInsecure" toml:"tlsInsecure"`
	AuthType              string   `json:"authType" yaml:"authType" toml:"authType"` // token, basic, or none
	Username              string   `json:"username" yaml:"username" toml:"username"`
	Password              string   `json:"password" yaml:"password" toml:"password"`
	Path                  string   `json:"path" yaml:"path" toml:"path"` // WebSocket path (default: /)
	Audience              string   `json:"audience" yaml:"audience" toml:"audience"`
}

func (b *BrokerConfig) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("name is required")
	}
	if b.Host == "" {
		return fmt.Errorf("host is required")
	}
	if b.Port <= 0 || b.Port > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	switch b.Transport {
	case "", "tcp", "websockets":
	default:
		return fmt.Errorf("transport must be \"tcp\" or \"websockets\"")
	}
	switch strings.ToLower(b.AuthType) {
	case "", "none", "token", "basic":
	default:
		return fmt.Errorf("authType must be \"token\", \"basic\", or \"none\"")
	}
	if err := ValidateTopicTemplate(b.PacketTopic); err != nil {
		return fmt.Errorf("packetTopic: %w", err)
	}
	if err := ValidateTopicTemplate(b.StatusTopic); err != nil {
		return fmt.Errorf("statusTopic: %w", err)
	}
	return nil
}

// TopicPlaceholders are the tokens a broker topic template may use. Both the
// lowercase forms and meshcoretomqtt's uppercase forms are accepted.
var TopicPlaceholders = []string{
	"iata", "IATA",
	"pubkey", "PUBKEY", "publicKey", "PUBLIC_KEY",
	"name", "NAME", "origin",
}

var topicTokenRe = regexp.MustCompile(`\{([^{}]*)\}`)

// ValidateTopicTemplate rejects topic templates with unknown placeholders or
// MQTT wildcards (publish topics may not contain + or #). Empty = default.
func ValidateTopicTemplate(t string) error {
	if t == "" {
		return nil
	}
	if strings.ContainsAny(t, "+#") {
		return fmt.Errorf("publish topics may not contain MQTT wildcards (+/#)")
	}
	for _, m := range topicTokenRe.FindAllStringSubmatch(t, -1) {
		if !slices.Contains(TopicPlaceholders, m[1]) {
			return fmt.Errorf("unknown placeholder {%s} (supported: {iata} {pubkey} {name})", m[1])
		}
	}
	return nil
}

// migrateTopicPrefix folds the deprecated topicPrefix field into explicit
// topic templates so feeds keep their topic paths.
func (b *BrokerConfig) migrateTopicPrefix() {
	if b.TopicPrefix == "" {
		return
	}
	if b.PacketTopic == "" {
		b.PacketTopic = b.TopicPrefix + "/{iata}/{pubkey}/packets"
	}
	if b.StatusTopic == "" {
		b.StatusTopic = b.TopicPrefix + "/{iata}/{pubkey}/status"
	}
	b.TopicPrefix = ""
}

func (c *MqttConfig) StatusIntervalSeconds() int {
	if c.StatusInterval != nil && *c.StatusInterval > 0 {
		return *c.StatusInterval
	}
	return 300
}
