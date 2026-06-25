package companion

import (
	"encoding/hex"
	"fmt"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/meshcore-bot/internal/config"
)

func (c *Companion) AddChannel(ref config.ChannelRef) error {
	ch, err := channelFromRef(ref)
	if err != nil {
		return fmt.Errorf("invalid channel %q: %w", ref.Name, err)
	}

	for i := range node.DefaultMaxChannels {
		if existing := c.node.Channel(i); existing != nil && existing.Name == ch.Name {
			return fmt.Errorf("channel %q already exists", ch.Name)
		}
	}

	idx := c.nextFreeChannelIndex()
	if idx < 0 {
		return fmt.Errorf("no free channel slots")
	}

	if !c.node.SetChannel(idx, ch) {
		return fmt.Errorf("failed to set channel at index %d", idx)
	}

	c.log.Info("channel added", "channel", ch.Name, "index", idx)
	return nil
}

func (c *Companion) RemoveChannel(name string) error {
	if used := c.channelTriggerUsage(name); used != "" {
		return fmt.Errorf("channel %q is in use by the %s; remove that trigger usage first", name, used)
	}
	for i := range node.DefaultMaxChannels {
		ch := c.node.Channel(i)
		if ch != nil && ch.Name == name {
			c.node.RemoveChannel(i)
			c.log.Info("channel removed", "channel", name, "index", i)
			return nil
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func (c *Companion) RenameChannel(oldName, newName string) error {
	if used := c.channelTriggerUsage(oldName); used != "" {
		return fmt.Errorf("channel %q is in use by the %s; remove that trigger usage first", oldName, used)
	}
	for i := range node.DefaultMaxChannels {
		ch := c.node.Channel(i)
		if ch != nil && ch.Name == oldName {
			ch.Name = newName
			c.log.Info("channel renamed", "old", oldName, "new", newName, "index", i)
			return nil
		}
	}
	return fmt.Errorf("channel %q not found", oldName)
}

// channelTriggerUsage returns a human description of the first trigger that
// references the named channel, or "" if none do. Channels are companion-owned;
// a channel a trigger uses can't be edited/renamed/removed until that trigger
// usage is gone. Matching is case-sensitive — channel names map to distinct
// keys per case (see channelFromRef), matching the rest of the channel code.
func (c *Companion) channelTriggerUsage(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.triggers {
		for _, ch := range e.channels {
			if ch.Name == name {
				if e.config.Type == "cron" {
					return "cron bot"
				}
				return "group bot"
			}
		}
	}
	return ""
}

func (c *Companion) nextFreeChannelIndex() int {
	for i := 0; i < node.DefaultMaxChannels; i++ {
		if c.node.Channel(i) == nil {
			return i
		}
	}
	return -1
}

// StandaloneChannels returns all of the companion's channels for config
// persistence. Channels are companion-owned (triggers only reference them by
// name), so this is simply every channel registered on the node. Hashtag/Public
// channels omit their derived key.
func (c *Companion) StandaloneChannels() []config.ChannelRef {
	allChs := c.node.Channels()
	var refs []config.ChannelRef
	for _, ch := range allChs {
		if ch == nil {
			continue
		}
		ref := config.ChannelRef{Name: ch.Name}
		if !isHashtagChannel(ch) {
			ref.PrivateKey = hex.EncodeToString(ch.PSK[:])
		}
		refs = append(refs, ref)
	}
	return refs
}

func isHashtagChannel(ch *meshcore.ChannelEntry) bool {
	if strings.HasPrefix(ch.Name, "#") {
		derived := meshcore.NewChannelFromHashtag(meshcore.NormalizeHashtag(ch.Name))
		return derived.Hash == ch.Hash
	}
	if strings.EqualFold(ch.Name, "Public") {
		pub, err := meshcore.NewChannelFromBase64("Public", "izOH6cXN6mrJ5e26oRXNcg==")
		if err != nil {
			return false
		}
		return pub.Hash == ch.Hash
	}
	return false
}
