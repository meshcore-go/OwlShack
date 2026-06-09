package api

import (
	"net/http"
)

func (s *Server) handleListCompanions(w http.ResponseWriter, r *http.Request) {
	provider := s.companionProvider()

	if provider == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	writeJSON(w, http.StatusOK, provider())
}
