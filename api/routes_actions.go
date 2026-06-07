package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type pathHopJSON struct {
	Hash      string   `json:"hash"`
	PeerNames []string `json:"peerNames,omitempty"`
}

func (s *Server) handleListEchoes(w http.ResponseWriter, r *http.Request) {
	msgIDStr := r.PathValue("messageId")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}

	echoes, err := s.store.Echoes.ListByMessage(msgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list echoes")
		return
	}

	type echoJSON struct {
		ID           int64         `json:"id"`
		ReceivedAt   string        `json:"receivedAt"`
		Hops         int           `json:"hops"`
		PathHashSize int           `json:"pathHashSize"`
		Path         []pathHopJSON `json:"path"`
		SNR          *float64      `json:"snr,omitempty"`
		RSSI         *int8         `json:"rssi,omitempty"`
	}

	out := make([]echoJSON, 0, len(echoes))
	for _, e := range echoes {
		ej := echoJSON{
			ID:           e.ID,
			ReceivedAt:   e.ReceivedAt.UTC().Format(time.RFC3339),
			Hops:         e.Hops,
			PathHashSize: e.PathHashSize,
			SNR:          e.SNR,
			RSSI:         e.RSSI,
		}

		ej.Path = s.resolvePathHashes(e.PathHashes, e.PathHashSize)
		out = append(out, ej)
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetMessagePath(w http.ResponseWriter, r *http.Request) {
	msgIDStr := r.PathValue("messageId")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}

	name := r.PathValue("name")
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		writeError(w, http.StatusBadRequest, "channel is required")
		return
	}

	msgs, err := s.store.Messages.List(name, channel, 1000, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch messages")
		return
	}

	type pathJSON struct {
		Hops         int           `json:"hops"`
		PathHashSize int           `json:"pathHashSize"`
		Sender       string        `json:"sender"`
		Path         []pathHopJSON `json:"path"`
	}

	for _, m := range msgs {
		if m.ID != msgID {
			continue
		}

		result := pathJSON{
			Sender: m.Sender,
		}

		if m.Hops != nil {
			result.Hops = *m.Hops
		}
		if m.PathHashSize != nil {
			result.PathHashSize = *m.PathHashSize
		}

		result.Path = s.resolvePathHashes(m.PathHashes, result.PathHashSize)
		writeJSON(w, http.StatusOK, result)
		return
	}

	writeError(w, http.StatusNotFound, "message not found")
}

func (s *Server) resolvePathHashes(pathBytes []byte, hashSize int) []pathHopJSON {
	if hashSize <= 0 {
		hashSize = 1
	}

	hops := make([]pathHopJSON, 0)

	for i := 0; i+hashSize <= len(pathBytes); i += hashSize {
		hashBytes := pathBytes[i : i+hashSize]
		hop := pathHopJSON{
			Hash: hex.EncodeToString(hashBytes),
		}

		peers, err := s.store.Peers.LookupByHash(hashBytes)
		if err == nil {
			hop.PeerNames = peers
		}

		hops = append(hops, hop)
	}

	return hops
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	msgIDStr := r.PathValue("messageId")
	msgID, err := strconv.ParseInt(msgIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}

	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Messages.Delete(msgID)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete message")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBlockSender(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	convoID := r.PathValue("conversationId")

	var body struct {
		Sender string `json:"sender"`
	}
	if err := readJSON(r, &body); err != nil || body.Sender == "" {
		writeError(w, http.StatusBadRequest, "sender is required")
		return
	}

	var blockErr error
	s.store.WriteSync(func() {
		blockErr = s.store.BlockedSenders.Block(name, convoID, body.Sender)
	})
	if blockErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to block sender")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnblockSender(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	convoID := r.PathValue("conversationId")
	sender := r.PathValue("sender")

	var unblockErr error
	s.store.WriteSync(func() {
		unblockErr = s.store.BlockedSenders.Unblock(name, convoID, sender)
	})
	if unblockErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to unblock sender")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListBlockedSenders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	convoID := r.PathValue("conversationId")

	senders, err := s.store.BlockedSenders.List(name, convoID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list blocked senders")
		return
	}

	if senders == nil {
		senders = []string{}
	}

	writeJSON(w, http.StatusOK, senders)
}

func (s *Server) handleDeleteConversationMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	convoID := r.PathValue("conversationId")

	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var channel string
	if strings.HasPrefix(convoID, "channel:") {
		channel = strings.TrimPrefix(convoID, "channel:")
	} else if strings.HasPrefix(convoID, "contact:") {
		channel = "dm:" + strings.TrimPrefix(convoID, "contact:")
	} else {
		writeError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}

	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Messages.DeleteByChannel(name, channel)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete messages")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListParticipants(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	convoID := r.PathValue("conversationId")

	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var channel string
	if strings.HasPrefix(convoID, "channel:") {
		channel = strings.TrimPrefix(convoID, "channel:")
	} else if strings.HasPrefix(convoID, "contact:") {
		channel = "dm:" + strings.TrimPrefix(convoID, "contact:")
	} else {
		writeError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}

	senders, err := s.store.Messages.DistinctSenders(name, channel)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list participants")
		return
	}

	if senders == nil {
		senders = []string{}
	}

	writeJSON(w, http.StatusOK, senders)
}

func (s *Server) handleRenameChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	channelName := r.PathValue("channel")

	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	renamer := s.ChannelRenamer()
	if renamer == nil {
		writeError(w, http.StatusServiceUnavailable, "channel management not available")
		return
	}

	if err := renamer(name, channelName, body.Name); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if persist := s.ConfigPersist(); persist != nil {
		if err := persist(); err != nil {
			s.log.Error("config persist failed after channel rename", "error", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetChannelKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	channelName := r.PathValue("channel")

	s.compMu.RLock()
	provider := s.companions
	s.compMu.RUnlock()

	if provider == nil {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	for _, c := range provider() {
		if c.Name == name {
			for _, ch := range c.Channels {
				if ch.Name == channelName {
					writeJSON(w, http.StatusOK, map[string]string{
						"name": ch.Name,
						"key":  hex.EncodeToString(ch.PSK),
					})
					return
				}
			}
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
	}

	writeError(w, http.StatusNotFound, "companion not found")
}

func (s *Server) handleGetContactPath(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pubkey := r.PathValue("pubkey")

	s.repeaterMu.RLock()
	rl := s.repeaterLookup
	s.repeaterMu.RUnlock()

	if rl == nil {
		writeError(w, http.StatusServiceUnavailable, "not available")
		return
	}

	ops, ok := rl(name)
	if !ok || ops == nil {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	info, err := ops.PathGet(pubkey)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleResetContactPath(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pubkey := r.PathValue("pubkey")

	s.repeaterMu.RLock()
	rl := s.repeaterLookup
	s.repeaterMu.RUnlock()

	if rl == nil {
		writeError(w, http.StatusServiceUnavailable, "not available")
		return
	}

	ops, ok := rl(name)
	if !ok || ops == nil {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	if err := ops.PathReset(pubkey); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
