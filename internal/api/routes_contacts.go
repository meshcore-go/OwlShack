package api

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/store"
)

type contactJSON struct {
	PeerPubKey string                `json:"peerPubkey"`
	Name       string                `json:"name"`
	Type       string                `json:"type"`
	AddedAt    string                `json:"addedAt"`
	Metadata   store.ContactMetadata `json:"metadata"`
}

// contactToJSON resolves the contact's display name/type from discovered_peers.
func (s *Server) contactToJSON(c *store.Contact) contactJSON {
	peerName := ""
	peerType := ""
	if peer, err := s.store.Peers.GetByPubKey(c.PeerPubKey); err == nil && peer != nil {
		peerName = peer.Name
		peerType = peer.Type
	}
	return contactJSON{
		PeerPubKey: hex.EncodeToString(c.PeerPubKey),
		Name:       peerName,
		Type:       peerType,
		AddedAt:    c.AddedAt.UTC().Format(time.RFC3339),
		Metadata:   c.Metadata,
	}
}

// validPeerType reports whether t is one of the recognised MeshCore peer
// types. Mirrors meshcore-go's advert type-string mapping.
func validPeerType(t string) bool {
	switch t {
	case "CHAT", "REPEATER", "ROOM", "SENSOR":
		return true
	}
	return false
}

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	contacts, err := s.store.Contacts.List(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list contacts")
		return
	}

	out := make([]contactJSON, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, s.contactToJSON(&c))
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	c, err := s.store.Contacts.Get(name, pubkey)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "contact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load contact")
		return
	}

	writeJSON(w, http.StatusOK, s.contactToJSON(c))
}

func (s *Server) handleAddContact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var body struct {
		PubKey string `json:"pubkey"`
		Name   string `json:"name"`
		Type   string `json:"type"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pubkey, err := hex.DecodeString(body.PubKey)
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	// Optional display metadata (manual entry or a shared-contact embed).
	contactName := strings.TrimSpace(body.Name)
	contactType := strings.ToUpper(strings.TrimSpace(body.Type))
	if contactType != "" && !validPeerType(contactType) {
		writeError(w, http.StatusBadRequest, "invalid contact type")
		return
	}

	// Seed discovered_peers so the contact row's FK is satisfiable. An unknown
	// peer is seeded even with blank name/type (the contract is POST {pubkey};
	// a later advert fills them in). Known peers are only updated when nameless
	// — never overwrite a peer we've already heard, so a crafted shared-contact
	// message can't relabel a trusted peer. Existing rows are loaded and
	// mutated in place to preserve lat/lon/path.
	var seedPeer *store.Peer
	if existing, gerr := s.store.Peers.GetByPubKey(pubkey); gerr == nil {
		if existing == nil {
			seedPeer = &store.Peer{
				PubKey:   pubkey,
				Name:     contactName,
				Type:     contactType,
				LastSeen: time.Now(),
			}
		} else if existing.Name == "" && (contactName != "" || contactType != "") {
			existing.Name = contactName
			if contactType != "" {
				existing.Type = contactType
			}
			seedPeer = existing
		}
	}

	var addErr error
	s.store.WriteSync(func() {
		// Seed the peer BEFORE the contact row: companion_contacts.peer_pubkey
		// has a foreign key to discovered_peers, so for a never-heard peer the
		// contact insert fails unless the peer row exists first.
		if seedPeer != nil {
			// Non-fatal for known peers: the contact is still added, just nameless.
			_ = s.store.Peers.Upsert(seedPeer)
		}
		addErr = s.store.Contacts.Add(name, pubkey)
	})
	if addErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to add contact")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pubkeyHex := r.PathValue("pubkey")

	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Contacts.Delete(name, pubkey)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateContactMetadata(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pubkeyHex := r.PathValue("pubkey")

	if !s.companionExists(name) {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	var meta store.ContactMetadata
	if err := readJSON(r, &meta); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var updateErr error
	s.store.WriteSync(func() {
		updateErr = s.store.Contacts.UpdateMetadata(name, pubkey, meta)
	})
	if updateErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to update contact metadata")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) companionExists(name string) bool {
	provider := s.companionProvider()

	if provider == nil {
		return false
	}

	for _, c := range provider() {
		if c.Name == name {
			return true
		}
	}
	return false
}
