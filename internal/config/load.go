package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

var defaultConfigNames = []string{
	"config.toml",
	"config.yaml",
	"config.yml",
	"config.json",
}

// Load reads the config from path, or searches the working directory for a
// default-named config file when path is empty. It returns the parsed config
// and the resolved file path.
func Load(path string) (*Config, string, error) {
	if path == "" {
		return loadFromCwd()
	}
	return LoadFromPath(path)
}

func loadFromCwd() (*Config, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getting working directory: %w", err)
	}

	for _, name := range defaultConfigNames {
		p := filepath.Join(cwd, name)
		if _, err := os.Stat(p); err == nil {
			slog.Info("using config", "path", p)
			return LoadFromPath(p)
		}
	}

	return nil, "", fmt.Errorf("no config file found in %s (tried %s)", cwd, strings.Join(defaultConfigNames, ", "))
}

// FindDefaultConfig returns the path of a default-named config file in the
// working directory, or "" when none exists.
func FindDefaultConfig() string {
	for _, name := range defaultConfigNames {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return ""
}

// LoadFromPath reads and parses the config at path, selecting the decoder from
// the file extension.
func LoadFromPath(path string) (*Config, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("reading config: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		cfg, err := UnmarshalConfigToml(data)
		return cfg, path, err
	case ".yaml", ".yml":
		cfg, err := UnmarshalConfigYaml(data)
		return cfg, path, err
	case ".json":
		cfg, err := UnmarshalConfigJson(data)
		return cfg, path, err
	default:
		return nil, path, fmt.Errorf("unsupported config format %q", ext)
	}
}

// Marshal encodes cfg using the format implied by path's extension.
func Marshal(path string, cfg *Config) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		return toml.Marshal(cfg)
	case ".yaml", ".yml":
		return yaml.Marshal(cfg)
	case ".json":
		return json.MarshalIndent(cfg, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported config format %q", ext)
	}
}

// ParseConnection splits a connection string of the form "serial://<addr>" or
// "tcp://<addr>" into its scheme and address.
func ParseConnection(conn string) (scheme, addr string, ok bool) {
	for _, prefix := range []string{"serial://", "tcp://"} {
		if strings.HasPrefix(conn, prefix) {
			return strings.TrimSuffix(prefix, "://"), conn[len(prefix):], true
		}
	}
	return "", "", false
}

// Validate checks that the config's fields are within acceptable ranges. Nil
// pointer fields are treated as "use default" and skipped.
func (c *Config) Validate() error {
	if c.Connection != nil {
		_, _, ok := ParseConnection(*c.Connection)
		if !ok {
			return fmt.Errorf("invalid connection string %q: must start with serial:// or tcp://", *c.Connection)
		}
	}

	if c.BaudRate != nil && *c.BaudRate <= 0 {
		return fmt.Errorf("baudRate must be positive")
	}

	if c.Freq != nil && (*c.Freq < 100 || *c.Freq > 1000) {
		return fmt.Errorf("freq must be between 100 and 1000 MHz")
	}

	if c.Bw != nil && *c.Bw <= 0 {
		return fmt.Errorf("bw must be positive")
	}

	if c.SF != nil && (*c.SF < 5 || *c.SF > 12) {
		return fmt.Errorf("sf must be between 5 and 12")
	}

	if c.CR != nil && (*c.CR < 5 || *c.CR > 8) {
		return fmt.Errorf("cr must be between 5 and 8")
	}

	if c.TX != nil && *c.TX > 22 {
		return fmt.Errorf("tx must be between 0 and 22 dBm")
	}

	if len(c.Companions) == 0 {
		return fmt.Errorf("at least one companion must be configured")
	}

	// A startCompanions failure after a reload exits the process, so anything
	// that would fail companion construction (bad regex/cron/channel key) must
	// be rejected here, before the config is ever written.
	seen := make(map[string]bool, len(c.Companions))
	seenKeys := make(map[string]string, len(c.Companions))
	for i, comp := range c.Companions {
		if comp.Name == "" {
			return fmt.Errorf("companion[%d]: name is required", i)
		}
		// The store keys all persisted state (messages, contacts) by name.
		if seen[comp.Name] {
			return fmt.Errorf("duplicate companion name %q", comp.Name)
		}
		seen[comp.Name] = true
		if comp.PrivateKey != "" {
			if err := validateSeedHex(comp.PrivateKey); err != nil {
				return fmt.Errorf("companion %q: %w", comp.Name, err)
			}
			if other, dup := seenKeys[comp.PrivateKey]; dup {
				return fmt.Errorf("companions %q and %q share the same privateKey", other, comp.Name)
			}
			seenKeys[comp.PrivateKey] = comp.Name
		}
		if comp.Triggers != nil {
			for j, trig := range *comp.Triggers {
				if err := trig.Validate(); err != nil {
					return fmt.Errorf("companion %q trigger[%d]: %w", comp.Name, j, err)
				}
			}
		}
	}

	if c.Mqtt != nil {
		if c.Mqtt.Node != nil && *c.Mqtt.Node != "" && !seen[*c.Mqtt.Node] {
			return fmt.Errorf("mqtt node %q does not match any companion", *c.Mqtt.Node)
		}
		for i, b := range c.Mqtt.Brokers {
			if err := b.Validate(); err != nil {
				return fmt.Errorf("mqtt broker[%d] %q: %w", i, b.Name, err)
			}
		}
	}

	return nil
}

// ModemSettingsChanged reports whether a config reload altered any field that
// requires tearing down and re-establishing the modem connection.
func ModemSettingsChanged(old, new_ *Config) bool {
	return derefStr(old.Connection) != derefStr(new_.Connection) ||
		derefInt(old.BaudRate) != derefInt(new_.BaudRate) ||
		derefFloat(old.Freq) != derefFloat(new_.Freq) ||
		derefFloat(old.Bw) != derefFloat(new_.Bw) ||
		derefUint8(old.SF) != derefUint8(new_.SF) ||
		derefUint8(old.CR) != derefUint8(new_.CR) ||
		derefUint8(old.TX) != derefUint8(new_.TX)
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefUint8(p *uint8) uint8 {
	if p == nil {
		return 0
	}
	return *p
}
