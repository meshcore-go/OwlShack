package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/meshcore-go/meshcore-bot/internal/store"
)

type CompanionInfo struct {
	Name      string        `json:"name"`
	PubKey    string        `json:"pubkey"`
	PeerCount int           `json:"peerCount"`
	Lat       *float64      `json:"lat,omitempty"`
	Lon       *float64      `json:"lon,omitempty"`
	Channels  []ChannelInfo `json:"channels,omitempty"`
}

type CompanionProvider func() []CompanionInfo

type MessageSender func(channelName, text string) error
type DMSender func(pubkeyHex, text string) error

type ChannelAdder func(name, privateKey string) error
type ChannelRemover func(name string) error
type ChannelRenamerFunc func(companionName, oldName, newName string) error

type CompanionLookup func(name string) (MessageSender, DMSender, bool)
type CompanionChannelMutator func(name string) (adder ChannelAdder, remover ChannelRemover, ok bool)

type RepeaterOps struct {
	Login        func(pubkeyHex, password string) (any, error)
	RoomLogin    func(pubkeyHex, password string, syncSince uint32) (any, error)
	StatusReq    func(pubkeyHex string) (any, error)
	CLI          func(pubkeyHex, command string) (string, error)
	Session      func(pubkeyHex string) any
	Logout       func(pubkeyHex string)
	PathGet      func(pubkeyHex string) (any, error)
	PathReset    func(pubkeyHex string) error
	PathSet      func(pubkeyHex, pathHex string, pathHashSize int) error
	NeighborsReq func(pubkeyHex string, count uint8, offset uint16) (any, error)
	OwnerInfoReq func(pubkeyHex string) (any, error)
	TelemetryReq func(pubkeyHex string) (any, error)
	// ContactTelemetryReq requests telemetry from a non-repeater contact
	// (no login session — uses the ECDH secret with the contact).
	ContactTelemetryReq func(pubkeyHex string) (any, error)
	AccessList          func(pubkeyHex string) (any, error)
	SetPerm             func(pubkeyHex, targetPubkeyHex string, perms uint8) error
}

type Server struct {
	store  *store.Store
	hub    *Hub
	mux    *http.ServeMux
	log    *slog.Logger
	assets fs.FS
	poller NodePoller

	mu      sync.RWMutex
	backend Backend
}

