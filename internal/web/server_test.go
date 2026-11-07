package web

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/auth"
	"github.com/fideliusfalcon/fragnight/internal/config"
	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

type fakeMatches struct {
	mu     sync.Mutex
	events []string
	aborts []string
	err    error
}

func (f *fakeMatches) HandleEvent(serverID string, ev matchzy.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, serverID+":"+ev.Name+":"+ev.MatchID)
	return f.err
}

func (f *fakeMatches) Abort(lobbyID, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts = append(f.aborts, lobbyID+":"+actor)
	return f.err
}

type env struct {
	matches *fakeMatches
	srv     *Server
	ts      *httptest.Server
	lobbies *lobby.Manager
	store   *store.Store
}

func newEnv(t *testing.T, devLogin bool) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lm := lobby.NewManager(time.Now, rand.New(rand.NewSource(1)))
	fm := &fakeMatches{}
	srv := New(Deps{
		Matches:  fm,
		Config:   config.Config{BaseURL: "http://lan.local", DevLogin: devLogin, WebhookSecret: "hook-secret"},
		Store:    st,
		Sessions: auth.NewSessions([]byte("test-secret"), time.Hour),
		Lobbies:  lm,
		HTTP:     http.DefaultClient,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &env{srv: srv, ts: ts, lobbies: lm, store: st, matches: fm}
}

func sid(n int) string { return fmt.Sprintf("7656119800000%04d", n) }

// user is a browser: a cookie jar that does not follow redirects.
type user struct {
	c  *http.Client
	e  *env
	id string
}

func (e *env) login(t *testing.T, n int, name string) *user {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"steamid": {sid(n)}, "name": {name}}
	resp, err := c.Get(e.ts.URL + "/auth/dev?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("dev login status %d", resp.StatusCode)
	}
	return &user{c: c, e: e, id: sid(n)}
}

func (u *user) cookieHeader() http.Header {
	base, _ := url.Parse(u.e.ts.URL)
	var parts []string
	for _, ck := range u.c.Jar.Cookies(base) {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return http.Header{"Cookie": {strings.Join(parts, "; ")}}
}

func (u *user) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	resp, err := u.c.Get(u.e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (u *user) post(t *testing.T, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := u.c.PostForm(u.e.ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestIndex_RedirectsToLoginWithoutSession(t *testing.T) {
	e := newEnv(t, true)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(e.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 3 || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestDevLogin_DisabledByDefault(t *testing.T) {
	e := newEnv(t, false)
	resp, err := http.Get(e.ts.URL + "/auth/dev?steamid=" + sid(1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestDevLogin_RejectsBadSteamID(t *testing.T) {
	e := newEnv(t, true)
	resp, err := http.Get(e.ts.URL + "/auth/dev?steamid=abc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestLoginPage_ShowsSteamButton(t *testing.T) {
	e := newEnv(t, false)
	resp, err := http.Get(e.ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "/auth/steam") {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
}

func TestDevLogin_RedirectsToNext(t *testing.T) {
	e := newEnv(t, true)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := c.Get(e.ts.URL + "/login?next=/lobby/abc123")
	resp.Body.Close()
	resp, _ = c.Get(e.ts.URL + "/auth/dev?steamid=" + sid(1))
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/lobby/abc123" {
		t.Fatalf("location %q", loc)
	}
	// an off-site next is ignored
	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2, CheckRedirect: c.CheckRedirect}
	resp, _ = c2.Get(e.ts.URL + "/login?next=//evil.example")
	resp.Body.Close()
	resp, _ = c2.Get(e.ts.URL + "/auth/dev?steamid=" + sid(2))
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("off-site next accepted: %q", loc)
	}
}

func TestCreateLobby_RedirectsAndRegisters(t *testing.T) {
	e := newEnv(t, true)
	u := e.login(t, 1, "guso")
	resp := u.post(t, "/lobbies", url.Values{"name": {"LAN Night"}, "public": {"on"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/lobby/") {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	list := e.lobbies.ListPublic()
	if len(list) != 1 || list[0].Name != "LAN Night" || list[0].HostName != "guso" {
		t.Fatalf("%+v", list)
	}
	// private: not listed
	u2 := e.login(t, 2, "mads")
	u2.post(t, "/lobbies", url.Values{"name": {"secret"}})
	if len(e.lobbies.ListPublic()) != 1 {
		t.Fatal("private lobby listed")
	}
}

func TestCreateLobby_RequiresLogin(t *testing.T) {
	e := newEnv(t, true)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(e.ts.URL+"/lobbies", url.Values{"name": {"x"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 3 || len(e.lobbies.ListPublic()) != 0 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestLobbyPage_JoinsViaLinkAndShowsMembers(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "guso")
	resp := host.post(t, "/lobbies", url.Values{"name": {"Private"}})
	path := resp.Header.Get("Location")
	guest := e.login(t, 2, "mads")
	r, body := guest.get(t, path)
	if r.StatusCode != 200 || !strings.Contains(body, "guso") || !strings.Contains(body, "mads") {
		t.Fatalf("status %d body %s", r.StatusCode, body)
	}
	id := strings.TrimPrefix(path, "/lobby/")
	if got, _ := e.lobbies.LobbyOf(sid(2)); got != id {
		t.Fatal("guest was not added to the lobby")
	}
	r, _ = guest.get(t, "/lobby/doesnotexist")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown lobby status %d", r.StatusCode)
	}
}

func TestLobbyPage_EscapesDisplayName(t *testing.T) {
	e := newEnv(t, true)
	evil := `<script>alert(1)</script>`
	u := e.login(t, 1, evil)
	resp := u.post(t, "/lobbies", url.Values{"name": {`<img src=x onerror=alert(2)>`}, "public": {"on"}})
	r, body := u.get(t, resp.Header.Get("Location"))
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if strings.Contains(body, evil) || strings.Contains(body, "<img src=x") {
		t.Fatal("unescaped markup in lobby page")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("escaped name missing")
	}
	_, index := u.get(t, "/")
	_ = context.Background()
	if strings.Contains(index, "<img src=x") {
		t.Fatal("unescaped lobby name in browser page")
	}
}

func TestStaticFilesServed(t *testing.T) {
	e := newEnv(t, true)
	resp, err := http.Get(e.ts.URL + "/static/cs16.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
