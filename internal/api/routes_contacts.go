package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/store"
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

// contactToJSON serializes a contact's own cached record, independent of discovered_peers.
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

// validPeerType mirrors meshcore-go's advert type-string mapping.
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
	if err != nil || len(pubkey) != meshcore.PubKeySize {
		writeError(w, http.StatusBadRequest, "pubkey must be 64 hex chars")
		return
	}

	// Our own companion identities show up as discovered peers, so reject a self-contact.
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

	// A known peer is only relabelled when nameless, so a crafted shared-contact can't rename a trusted peer.
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
		// Seed the peer so it appears in the Peers list.
		if seedPeer != nil {
			_ = s.store.Peers.Upsert(r.Context(), seedPeer)
		}
		if addErr = s.store.Contacts.Add(r.Context(), cid, pubkey, storeName, storeType); addErr != nil {
			return
		}
		// Backfill from the heard peer so location/path/feat don't wait for the next advert.
		if existing != nil {
			hasLoc := existing.HasLocation()
			_ = s.store.Contacts.RefreshFromAdvert(
				r.Context(), pubkey, existing.Name, existing.Type,
				existing.Lat, existing.Lon, existing.Feat1, existing.Feat2,
				existing.LastSeen, existing.LastAdvertTS, hasLoc,
			)
			if len(existing.OutPath) > 0 {
				hs := existing.OutPathHashSize
				if hs == 0 {
					hs = 1
				}
				_ = s.store.Contacts.UpdateOutPath(r.Context(), cid, pubkey, node.ReverseHops(existing.OutPath, int(hs)), hs)
			}
		}
	})
	if addErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to add contact")
		return
	}

	// Without this, a contact added by pubkey alone fails every radio op until a restart re-hydrates it.
	if add := s.peerAdder(); add != nil {
		add(pubkey, storeName, storeType)
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

// handleSetContactLocation takes lat/lon in microdegrees; a later advert with a position overwrites it.
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

// companionID maps the name in the URL to the surrogate id, writing a 404 when there is no such companion.
func (s *Server) companionID(ctx context.Context, w http.ResponseWriter, name string) (int64, bool) {
	id, err := s.store.Companions.IDByName(ctx, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "companion not found")
		return 0, false
	}
	return id, true
}
