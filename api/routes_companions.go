package api

import (
	"net/http"
)

func (s *Server) handleListCompanions(w http.ResponseWriter, r *http.Request) {
	s.compMu.RLock()
	provider := s.companions
	s.compMu.RUnlock()

	if provider == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	writeJSON(w, http.StatusOK, provider())
}
