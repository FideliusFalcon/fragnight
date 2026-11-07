package web

import (
	"errors"
	"net/http"
	"slices"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

func (s *Server) statsRoutes() {
	s.mux.HandleFunc("GET /stats", s.handleStats)
	s.mux.HandleFunc("GET /history", s.handleHistory)
	s.mux.HandleFunc("GET /player/{steamid}", s.handlePlayer)
	s.mux.HandleFunc("GET /match/{id}", s.handleMatch)
	s.mux.HandleFunc("GET /admin", s.handleAdmin)
	s.mux.HandleFunc("POST /admin/servers/{id}/reset", s.handleAdminReset)
	s.mux.HandleFunc("POST /admin/matches/{id}/delete", s.handleAdminDelete)
	s.mux.HandleFunc("POST /admin/lobbies/{id}/delete", s.handleAdminDeleteLobby)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, nil)
		return
	}
	http.Error(w, "error", http.StatusInternalServerError)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	rows, err := s.Store.Leaderboard(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "stats", page{Title: "Leaderboard", Tab: "stats", Me: &me, Data: rows})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	rows, err := s.Store.MatchesOf(r.Context(), me.SteamID, 50)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "history", page{Title: "Match history", Tab: "history", Me: &me, Data: rows})
}

func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("steamid")
	if !steamIDRe.MatchString(id) {
		s.render(w, http.StatusNotFound, "notfound", page{Title: "Not found", Me: &me})
		return
	}
	sum, err := s.Store.PlayerSummary(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, http.StatusNotFound, "notfound", page{Title: "Not found", Me: &me})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "player", page{Title: sum.Name, Tab: "stats", Me: &me, Data: sum})
}

func (s *Server) handleMatch(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	d, err := s.Store.MatchDetail(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, http.StatusNotFound, "notfound", page{Title: "Not found", Me: &me})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	var t1, t2 []store.DetailRow
	for _, p := range d.Players {
		if p.Team == 1 {
			t1 = append(t1, p)
		} else {
			t2 = append(t2, p)
		}
	}
	s.render(w, http.StatusOK, "match", page{Title: "Scoreboard", Tab: "history", Me: &me,
		Data: map[string]any{"M": d, "Team1": t1, "Team2": t2}})
}

func (s *Server) isAdmin(steamID string) bool {
	return slices.Contains(s.Config.AdminSteamIDs, steamID)
}

// requireAdmin returns the player if they are an admin; otherwise it has already
// answered (redirect to login, or 403).
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (lobby.Player, bool) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return me, false
	}
	if !s.isAdmin(me.SteamID) {
		http.Error(w, "admins only", http.StatusForbidden)
		return me, false
	}
	return me, true
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	matches, err := s.Store.RecentMatches(r.Context(), 30)
	if err != nil {
		s.fail(w, err)
		return
	}
	var servers any
	if s.Matches != nil {
		servers = s.Matches.PoolState()
	}
	s.render(w, http.StatusOK, "admin", page{Title: "Admin", Me: &me, Data: map[string]any{"Servers": servers, "Matches": matches, "Lobbies": s.Lobbies.ListAll()}})
}

func (s *Server) handleAdminReset(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.Matches == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if err := s.Matches.ResetServer(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAdminDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.Store.DeleteMatch(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAdminDeleteLobby(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	// Closing releases everyone; the match orchestrator hears about it through
	// the lobby manager's OnEmpty hook and aborts any running match.
	if err := s.Lobbies.Close(r.PathValue("id")); err != nil && !errors.Is(err, lobby.ErrNotFound) {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