func NewServer(st *store.Store, assets fs.FS, log *slog.Logger) *Server {
	s := &Server{
		store:  st,
		hub:    NewHub(),
		mux:    http.NewServeMux(),
		log:    log,
		assets: assets,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/peers", s.handleListPeers)
	s.mux.HandleFunc("DELETE /api/peers/{pubkey}", s.handleDeletePeer)
	s.mux.HandleFunc("GET /api/packets", s.handleListPackets)
	s.mux.HandleFunc("GET /api/companions", s.handleListCompanions)
	s.mux.HandleFunc("GET /api/companions/{name}/contacts", s.handleListContacts)
	s.mux.HandleFunc("GET /api/companions/{name}/contacts/{pubkey}", s.handleGetContact)
	s.mux.HandleFunc("POST /api/companions/{name}/contacts", s.handleAddContact)
	s.mux.HandleFunc("DELETE /api/companions/{name}/contacts/{pubkey}", s.handleDeleteContact)
	s.mux.HandleFunc("PATCH /api/companions/{name}/contacts/{pubkey}", s.handleUpdateContactMetadata)
	s.mux.HandleFunc("GET /api/companions/{name}/contacts/{pubkey}/telemetry", s.handleContactTelemetry)
	// Per-resource config REST (reads; scoped + secret-redacted).
	s.mux.HandleFunc("GET /api/config/settings", s.handleGetSettings)
	s.mux.HandleFunc("GET /api/config/mqtt", s.handleGetMqtt)
	s.mux.HandleFunc("GET /api/config/mqtt/brokers", s.handleGetBrokers)
	s.mux.HandleFunc("GET /api/config/companions", s.handleGetCompanions)
	s.mux.HandleFunc("GET /api/config/companions/{id}/channels", s.handleGetCompanionChannels)
	s.mux.HandleFunc("GET /api/config/channels", s.handleGetAllChannels)
	s.mux.HandleFunc("GET /api/config/triggers", s.handleGetTriggers)
	// Per-resource config REST (writes; validated + reloaded server-side).
	s.mux.HandleFunc("PUT /api/config/settings", s.handlePutSettings)
	s.mux.HandleFunc("PUT /api/config/mqtt", s.handlePutMqtt)
	s.mux.HandleFunc("POST /api/config/mqtt/brokers", s.handleSaveBroker)
	s.mux.HandleFunc("PUT /api/config/mqtt/brokers/{id}", s.handleSaveBroker)
	s.mux.HandleFunc("DELETE /api/config/mqtt/brokers/{id}", s.handleDeleteBroker)
	s.mux.HandleFunc("POST /api/config/companions", s.handleSaveCompanion)
	s.mux.HandleFunc("PUT /api/config/companions/{id}", s.handleSaveCompanion)
	s.mux.HandleFunc("DELETE /api/config/companions/{id}", s.handleDeleteCompanion)
	s.mux.HandleFunc("POST /api/config/companions/{id}/channels", s.handleCreateChannel)
	s.mux.HandleFunc("PUT /api/config/channels/{id}", s.handleSaveChannel)
	s.mux.HandleFunc("DELETE /api/config/channels/{id}", s.handleDeleteChannel)
	s.mux.HandleFunc("POST /api/config/triggers", s.handleSaveTrigger)
	s.mux.HandleFunc("PUT /api/config/triggers/{id}", s.handleSaveTrigger)
	s.mux.HandleFunc("DELETE /api/config/triggers/{id}", s.handleDeleteTrigger)
	s.mux.HandleFunc("GET /api/companions/{name}/channels", s.handleListChannels)
	s.mux.HandleFunc("POST /api/companions/{name}/channels", s.handleAddChannel)
	s.mux.HandleFunc("DELETE /api/companions/{name}/channels/{channel}", s.handleRemoveChannel)
	s.mux.HandleFunc("GET /api/companions/{name}/messages", s.handleListMessages)
	s.mux.HandleFunc("POST /api/companions/{name}/messages", s.handleSendMessage)
	s.mux.HandleFunc("GET /api/companions/{name}/conversations", s.handleListConversations)
	s.mux.HandleFunc("POST /api/companions/{name}/conversations/{conversationId}/read", s.handleMarkRead)
	s.mux.HandleFunc("GET /api/messages/{messageId}/echoes", s.handleListEchoes)
	s.mux.HandleFunc("GET /api/companions/{name}/messages/{messageId}/path", s.handleGetMessagePath)
	s.mux.HandleFunc("DELETE /api/messages/{messageId}", s.handleDeleteMessage)
	s.mux.HandleFunc("POST /api/companions/{name}/messages/{messageId}/retry", s.handleRetryMessage)
	s.mux.HandleFunc("POST /api/companions/{name}/conversations/{conversationId}/block", s.handleBlockSender)
	s.mux.HandleFunc("DELETE /api/companions/{name}/conversations/{conversationId}/block/{sender}", s.handleUnblockSender)
	s.mux.HandleFunc("GET /api/companions/{name}/conversations/{conversationId}/block", s.handleListBlockedSenders)
	s.mux.HandleFunc("DELETE /api/companions/{name}/conversations/{conversationId}/messages", s.handleDeleteConversationMessages)
	s.mux.HandleFunc("GET /api/companions/{name}/conversations/{conversationId}/participants", s.handleListParticipants)
	s.mux.HandleFunc("PATCH /api/companions/{name}/channels/{channel}", s.handleRenameChannel)
	s.mux.HandleFunc("GET /api/companions/{name}/channels/{channel}/key", s.handleGetChannelKey)
	s.mux.HandleFunc("GET /api/companions/{name}/contacts/{pubkey}/path", s.handleGetContactPath)
	s.mux.HandleFunc("DELETE /api/companions/{name}/contacts/{pubkey}/path", s.handleResetContactPath)
	s.mux.HandleFunc("POST /api/companions/{name}/trace", s.handleSendTrace)
	s.mux.HandleFunc("POST /api/companions/{name}/repeaters/{pubkey}/login", s.handleRepeaterLogin)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/status", s.handleRepeaterStatus)
	s.mux.HandleFunc("POST /api/companions/{name}/repeaters/{pubkey}/cli", s.handleRepeaterCLI)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/session", s.handleRepeaterSession)
	s.mux.HandleFunc("DELETE /api/companions/{name}/repeaters/{pubkey}/session", s.handleRepeaterLogout)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/path", s.handleRepeaterPathGet)
	s.mux.HandleFunc("DELETE /api/companions/{name}/repeaters/{pubkey}/path", s.handleRepeaterPathReset)
	s.mux.HandleFunc("PUT /api/companions/{name}/repeaters/{pubkey}/path", s.handleRepeaterPathSet)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/neighbors", s.handleRepeaterNeighbors)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/owner", s.handleRepeaterOwnerInfo)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/telemetry", s.handleRepeaterTelemetry)
	s.mux.HandleFunc("GET /api/companions/{name}/repeaters/{pubkey}/access", s.handleRepeaterAccessList)
	s.mux.HandleFunc("PUT /api/companions/{name}/repeaters/{pubkey}/access/{target}", s.handleRepeaterAccessSet)
	s.mux.HandleFunc("DELETE /api/companions/{name}/repeaters/{pubkey}/access/{target}", s.handleRepeaterAccessRemove)
	s.mux.HandleFunc("POST /api/companions/{name}/rooms/{pubkey}/login", s.handleRoomLogin)
	s.mux.HandleFunc("GET /api/companions/{name}/rooms/{pubkey}/session", s.handleRepeaterSession)
	s.mux.HandleFunc("DELETE /api/companions/{name}/rooms/{pubkey}/session", s.handleRepeaterLogout)
	s.mux.HandleFunc("GET /api/nodes/monitored", s.handleListMonitoredNodes)
	s.mux.HandleFunc("GET /api/nodes/{pubkey}/metrics", s.handleListNodeMetricNames)
	s.mux.HandleFunc("GET /api/nodes/{pubkey}/history", s.handleNodeHistory)
	s.mux.HandleFunc("POST /api/nodes/{pubkey}/poll", s.handlePollNode)
	s.mux.HandleFunc("GET /api/ws", s.handleWebSocket)

	s.mux.Handle("/", s.spaHandler())
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) Hub() *Hub {
	return s.hub
}

