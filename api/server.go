package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"github.com/meshcore-go/meshcore-bot/store"
)

type CompanionInfo struct {
	Name      string        `json:"name"`
	PubKey    string        `json:"pubkey"`
	PeerCount int           `json:"peerCount"`
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
	AccessList   func(pubkeyHex string) (any, error)
	SetPerm      func(pubkeyHex, targetPubkeyHex string, perms uint8) error
}

type RepeaterLookup func(companionName string) (*RepeaterOps, bool)

type Server struct {
	store      *store.Store
	hub        *Hub
	mux        *http.ServeMux
	log        *slog.Logger
	assets     fs.FS
	compMu     sync.RWMutex
	companions CompanionProvider
	chMu       sync.RWMutex
	channels   ChannelLookup
	senderMu       sync.RWMutex
	senderLookup   CompanionLookup
	channelMutator  CompanionChannelMutator
	channelRenamer  ChannelRenamerFunc
	configPersist   func() error
	cfgMu         sync.RWMutex
	configPath    string
	reloadFn      func() error
	configGetter  ConfigGetter
	configUpdater ConfigUpdater
	traceMu    sync.RWMutex
	traceLookup TraceSenderLookup
	repeaterMu      sync.RWMutex
	repeaterLookup  RepeaterLookup
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
	s.mux.HandleFunc("POST /api/companions/{name}/contacts", s.handleAddContact)
	s.mux.HandleFunc("DELETE /api/companions/{name}/contacts/{pubkey}", s.handleDeleteContact)
	s.mux.HandleFunc("PATCH /api/companions/{name}/contacts/{pubkey}", s.handleUpdateContactMetadata)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handlePutConfig)
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
	s.mux.HandleFunc("GET /api/ws", s.handleWebSocket)

	s.mux.Handle("/", s.spaHandler())
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) Hub() *Hub {
	return s.hub
}

func (s *Server) SetCompanionProvider(p CompanionProvider) {
	s.compMu.Lock()
	s.companions = p
	s.compMu.Unlock()
}

func (s *Server) SetChannelLookup(cl ChannelLookup) {
	s.chMu.Lock()
	s.channels = cl
	s.chMu.Unlock()
}

func (s *Server) SetCompanionLookup(cl CompanionLookup) {
	s.senderMu.Lock()
	s.senderLookup = cl
	s.senderMu.Unlock()
}

func (s *Server) CompanionLookup() CompanionLookup {
	s.senderMu.RLock()
	defer s.senderMu.RUnlock()
	return s.senderLookup
}

func (s *Server) SetChannelMutator(cm CompanionChannelMutator) {
	s.senderMu.Lock()
	s.channelMutator = cm
	s.senderMu.Unlock()
}

func (s *Server) ChannelMutator() CompanionChannelMutator {
	s.senderMu.RLock()
	defer s.senderMu.RUnlock()
	return s.channelMutator
}

func (s *Server) SetChannelRenamer(fn ChannelRenamerFunc) {
	s.senderMu.Lock()
	s.channelRenamer = fn
	s.senderMu.Unlock()
}

func (s *Server) ChannelRenamer() ChannelRenamerFunc {
	s.senderMu.RLock()
	defer s.senderMu.RUnlock()
	return s.channelRenamer
}

func (s *Server) SetConfigPersist(fn func() error) {
	s.cfgMu.Lock()
	s.configPersist = fn
	s.cfgMu.Unlock()
}

func (s *Server) ConfigPersist() func() error {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.configPersist
}

func (s *Server) SetConfigPath(path string) {
	s.cfgMu.Lock()
	s.configPath = path
	s.cfgMu.Unlock()
}

func (s *Server) ConfigPath() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.configPath
}

func (s *Server) SetReloadFunc(fn func() error) {
	s.cfgMu.Lock()
	s.reloadFn = fn
	s.cfgMu.Unlock()
}

func (s *Server) ReloadFunc() func() error {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.reloadFn
}

func (s *Server) ChannelLookup() ChannelLookup {
	s.chMu.RLock()
	defer s.chMu.RUnlock()
	return s.channels
}

func (s *Server) SetTraceSenderLookup(tl TraceSenderLookup) {
	s.traceMu.Lock()
	s.traceLookup = tl
	s.traceMu.Unlock()
}

func (s *Server) SetRepeaterLookup(rl RepeaterLookup) {
	s.repeaterMu.Lock()
	s.repeaterLookup = rl
	s.repeaterMu.Unlock()
}

func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServerFS(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
