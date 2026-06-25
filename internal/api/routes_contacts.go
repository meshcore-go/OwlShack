package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/store"
)

type contactJSON struct {
	PeerPubKey      string                `json:"peerPubkey"`
	Name            string                `json:"name"`
	Type            string                `json:"type"`
	Lat             int32                 `json:"lat"`
	Lon             int32                 `json:"lon"`
	Feat1           uint16                `json:"feat1"`
	Feat2           uint16                `json:"feat2"`
	OutPath         string                `json:"outPath,omitempty"`
	OutPathHashSize uint8                 `json:"outPathHashSize"`
	LastSeen        string                `json:"lastSeen,omitempty"`
	LastAdvertTS    uint32                `json:"lastAdvertTs"`
	AddedAt         string                `json:"addedAt"`
	Metadata        store.ContactMetadata `json:"metadata"`
}

// contactToJSON serializes a contact from its own cached record (identity/
// location/path/last-seen, kept fresh by adverts + path learning) —
// independent of discovered_peers.
func (s *Server) contactToJSON(c *store.Contact) contactJSON {
	lastSeen := ""
	if !c.LastSeen.IsZero() {
		lastSeen = c.LastSeen.UTC().Format(time.RFC3339)
	}
	return contactJSON{
		PeerPubKey:      hex.EncodeToString(c.PeerPubKey),
		Name:            c.Name,
		Type:            c.Type,
		Lat:             c.Lat,
		Lon:             c.Lon,
		Feat1:           c.Feat1,
		Feat2:           c.Feat2,
		OutPath:         hex.EncodeToString(c.OutPath),
		OutPathHashSize: c.OutPathHashSize,
		LastSeen:        lastSeen,
		LastAdvertTS:    c.LastAdvertTS,
		AddedAt:         c.AddedAt.UTC().Format(time.RFC3339),
		Metadata:        c.Metadata,
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
	cid, ok := s.companionID(r.Context(), w, name)
	if !ok {
		return
	}

	contacts, err := s.store.Contacts.List(r.Context(), cid)
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
	cid, ok := s.companionID(r.Context(), w, name)
	if !ok {
		return
	}

	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	c, err := s.store.Contacts.Get(r.Context(), cid, pubkey)
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
	cid, ok := s.companionID(r.Context(), w, name)
	if !ok {
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

	// A node can't be its own peer: our own companion identities show up as
	// discovered peers (e.g. on the map), so reject adding any of them as a
	// contact rather than creating a nonsensical self-contact row.
	if comps, lerr := s.store.Companions.List(r.Context()); lerr == nil {
		selfHex := hex.EncodeToString(pubkey)
		for _, c := range comps {
			if strings.EqualFold(c.PubKey, selfHex) {
				writeError(w, http.StatusBadRequest, "cannot add your own node as a contact")
				return
			}
		}
	}

	// Optional display metadata (manual entry or a shared-contact embed).
	contactName := strings.TrimSpace(body.Name)
	contactType := strings.ToUpper(strings.TrimSpace(body.Type))
	if contactType != "" && !validPeerType(contactType) {
		writeError(w, http.StatusBadRequest, "invalid contact type")
		return
	}

	// A peer we've already heard, used to (a) backfill the contact's cached
	// name/type when the request didn't carry them, and (b) seed discovered_peers
	// so the peer shows up in the Peers list. Known peers are only relabelled
	// when nameless, so a crafted shared-contact message can't rename a trusted
	// peer.
	existing, _ := s.store.Peers.GetByPubKey(r.Context(), pubkey)

	// The contact owns its identity now; backfill from a known peer when blank.
	storeName, storeType := contactName, contactType
	if storeName == "" && existing != nil {
		storeName = existing.Name
		if storeType == "" {
			storeType = existing.Type
		}
	}

	var seedPeer *store.Peer
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

	var addErr error
	s.store.WriteSync(func() {
		// Seed the peer so it appears in the Peers list. No longer required for
		// referential integrity — contacts are decoupled from discovered_peers.
		if seedPeer != nil {
			_ = s.store.Peers.Upsert(r.Context(), seedPeer)
		}
		if addErr = s.store.Contacts.Add(r.Context(), cid, pubkey, storeName, storeType); addErr != nil {
			return
		}
		// Backfill the full record from what we already know about the peer, so a
		// contact added from a heard peer has location/path/feat immediately
		// rather than waiting for the next advert.
		if existing != nil {
			hasLoc := existing.Lat != 0 || existing.Lon != 0
			_ = s.store.Contacts.RefreshFromAdvert(
				r.Context(), pubkey, existing.Name, existing.Type,
				existing.Lat, existing.Lon, existing.Feat1, existing.Feat2,
				existing.LastSeen, existing.LastAdvertTS, hasLoc,
			)
			if len(existing.OutPath) > 0 {
				_ = s.store.Contacts.UpdateOutPath(r.Context(), cid, pubkey, existing.OutPath, existing.OutPathHashSize)
			}
		}
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

	cid, ok := s.companionID(r.Context(), w, name)
	if !ok {
		return
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Contacts.Delete(r.Context(), cid, pubkey)
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

	cid, ok := s.companionID(r.Context(), w, name)
	if !ok {
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
		updateErr = s.store.Contacts.UpdateMetadata(r.Context(), cid, pubkey, meta)
	})
	if updateErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to update contact metadata")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSetContactLocation hand-sets a contact's location (lat/lon in
// microdegrees, matching discovered_peers). A later advert carrying a position
// overwrites it ("advert wins").
func (s *Server) handleSetContactLocation(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.companionID(r.Context(), w, r.PathValue("name"))
	if !ok {
		return
	}
	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}
	var body struct {
		Lat int32 `json:"lat"`
		Lon int32 `json:"lon"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var uerr error
	s.store.WriteSync(func() {
		uerr = s.store.Contacts.SetLocation(r.Context(), cid, pubkey, body.Lat, body.Lon)
	})
	if uerr != nil {
		writeError(w, http.StatusInternalServerError, "failed to set contact location")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// companionID resolves a companion's surrogate id from its current name (the
// API keeps name in URLs; per-companion history is keyed by id so a rename
// keeps it). Writes a 404 and returns ok=false when no companion has that name.
func (s *Server) companionID(ctx context.Context, w http.ResponseWriter, name string) (int64, bool) {
	id, err := s.store.Companions.IDByName(ctx, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "companion not found")
		return 0, false
	}
	return id, true
}
