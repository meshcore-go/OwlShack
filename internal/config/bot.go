package config

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"text/template"

	"github.com/robfig/cron/v3"
)

type TriggerConfig struct {
	Type     string `json:"type" yaml:"type" toml:"type"` // group, private, dm, cron, cap, etc
	Template string `json:"template" yaml:"template" toml:"template"`

	// Message Overflow behaviour
	CharLimitBehaviour *string `json:"charLimitBehaviour" yaml:"charLimitBehaviour" toml:"charLimitBehaviour"` // e.g. truncate or split

	// Messages/DMs
	Match    *[]string    `json:"match" yaml:"match" toml:"match"`          // Patterns to match against (supports wildcards/regex)
	Channels *ChannelList `json:"channels" yaml:"channels" toml:"channels"` // Channels to listen on (strings or {name, privateKey} objects)
	Contacts *[]string    `json:"contacts" yaml:"contacts" toml:"contact"`  // What Contacts to listen in for DMs

	// Retry Settings
	RetryTimeout *int64 `json:"retryTimeout" yaml:"retryTimeout" toml:"retryTimeout"` // Stored as seconds
	MaxRetries   *int   `json:"maxRetries" yaml:"maxRetries" toml:"maxRetries"`

	// Path Hash Size: 1-4 = fixed size, 0 = mirror incoming packet's hash size, nil = default (1)
	PathHashSize *uint8 `json:"pathHashSize,omitempty" yaml:"pathHashSize,omitempty" toml:"pathHashSize,omitempty"`

	// Cron Trigger
	Schedule string `json:"schedule,omitempty" yaml:"schedule,omitempty" toml:"schedule,omitempty"`
}

// Validate rejects trigger configs that would fail companion construction or
// trigger startup (which, after a reload, exits the process).
func (t *TriggerConfig) Validate() error {
	switch t.Type {
	case "channel", "group":
		if t.Channels == nil || len(*t.Channels) == 0 {
			return fmt.Errorf("%s trigger requires at least one channel", t.Type)
		}
	case "cron":
		if t.Schedule == "" {
			return fmt.Errorf("cron trigger requires a schedule")
		}
		if _, err := cron.ParseStandard(t.Schedule); err != nil {
			return fmt.Errorf("invalid cron schedule %q: %w", t.Schedule, err)
		}
	default:
		return fmt.Errorf("unknown trigger type %q (supported: group, cron)", t.Type)
	}

	if t.Template == "" {
		return fmt.Errorf("template is required")
	}
	// Parse-check with stubs for the trigger func map (templater.go) so valid
	// templates pass and typo'd function names are caught.
	stubs := template.FuncMap{
		"formatPathBytes": func(any) string { return "" },
	}
	if _, err := template.New("trigger").Funcs(stubs).Parse(t.Template); err != nil {
		return fmt.Errorf("invalid template: %w", err)
	}

	if t.Match != nil {
		for _, pattern := range *t.Match {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("invalid match pattern %q: %w", pattern, err)
			}
		}
	}

	if t.Channels != nil {
		for _, ch := range *t.Channels {
			if err := ch.Validate(); err != nil {
				return err
			}
		}
	}

	if t.PathHashSize != nil && *t.PathHashSize > 4 {
		return fmt.Errorf("pathHashSize must be 0-4")
	}

	return nil
}

func (cr *ChannelRef) Validate() error {
	if cr.Name == "" {
		return fmt.Errorf("channel name is required")
	}
	if cr.PrivateKey != "" {
		if _, err := hex.DecodeString(cr.PrivateKey); err != nil {
			return fmt.Errorf("channel %q: privateKey must be hex: %w", cr.Name, err)
		}
	}
	return nil
}
