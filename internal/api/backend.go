package api

// Backend is the single seam between the HTTP/WS layer and the companion
// runtime. The app package implements it; the api package never imports the
// domain, which keeps the dependency arrow pointing inward (app -> api -> store).
//
// A nil method receiver is never expected: the server holds the backend behind
// a lock and swaps it atomically on config reload / modem reconnect. Handlers
// guard against a not-yet-wired (nil) backend via the accessors in server.go.
type Backend interface {
	// Companions returns a snapshot of all configured companions.
	Companions() []CompanionInfo

	// ChannelByHash resolves a channel hash byte to its info (name + PSK),
	// across every companion. Returns nil for an unknown hash.
	ChannelByHash(hash byte) *ChannelInfo

	// Companion returns the channel/DM senders for a named companion.
	Companion(name string) (MessageSender, DMSender, bool)

	// ChannelMutator returns add/remove operations for a companion's channels.
	ChannelMutator(name string) (adder ChannelAdder, remover ChannelRemover, ok bool)

	// RenameChannel renames a channel on the named companion.
	RenameChannel(companionName, oldName, newName string) error

	// TraceSender returns the trace sender for a named companion.
	TraceSender(name string) (TraceSender, bool)

	// Repeater returns the repeater operations for a named companion.
	Repeater(name string) (*RepeaterOps, bool)

	// PersistChannels writes the companions' current channels back to the
	// config file.
	PersistChannels() error

	// Config returns the current configuration as a JSON-serializable value.
	Config() any

	// UpdateConfig validates and applies a new configuration from JSON input.
	UpdateConfig(input map[string]any) error
}
