// Package web serves the lobby UI: HTML pages, static files and WebSockets.
package web

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/auth"
	"github.com/fideliusfalcon/fragnight/internal/config"
	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps are the collaborators the web layer needs.
type Deps struct {
	Config   config.Config
	Store    *store.Store
	Sessions *auth.Sessions
	Lobbies  *lobby.Manager
	HTTP     *http.Client
	// SteamEndpoint overrides Steam's OpenID URL (tests); default is Steam's.
	SteamEndpoint string
}

// Server is the HTTP handler for the whole app.
type Server struct {
	Deps
	mux  *http.ServeMux
	tpls map[string]*template.Template
}

var steamIDRe = regexp.MustCompile(`^\d{17}$`)

const nextCookie = "fragnight_next"

// New builds the HTTP handler.
func New(d Deps) *Server {
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if d.SteamEndpoint == "" {
		d.SteamEndpoint = auth.SteamOpenIDEndpoint
	}
	s := &Server{Deps: d, mux: http.NewServeMux(), tpls: map[string]*template.Template{}}
	s.loadTemplates()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) loadTemplates() {
	funcs := template.FuncMap{
		"open":    func(n int) []int { return make([]int, max(lobby.TeamSize-n, 0)) },
		"seconds": func(ms int64) int64 { return (ms + 999) / 1000 },
		"dict2": func(members []lobby.Member, n, size int, me lobby.Player) map[string]any {
			return map[string]any{"Members": members, "N": n, "Size": size, "Me": me}
		},
		"statusLabel": func(st lobby.Status) string {
			switch st {
			case lobby.StatusWaiting:
				return "Waiting"
			case lobby.StatusVoting:
				return "Voting"
			case lobby.StatusAwaitingServer:
				return "Waiting for a free server"
			case lobby.StatusInMatch:
				return "In match"
			case lobby.StatusFinished:
				return "Finished"
			}
			return string(st)
		},
	}
	pages, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		panic(err)
	}
	for _, p := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		s.tpls[name] = template.Must(template.New("layout").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", p))
	}
}

func (s *Server) routes() {
	static, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("GET /auth/steam", s.handleSteamStart)
	s.mux.HandleFunc("GET "+auth.CallbackPath, s.handleSteamCallback)
	s.mux.HandleFunc("GET /auth/dev", s.handleDevLogin)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
	s.mux.HandleFunc("POST /lobbies", s.handleCreateLobby)
	s.mux.HandleFunc("GET /lobby/{id}", s.handleLobbyPage)
	s.mux.HandleFunc("GET /ws/browser", s.handleBrowserWS)
	s.mux.HandleFunc("GET /ws/lobby/{id}", s.handleLobbyWS)
}

