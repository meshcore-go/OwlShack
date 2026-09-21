package api

import (
	"net/http"
)

// handleSensors lists every configured sensor with its current state. There is no separate list
// endpoint: the configuration and the readings come from one snapshot, so the page can never show a
// sensor whose reading belongs to a different set.
func (s *Server) handleSensors(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		// Not an empty list: that would read as "no sensors are configured".
		writeError(w, http.StatusServiceUnavailable, "sensors are not ready yet")
		return
	}
	writeJSON(w, http.StatusOK, b.Sensors())
}

func (s *Server) handleSensorProviders(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "sensors are not ready yet")
		return
	}
	writeJSON(w, http.StatusOK, b.SensorProviders(r.Context()))
}

// handleSensorDiscover asks one provider what it can see. POST because it talks to hardware, even
// though it configures nothing: probing a bus is not something to do on every page load.
func (s *Server) handleSensorDiscover(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	found, err := b.DiscoverSensors(r.Context(), body.Provider)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": found})
}

func (s *Server) handleCreateSensor(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	var in SensorInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	id, err := b.CreateSensor(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) handleDeleteSensor(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := b.DeleteSensor(r.Context(), id); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
