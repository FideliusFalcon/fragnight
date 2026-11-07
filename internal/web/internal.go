package web

import (
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

func (s *Server) internalRoutes() {
	s.mux.HandleFunc("GET /internal/match/{id}/config.json", s.handleMatchConfig)
	s.mux.HandleFunc("POST /internal/events/{server}", s.handleEvent)
}

// handleEvent receives MatchZy webhook events. The shared secret header is the
// only credential; with no secret configured every request is refused.
func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	secret := s.Config.WebhookSecret
	got := r.Header.Get("X-Webhook-Secret")
	if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ev, err := matchzy.ParseEvent(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.Matches == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if err := s.Matches.HandleEvent(r.PathValue("server"), ev); err != nil {
		log.Printf("event %s from %s: %v", ev.Name, r.PathValue("server"), err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
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
