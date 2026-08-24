package companion

import (
	"context"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/logging"
	"github.com/meshcore-go/OwlShack/internal/trigger"
)

// ReloadTriggers swaps the companion's trigger set in place, without tearing
// down the node, identity, MQTT observer, advert loop, or repeater/room
// sessions. Use it when only a companion's triggers changed: the result is the
// same as a restart for trigger purposes, but with no re-advert and no session
// loss. The new set is built and validated first, so an invalid trigger aborts
// the reload with the old set still running.
func (c *Companion) ReloadTriggers(newCfg config.CompanionConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx := c.runCtx
	if ctx == nil {
		return fmt.Errorf("companion %q not started", c.cfg.Name)
	}

	// Build + validate the new trigger set up front; bail before touching the
	// live triggers if anything is invalid. Channels are companion-owned and
	// unchanged here (a config change that also alters channels is not a
	// triggers-only change, so it takes the full-restart path instead).
	newEntries, err := c.buildTriggers(newCfg)
	if err != nil {
		return err
	}

	// Stop the old triggers (cron loops exit; channel triggers go no-op), then
	// swap in and start the new set. The persistent group-text handler picks up
	// the new set; node channels are untouched.
	for _, e := range c.triggers {
		e.trigger.Stop()
	}
	c.triggers = newEntries
	c.cfg.Triggers = newCfg.Triggers
	for _, e := range c.triggers {
		if err := e.trigger.Start(ctx, c.makeCallback(ctx, e)); err != nil {
			return fmt.Errorf("companion %q starting reloaded trigger (%s): %w", c.cfg.Name, e.config.Type, err)
		}
	}

	c.log.Info("triggers reloaded", "count", len(c.triggers))
	return nil
}

func applyTriggerDefaults(t *config.TriggerConfig) {
	if t.MaxRetries == nil {
		retries := 3
		t.MaxRetries = &retries
	}
	if t.RetryTimeout == nil {
		retry := int64(5)
		t.RetryTimeout = &retry
	}
}

// triggerChannelFilters builds the channel entries a channel trigger reacts to
// (used only as a name filter). It does NOT register them on the node — message
// decryption uses the companion's channels, which ApplyDefaults guarantees
// include every channel a trigger references.
func triggerChannelFilters(cfg config.TriggerConfig) ([]*meshcore.ChannelEntry, error) {
	if cfg.Channels == nil {
		return nil, nil
	}
	var channels []*meshcore.ChannelEntry
	for _, chRef := range *cfg.Channels {
		ch, err := channelFromRef(chRef)
		if err != nil {
			return nil, fmt.Errorf("invalid channel %q: %w", chRef.Name, err)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

// buildTriggers constructs the trigger entries for a companion config block.
// Channel triggers get a name filter only; channels are not registered on the
// node (they are companion-owned). Shared by NewCompanion and ReloadTriggers.
func (c *Companion) buildTriggers(cfg config.CompanionConfig) ([]triggerEntry, error) {
	if cfg.Triggers == nil {
		return nil, nil
	}
	entries := make([]triggerEntry, 0, len(*cfg.Triggers))
	for _, trigCfg := range *cfg.Triggers {
		applyTriggerDefaults(&trigCfg)
		channels, err := triggerChannelFilters(trigCfg)
		if err != nil {
			return nil, fmt.Errorf("companion %q: %w", c.cfg.Name, err)
		}
		entry, err := c.buildTrigger(trigCfg, channels)
		if err != nil {
			return nil, fmt.Errorf("companion %q trigger %q: %w", c.cfg.Name, trigCfg.Type, err)
		}
		entries = append(entries, *entry)
	}
	return entries, nil
}

func (c *Companion) buildTrigger(cfg config.TriggerConfig, channels []*meshcore.ChannelEntry) (*triggerEntry, error) {
	var t trigger.Trigger
	var err error

	switch cfg.Type {
	case "channel", "group":
		t, err = trigger.NewChannelTrigger(c.cfg.Name, cfg, c.node, channels, c.log)
	case "cron":
		t, err = trigger.NewCronTrigger(c.cfg.Name, cfg, c.log)
	default:
		return nil, fmt.Errorf("unknown trigger type %q", cfg.Type)
	}
	if err != nil {
		return nil, err
	}

	return &triggerEntry{
		trigger:  t,
		config:   cfg,
		channels: channels,
	}, nil
}

func (c *Companion) makeCallback(ctx context.Context, entry triggerEntry) trigger.Callback {
	return func(evt trigger.Event) {
		rendered, err := c.templater.Render(&evt, entry.config.Template)
		if err != nil {
			c.log.Error("template error", "error", err)
			return
		}

		c.log.Log(ctx, logging.LevelTrace, "template rendered",
			"trigger", evt.Type, "output", rendered)

		hashSize := resolvePathHashSize(entry.config.PathHashSize, evt)

		retryTimeout := time.Duration(*entry.config.RetryTimeout) * time.Second

		switch evt.Type {
		case "channel":
			ch, _ := evt.Data["ChannelEntry"].(*meshcore.ChannelEntry)
			c.log.Debug("sending group txt", "channel", ch.Name, "pathHashSize", hashSize)
			if err := c.sendGroupReply(ch, rendered, hashSize, retryTimeout, *entry.config.MaxRetries); err != nil {
				c.log.Error("send error", "error", err)
			}

		case "cron":
			for _, ch := range entry.channels {
				c.log.Debug("sending group txt", "channel", ch.Name, "pathHashSize", hashSize)
				if err := c.sendGroupReply(ch, rendered, hashSize, retryTimeout, *entry.config.MaxRetries); err != nil {
					c.log.Error("send error", "error", err)
				}
			}
		}
	}
}
