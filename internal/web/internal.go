package web

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/fideliusfalcon/fragnight/internal/store"
)

func (s *Server) internalRoutes() {
	s.mux.HandleFunc("GET /internal/match/{id}/config.json", s.handleMatchConfig)
}

// handleMatchConfig serves a match's MatchZy config to the CS2 server. The
// per-match token is the only credential; the config stops being served once
// the match is no longer pending or running.
func (s *Server) handleMatchConfig(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMatch(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	token := r.URL.Query().Get("token")
	if m.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(m.Token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if m.Status != store.MatchPending && m.Status != store.MatchRunning {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(m.ConfigJSON))
}
