package api

import (
	"net/http"
	"strconv"
)

func (s *Server) repeaterOps(name string) (*RepeaterOps, bool) {
	b := s.backendRef()
	if b == nil {
		return nil, false
	}
	return b.Repeater(name)
}

func (s *Server) handleRepeaterLogin(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := ops.Login(r.PathValue("pubkey"), body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleRoomLogin logs in to a room server. When the body omits syncSince it
// is derived from the newest stored post, so only unseen posts backfill.
func (s *Server) handleRoomLogin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ops, ok := s.repeaterOps(name)
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}
	if ops.RoomLogin == nil {
		writeError(w, http.StatusServiceUnavailable, "room login not available")
		return
	}

	var body struct {
		Password  string `json:"password"`
		SyncSince *int64 `json:"syncSince"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pubkey := r.PathValue("pubkey")
	var syncSince uint32
	if body.SyncSince != nil {
		if *body.SyncSince > 0 {
			syncSince = uint32(*body.SyncSince)
		}
	} else if cid, err := s.store.Companions.IDByName(name); err == nil {
		if latest, lerr := s.store.Messages.LatestRx(cid, "dm:"+pubkey); lerr == nil && latest != nil {
			syncSince = uint32(latest.Timestamp.Unix())
		}
	}

	result, err := ops.RoomLogin(pubkey, body.Password, syncSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRepeaterStatus(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	status, err := ops.StatusReq(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRepeaterCLI(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var body struct {
		Command string `json:"command"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	response, err := ops.CLI(r.PathValue("pubkey"), body.Command)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"response": response})
}

func (s *Server) handleRepeaterSession(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	sess := ops.Session(r.PathValue("pubkey"))
	if sess == nil {
		writeJSON(w, http.StatusOK, map[string]any{"loggedIn": false})
		return
	}

	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleRepeaterLogout(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	ops.Logout(r.PathValue("pubkey"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRepeaterPathGet(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	info, err := ops.PathGet(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRepeaterPathReset(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	if err := ops.PathReset(r.PathValue("pubkey")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepeaterPathSet(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var body struct {
		Path         string `json:"path"`
		PathHashSize int    `json:"pathHashSize"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := ops.PathSet(r.PathValue("pubkey"), body.Path, body.PathHashSize); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepeaterNeighbors(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	// Default to 10 — the firmware's results buffer holds ~11 entries with a
	// 6-byte prefix + 4 + 1 = 11-byte stride, so 10 is a safe ceiling per page.
	count := uint8(10)
	if v := r.URL.Query().Get("count"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 8); err == nil && n > 0 {
			count = uint8(n)
		}
	}
	var offset uint16
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 16); err == nil {
			offset = uint16(n)
		}
	}

	res, err := ops.NeighborsReq(r.PathValue("pubkey"), count, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRepeaterOwnerInfo(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	res, err := ops.OwnerInfoReq(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRepeaterAccessList(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	res, err := ops.AccessList(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRepeaterAccessSet(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	var body struct {
		Permissions uint8 `json:"permissions"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := ops.SetPerm(r.PathValue("pubkey"), r.PathValue("target"), body.Permissions); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepeaterAccessRemove(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	if err := ops.SetPerm(r.PathValue("pubkey"), r.PathValue("target"), 0); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepeaterTelemetry(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	res, err := ops.TelemetryReq(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleContactTelemetry(w http.ResponseWriter, r *http.Request) {
	ops, ok := s.repeaterOps(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "companion not found")
		return
	}

	res, err := ops.ContactTelemetryReq(r.PathValue("pubkey"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}