// SetBackend installs (or swaps, on config reload / modem reconnect) the
// domain backend. Safe to call while the server is serving requests.
func (s *Server) SetBackend(b Backend) {
	s.mu.Lock()
	s.backend = b
	s.mu.Unlock()
}

func (s *Server) backendRef() Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backend
}

// SetPoller installs the node poller (the monitor service). Unlike the backend
// it isn't swapped on reload — the service is long-lived — but it's set after
// the HTTP server starts, so access is guarded all the same.
func (s *Server) SetPoller(p NodePoller) {
	s.mu.Lock()
	s.poller = p
	s.mu.Unlock()
}

func (s *Server) pollerRef() NodePoller {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.poller
}

// The accessors below adapt the backend into the small func types the handlers
// consume. Each returns nil when no backend is wired yet, which the handlers
// guard against (returning 503/404).

func (s *Server) companionProvider() CompanionProvider {
	if b := s.backendRef(); b != nil {
		return b.Companions
	}
	return nil
}

func (s *Server) ChannelLookup() ChannelLookup {
	if b := s.backendRef(); b != nil {
		return b.ChannelByHash
	}
	return nil
}

func (s *Server) CompanionLookup() CompanionLookup {
	if b := s.backendRef(); b != nil {
		return b.Companion
	}
	return nil
}

func (s *Server) ChannelMutator() CompanionChannelMutator {
	if b := s.backendRef(); b != nil {
		return b.ChannelMutator
	}
	return nil
}

func (s *Server) ChannelRenamer() ChannelRenamerFunc {
	if b := s.backendRef(); b != nil {
		return b.RenameChannel
	}
	return nil
}

func (s *Server) ConfigPersist() func() error {
	if b := s.backendRef(); b != nil {
		return b.PersistChannels
	}
	return nil
}

func (s *Server) traceSenderLookup() TraceSenderLookup {
	if b := s.backendRef(); b != nil {
		return b.TraceSender
	}
	return nil
}

func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServerFS(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unmatched API paths must 404, not fall through to the SPA index — a
		// retired endpoint should read as "gone", not return HTML.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		f, err := s.assets.Open(r.URL.Path[1:])
		if err != nil {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
