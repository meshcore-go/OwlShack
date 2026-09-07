package trigger

import (
	"context"

	"github.com/meshcore-go/OwlShack/internal/config"
)

// Event is emitted by a trigger and passed to the template engine.
type Event struct {
	Cfg     config.TriggerConfig
	Type    string // group, dm, cron, cap, etc
	BotName string
	Data    map[string]any // Trigger-specific data available to templates
}

// Callback is called when a trigger fires.
type Callback func(Event)

// Trigger is the interface all trigger types implement.
type Trigger interface {
	// Start begins listening/polling; ctx controls the trigger's lifetime.
	Start(ctx context.Context, callback Callback) error

	// Stop shuts down the trigger and releases resources.
	Stop() error
}
