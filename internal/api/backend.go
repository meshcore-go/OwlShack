package api

import "context"

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

	// RemovePeers drops the given peers from every companion's in-memory peer
	// table, so a deleted discovered peer doesn't linger in routing/counts until
	// the next restart. The DB row is deleted separately by the handler.
	RemovePeers(pubkeys [][]byte)

	// Companion returns the channel/DM senders for a named companion.
	Companion(name string) (MessageSender, DMSender, bool)

	// ChannelMutator returns add/remove operations for a companion's channels.
	ChannelMutator(name string) (adder ChannelAdder, remover ChannelRemover, ok bool)

	// RenameChannel renames a channel on the named companion.
	RenameChannel(companionName, oldName, newName string) error

	// TraceSender returns the trace sender for a named companion.
	TraceSender(name string) (TraceSender, bool)

	// AdvertSender returns the self-advert sender for a named companion.
	AdvertSender(name string) (AdvertSender, bool)

	// Repeater returns the repeater operations for a named companion.
	Repeater(name string) (*RepeaterOps, bool)

	// RepeaterNode returns runtime operations for the single repeater node the
	// bot runs (relay stats, neighbours, advertise-now), or ok=false when none
	// is running. This is the local repeater we ARE, not a remote one we drive.
	RepeaterNode() (*RepeaterNodeOps, bool)

	// PersistChannels writes the companions' current channels back to the
	// config file.
	PersistChannels(ctx context.Context) error

	// Per-resource config writes. Each applies the change by surrogate id,
	// re-validates the whole assembled config, and reloads — so an invalid edit
	// is rejected before it persists. Save* with id==0 creates (returning the
	// new id); id>0 updates. Secret fields typed *string mean "keep existing"
	// when nil, set when non-nil, cleared when empty. The ctx is the request
	// context, valid through the blocking write (the subsequent reload runs on
	// the app lifecycle, not this ctx).
	SaveSettings(ctx context.Context, in SettingsInput) error
	SaveMqtt(ctx context.Context, in MqttInput) error
	SaveBroker(ctx context.Context, in BrokerInput) (int64, error)
	DeleteBroker(ctx context.Context, id int64) error
	SaveCompanion(ctx context.Context, in CompanionInput) (int64, error)
	DeleteCompanion(ctx context.Context, id int64) error
	SaveChannel(ctx context.Context, in ChannelInput) (int64, error)
	DeleteChannel(ctx context.Context, id int64) error
	SaveTrigger(ctx context.Context, in TriggerInput) (int64, error)
	DeleteTrigger(ctx context.Context, id int64) error

	// Repeater node config is edited per-section so a save touches only its own
	// slice (no whole-config bulk update). CreateRepeater sets up the singleton
	// (PrivateKey nil generates one, seeds the "*" region); the Update* methods
	// patch one section of an existing repeater; the region ops are item-level.
	// Each validates the whole assembled config and reloads. DeleteRepeater
	// removes it entirely.
	CreateRepeater(ctx context.Context, in RepeaterCreateInput) error
	UpdateRepeaterNode(ctx context.Context, in RepeaterNodeInput) error
	UpdateRepeaterRelay(ctx context.Context, in RepeaterRelayInput) error
	UpdateRepeaterAdmin(ctx context.Context, in RepeaterAdminInput) error
	AddRepeaterRegion(ctx context.Context, in RepeaterRegionInput) error
	SetRepeaterRegionFlood(ctx context.Context, name string, denyFlood bool) error
	RemoveRepeaterRegion(ctx context.Context, name string) error
	DeleteRepeater(ctx context.Context) error
}

// RepeaterNodeOps are runtime operations on the running repeater node, wired to
// the domain by the app package (the api package never imports the domain).
type RepeaterNodeOps struct {
	Name      string
	Stats     func() any // live relay counters + uptime + neighbour count
	Neighbors func() any // directly-heard repeaters
	Advert    func(flood bool) error
	ACL       func() any                // admin clients in the ACL (name-resolved)
	RevokeACL func(pubkey string) error // drop a client's access
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

// RepeaterCreateInput sets up the singleton. Only identity is needed on create;
// everything else is edited afterward through the section endpoints.
type RepeaterCreateInput struct {
	Name       string  `json:"name"`
	PrivateKey *string `json:"privateKey"` // nil/empty = generate
}

// RepeaterNodeInput is the Node section: identity + position. PrivateKey nil =
// keep the current identity; a value rotates it.
type RepeaterNodeInput struct {
	Name       string   `json:"name"`
	PrivateKey *string  `json:"privateKey"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
}

// RepeaterRelayInput is the Relay-policy section: forwarding + advert cadence.
type RepeaterRelayInput struct {
	DisableFwd          *bool   `json:"disableFwd"`
	FloodMax            *int    `json:"floodMax"`
	FloodMaxUnscoped    *int    `json:"floodMaxUnscoped"`
	FloodMaxAdvert      *int    `json:"floodMaxAdvert"`
	LoopDetect          *string `json:"loopDetect"`
	PathHashMode        *int    `json:"pathHashMode"`
	DefaultRegion       string  `json:"defaultRegion"` // "" = unscoped flood adverts
	AdvertInterval      *int    `json:"advertInterval"`
	FloodAdvertInterval *int    `json:"floodAdvertInterval"`
}

// RepeaterAdminInput is the Owner & access section. Passwords are nil = keep,
// "" = clear.
type RepeaterAdminInput struct {
	OwnerInfo     string  `json:"ownerInfo"`
	AdminPassword *string `json:"adminPassword"`
	GuestPassword *string `json:"guestPassword"`
}

// RepeaterRegionInput is a region add (POST) or deny-flood toggle (PATCH) body.
type RepeaterRegionInput struct {
	Name      string `json:"name"`
	DenyFlood bool   `json:"denyFlood"`
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
