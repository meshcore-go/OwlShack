package api

import (
	"net/http"
)

// ConfigGetter returns the current config as a JSON-serializable struct.
type ConfigGetter func() any

// ConfigUpdater validates and applies a new config from JSON input.
// It returns an error if validation fails or the config cannot be applied.
type ConfigUpdater func(map[string]any) error

func (s *Server) SetConfigGetter(fn ConfigGetter) {
	s.cfgMu.Lock()
	s.configGetter = fn
	s.cfgMu.Unlock()
}

func (s *Server) ConfigGetter() ConfigGetter {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.configGetter
}

func (s *Server) SetConfigUpdater(fn ConfigUpdater) {
	s.cfgMu.Lock()
	s.configUpdater = fn
	s.cfgMu.Unlock()
}

func (s *Server) ConfigUpdater() ConfigUpdater {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.configUpdater
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	getter := s.ConfigGetter()
	if getter == nil {
		writeError(w, http.StatusServiceUnavailable, "config not available")
		return
	}

	writeJSON(w, http.StatusOK, getter())
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	updater := s.ConfigUpdater()
	if updater == nil {
		writeError(w, http.StatusServiceUnavailable, "config updates not available")
		return
	}

	var input map[string]any
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if len(input) == 0 {
		writeError(w, http.StatusBadRequest, "empty config body")
		return
	}

	if err := updater(input); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
