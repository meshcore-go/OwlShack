package api

import (
	"errors"
	"io"
	"net/http"
)

// handleSensors lists sensors and readings from one snapshot, so the page cannot mix two sets.
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

// handleSensorDiscover scans for parts, every provider unless one is named; POST because it talks to hardware.
func (s *Server) handleSensorDiscover(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	// A chunked body has no ContentLength to test, so decode always and treat only EOF as nothing sent.
	if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	scan, err := b.DiscoverSensors(r.Context(), body.Provider)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scan)
}

// handleSensorKinds touches no hardware, so it answers for a bus that is unavailable or not yet wired.
func (s *Server) handleSensorKinds(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "sensors are not ready yet")
		return
	}
	kinds, err := b.SensorKinds(r.URL.Query().Get("provider"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, kinds)
}

func (s *Server) handleUpdateSensor(w http.ResponseWriter, r *http.Request) {
	b, ok := s.configBackend(w)
	if !ok {
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in SensorInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := b.UpdateSensor(r.Context(), id, in); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
