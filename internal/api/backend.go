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

	// Per-resource config writes. Each applies the change by surrogate id,
	// re-validates the whole assembled config, and reloads — so an invalid edit
	// is rejected before it persists. Save* with id==0 creates (returning the
	// new id); id>0 updates. Secret fields typed *string mean "keep existing"
	// when nil, set when non-nil, cleared when empty.
	SaveSettings(in SettingsInput) error
	SaveMqtt(in MqttInput) error
	SaveBroker(in BrokerInput) (int64, error)
	DeleteBroker(id int64) error
	SaveCompanion(in CompanionInput) (int64, error)
	DeleteCompanion(id int64) error
	SaveChannel(in ChannelInput) (int64, error)
	DeleteChannel(id int64) error
	SaveTrigger(in TriggerInput) (int64, error)
	DeleteTrigger(id int64) error
}

// --- per-resource config write inputs (JSON request bodies) ---

type SettingsInput struct {
	LogLevel       *string  `json:"logLevel"`
	ConnectionType *string  `json:"connectionType"`
	Connection     *string  `json:"connection"`
	BaudRate       *int     `json:"baudRate"`
	Freq           *float64 `json:"freq"`
	BW             *float64 `json:"bw"`
	SF             *int     `json:"sf"`
	CR             *int     `json:"cr"`
	TX             *int     `json:"tx"`
	ListenAddr     *string  `json:"listenAddr"`
	SetupComplete  *bool    `json:"setupComplete"`
}

type MqttInput struct {
	Enabled         *bool   `json:"enabled"`
	NodeCompanionID *int64  `json:"nodeCompanionId"`
	IataCode        *string `json:"iataCode"`
	StatusInterval  *int    `json:"statusInterval"`
	Owner           *string `json:"owner"`
	Email           *string `json:"email"`
}

type BrokerInput struct {
	ID                    int64    `json:"id"`
	Name                  string   `json:"name"`
	Enabled               bool     `json:"enabled"`
	Dedup                 bool     `json:"dedup"`
	Transport             string   `json:"transport"`
	Host                  string   `json:"host"`
	Port                  int      `json:"port"`
	PacketTopic           *string  `json:"packetTopic"`
	StatusTopic           *string  `json:"statusTopic"`
	DisallowedPacketTypes []string `json:"disallowedPacketTypes"`
	RetainStatus          bool     `json:"retainStatus"`
	TLSEnabled            bool     `json:"tlsEnabled"`
	TLSInsecure           bool     `json:"tlsInsecure"`
	AuthType              string   `json:"authType"`
	Username              string   `json:"username"`
	Password              *string  `json:"password"` // nil = keep existing
	Path                  string   `json:"path"`
	Audience              string   `json:"audience"`
}

type CompanionInput struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	PrivateKey     *string  `json:"privateKey"` // nil = keep (update) / generate (create)
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	AdvertInterval *int     `json:"advertInterval"`
}

type ChannelInput struct {
	ID          int64   `json:"id"`
	CompanionID int64   `json:"companionId"`
	Name        string  `json:"name"`
	PrivateKey  *string `json:"privateKey"` // nil = keep existing
}

type TriggerInput struct {
	ID                 int64    `json:"id"`
	CompanionID        int64    `json:"companionId"`
	Type               string   `json:"type"`
	Template           string   `json:"template"`
	CharLimitBehaviour *string  `json:"charLimitBehaviour"`
	Match              []string `json:"match"`
	Contacts           []string `json:"contacts"`
	ChannelIDs         []int64  `json:"channelIds"`
	RetryTimeout       *int64   `json:"retryTimeout"`
	MaxRetries         *int     `json:"maxRetries"`
	PathHashSize       *int     `json:"pathHashSize"`
	Schedule           *string  `json:"schedule"`
}