// page is the data every template receives.
type page struct {
	Title string
	Tab   string
	Me    *lobby.Player
	Data  any
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	t, ok := s.tpls[name]
	if !ok {
		http.Error(w, "missing template", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// me returns the logged-in player, if any.
func (s *Server) me(r *http.Request) (lobby.Player, bool) {
	id, ok := s.Sessions.SteamID(r)
	if !ok {
		return lobby.Player{}, false
	}
	p, err := s.Store.GetPlayer(r.Context(), id)
	if err != nil {
		return lobby.Player{}, false
	}
	return lobby.Player{SteamID: p.SteamID, Name: p.Name, AvatarURL: p.AvatarURL}, true
}

// requireLogin redirects to /login (remembering where the player was going).
func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) (lobby.Player, bool) {
	p, ok := s.me(r)
	if !ok {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
	return p, ok
}

func safeNext(n string) string {
	if strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "//") && !strings.Contains(n, "\\") {
		return n
	}
	return "/"
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if n := r.URL.Query().Get("next"); n != "" {
		http.SetCookie(w, &http.Cookie{Name: nextCookie, Value: url.QueryEscape(safeNext(n)), Path: "/", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	s.render(w, http.StatusOK, "login", page{Title: "Sign in", Data: map[string]any{"Dev": s.Config.DevLogin, "Error": r.URL.Query().Get("error")}})
}

// finishLogin stores the player, starts a session and redirects.
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, p store.Player) {
	if err := s.Store.UpsertPlayer(r.Context(), p); err != nil {
		http.Error(w, "could not save player", http.StatusInternalServerError)
		return
	}
	s.Sessions.Issue(w, p.SteamID)
	dest := "/"
	if c, err := r.Cookie(nextCookie); err == nil {
		if v, err := url.QueryUnescape(c.Value); err == nil {
			dest = safeNext(v)
		}
		http.SetCookie(w, &http.Cookie{Name: nextCookie, Value: "", Path: "/", MaxAge: -1})
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleSteamStart(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, auth.LoginURL(s.Config.BaseURL), http.StatusSeeOther)
}

func (s *Server) handleSteamCallback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	id, err := auth.Verify(ctx, s.HTTP, s.SteamEndpoint, s.Config.BaseURL, r.URL.Query())
	if err != nil {
		log.Printf("steam login failed: %v", err)
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Steam login failed. The lobby server needs internet access to steamcommunity.com."), http.StatusSeeOther)
		return
	}
	s.finishLogin(w, r, auth.FetchProfile(ctx, s.HTTP, s.Config.SteamAPIKey, id))
}

// handleDevLogin lets developers sign in without Steam. Only when DEV_LOGIN=1.
func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !s.Config.DevLogin {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("steamid")
	if !steamIDRe.MatchString(id) {
		http.Error(w, "steamid must be 17 digits", http.StatusBadRequest)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "dev-" + id[len(id)-4:]
	}
	s.finishLogin(w, r, store.Player{SteamID: id, Name: name})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "browser", page{Title: "Find Lobbies", Tab: "public", Me: &me, Data: s.Lobbies.ListPublic()})
}

func (s *Server) handleCreateLobby(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	public := r.PostForm.Get("public") != ""
	snap, err := s.Lobbies.Create(me, r.PostForm.Get("name"), public)
	if errors.Is(err, lobby.ErrAlreadyInLobby) {
		if id, in := s.Lobbies.LobbyOf(me.SteamID); in {
			http.Redirect(w, r, "/lobby/"+id, http.StatusSeeOther)
			return
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/lobby/"+snap.ID, http.StatusSeeOther)
}

// lobbyView is the server-rendered state of a lobby page.
type lobbyView struct {
	Snap       lobby.Snapshot
	Team1      []lobby.Member
	Team2      []lobby.Member
	Unassigned []lobby.Member
	Me         lobby.Player
	IsHost     bool
	MyTeam     lobby.Team
	Maps       []mapRow
	TeamSize   int
}

type mapRow struct {
	Name  string
	Votes int
	Pct   int
}

func newLobbyView(snap lobby.Snapshot, me lobby.Player) lobbyView {
	v := lobbyView{Snap: snap, Me: me, IsHost: snap.HostSteamID == me.SteamID, TeamSize: lobby.TeamSize}
	total := 0
	for _, n := range snap.Tally {
		total += n
	}
	for _, m := range snap.Members {
		switch m.Team {
		case lobby.Team1:
			v.Team1 = append(v.Team1, m)
		case lobby.Team2:
			v.Team2 = append(v.Team2, m)
		default:
			v.Unassigned = append(v.Unassigned, m)
		}
		if m.SteamID == me.SteamID {
			v.MyTeam = m.Team
		}
	}
	for _, name := range snap.Settings.Maps {
		n := snap.Tally[name]
		pct := 0
		if total > 0 {
			pct = n * 100 / total
		}
		v.Maps = append(v.Maps, mapRow{Name: name, Votes: n, Pct: pct})
	}
	return v
}

func (s *Server) handleLobbyPage(w http.ResponseWriter, r *http.Request) {
	me, ok := s.requireLogin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	snap, err := s.Lobbies.Join(id, me)
	switch {
	case errors.Is(err, lobby.ErrNotFound):
		s.render(w, http.StatusNotFound, "notfound", page{Title: "Lobby not found", Me: &me})
		return
	case errors.Is(err, lobby.ErrAlreadyInLobby):
		if cur, in := s.Lobbies.LobbyOf(me.SteamID); in {
			http.Redirect(w, r, "/lobby/"+cur, http.StatusSeeOther)
			return
		}
	case err != nil:
		// e.g. the match already started: show it read-only if we can
		var found bool
		if snap, found = s.Lobbies.Get(id); !found {
			http.NotFound(w, r)
			return
		}
	}
	s.render(w, http.StatusOK, "lobby", page{Title: snap.Name, Me: &me, Data: newLobbyView(snap, me)})
}
