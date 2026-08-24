package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// Repeater NODE routes — the single repeater the bot RUNS (relay + advert),
// distinct from the /api/companions/{name}/repeaters/* routes that drive REMOTE
// repeaters. Config is a singleton resource (like settings/mqtt); runtime state
// (relay counters, neighbours) comes from the live node via the backend.

// repeaterDTO is the secret-redacted read shape. privateKey and passwords are
// never sent — only *Set booleans, matching the companion/broker pattern.
type repeaterDTO struct {
	Configured          bool                `json:"configured"`
	Running             bool                `json:"running"`
	Name                string              `json:"name"`
	PubKey              string              `json:"pubkey"`
	PrivateKeySet       bool                `json:"privateKeySet"`
	Latitude            *float64            `json:"latitude"`
	Longitude           *float64            `json:"longitude"`
	AdvertInterval      *int                `json:"advertInterval"`
	FloodAdvertInterval *int                `json:"floodAdvertInterval"`
	DisableFwd          *bool               `json:"disableFwd"`
	FloodMax            *int                `json:"floodMax"`
	FloodMaxUnscoped    *int                `json:"floodMaxUnscoped"`
	FloodMaxAdvert      *int                `json:"floodMaxAdvert"`
	LoopDetect          *string             `json:"loopDetect"`
	PathHashMode        *int                `json:"pathHashMode"`
	DefaultRegion       string              `json:"defaultRegion"`
	AdminPasswordSet    bool                `json:"adminPasswordSet"`
	GuestPasswordSet    bool                `json:"guestPasswordSet"`
	OwnerInfo           string              `json:"ownerInfo"`
	Regions             []repeaterRegionDTO `json:"regions"`
}

type repeaterRegionDTO struct {
	Name      string `json:"name"`
	DenyFlood bool   `json:"denyFlood"`
}

func (s *Server) handleGetRepeater(w http.ResponseWriter, r *http.Request) {
	running := false
	if b := s.backendRef(); b != nil {
		_, running = b.RepeaterNode()
	}

	rep, err := s.store.Repeater.Get(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, repeaterDTO{Configured: false, Running: running})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read repeater")
		return
	}

	regions := make([]repeaterRegionDTO, 0, len(rep.Regions))
	for _, rg := range rep.Regions {
		regions = append(regions, repeaterRegionDTO{Name: rg.Name, DenyFlood: rg.DenyFlood})
	}

	writeJSON(w, http.StatusOK, repeaterDTO{
		Configured:          true,
		Running:             running,
		Name:                rep.Name,
		PubKey:              rep.PubKey,
		PrivateKeySet:       rep.PrivateKey != "",
		Latitude:            rep.Latitude,
		Longitude:           rep.Longitude,
		AdvertInterval:      rep.AdvertInterval,
		FloodAdvertInterval: rep.FloodAdvertInterval,
		DisableFwd:          rep.DisableFwd,
		FloodMax:            rep.FloodMax,
		FloodMaxUnscoped:    rep.FloodMaxUnscoped,
		FloodMaxAdvert:      rep.FloodMaxAdvert,
		LoopDetect:          rep.LoopDetect,
		PathHashMode:        rep.PathHashMode,
		DefaultRegion:       rep.DefaultRegion,
		AdminPasswordSet:    rep.AdminPassword != "",
		GuestPasswordSet:    rep.GuestPassword != "",
		OwnerInfo:           rep.OwnerInfo,
		Regions:             regions,
	})
}

// repeaterSection reads a JSON section body and applies it through fn (a
// Backend method expression), writing 204 on success, 400 on bad JSON, or 422
// on validation / not-configured failure. Shared by the create + section PUTs.
func repeaterSection[T any](s *Server, w http.ResponseWriter, r *http.Request, fn func(Backend, context.Context, T) error) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	var in T
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := fn(b, r.Context(), in); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreateRepeater(w http.ResponseWriter, r *http.Request) {
	repeaterSection(s, w, r, Backend.CreateRepeater)
}

func (s *Server) handleUpdateRepeaterNode(w http.ResponseWriter, r *http.Request) {
	repeaterSection(s, w, r, Backend.UpdateRepeaterNode)
}

func (s *Server) handleUpdateRepeaterRelay(w http.ResponseWriter, r *http.Request) {
	repeaterSection(s, w, r, Backend.UpdateRepeaterRelay)
}

func (s *Server) handleUpdateRepeaterAdmin(w http.ResponseWriter, r *http.Request) {
	repeaterSection(s, w, r, Backend.UpdateRepeaterAdmin)
}

func (s *Server) handleAddRepeaterRegion(w http.ResponseWriter, r *http.Request) {
	repeaterSection(s, w, r, Backend.AddRepeaterRegion)
}

// handlePatchRepeaterRegion toggles a region's deny-flood flag. The region name
// is the (percent-decoded) path segment, so "*" arrives as %2A.
func (s *Server) handlePatchRepeaterRegion(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	var in struct {
		DenyFlood bool `json:"denyFlood"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := b.SetRepeaterRegionFlood(r.Context(), r.PathValue("name"), in.DenyFlood); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteRepeaterRegion(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	if err := b.RemoveRepeaterRegion(r.Context(), r.PathValue("name")); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	if err := b.DeleteRepeater(r.Context()); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// repeaterNodeOps returns the running repeater's ops, or writes 404 when no
// repeater is running (503 when the backend isn't wired yet).
func (s *Server) repeaterNodeOps(w http.ResponseWriter) (*RepeaterNodeOps, bool) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "not available")
		return nil, false
	}
	ops, ok := b.RepeaterNode()
	if !ok {
		writeError(w, http.StatusNotFound, "no repeater running")
		return nil, false
	}
	return ops, true
}

func (s *Server) handleRepeaterNodeStatus(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterNodeOps(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ops.Stats())
}

func (s *Server) handleRepeaterNodeNeighbors(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterNodeOps(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ops.Neighbors())
}

func (s *Server) handleRepeaterNodeACL(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterNodeOps(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ops.ACL())
}

// handleRepeaterNodeRevoke drops a client from the repeater's ACL. The pubkey is
// the (percent-decoded) path segment.
func (s *Server) handleRepeaterNodeRevoke(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterNodeOps(w)
	if !ok {
		return
	}
	if err := ops.RevokeACL(r.PathValue("pubkey")); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRepeaterNodeAdvert(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterNodeOps(w)
	if !ok {
		return
	}
	// Body is optional; default to a flood advert. A malformed/empty body just
	// falls back to the default rather than erroring.
	var body struct {
		Flood *bool `json:"flood"`
	}
	_ = readJSON(r, &body)
	flood := true
	if body.Flood != nil {
		flood = *body.Flood
	}
	if err := ops.Advert(flood); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
