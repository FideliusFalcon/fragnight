package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
)

type wsClient struct {
	t *testing.T
	c *websocket.Conn
}

func (u *user) dial(t *testing.T, path string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(u.e.ts.URL, "http") + path
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: u.cookieHeader()})
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c}
}

// next reads messages until one of the wanted type arrives.
func (w *wsClient) next(want string) map[string]json.RawMessage {
	w.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, data, err := w.c.Read(ctx)
		cancel()
		if err != nil {
			w.t.Fatalf("waiting for %q: %v", want, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			w.t.Fatal(err)
		}
		var typ string
		json.Unmarshal(m["type"], &typ)
		if typ == want {
			return m
		}
	}
}

func (w *wsClient) send(v any) {
	w.t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.c.Write(ctx, websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

func (w *wsClient) stateUntil(cond func(lobby.Snapshot) bool) lobby.Snapshot {
	w.t.Helper()
	for i := 0; i < 20; i++ {
		m := w.next("state")
		var snap lobby.Snapshot
		json.Unmarshal(m["lobby"], &snap)
		if cond(snap) {
			return snap
		}
	}
	w.t.Fatal("condition never met")
	return lobby.Snapshot{}
}

func teamOf(s lobby.Snapshot, id string) lobby.Team {
	for _, m := range s.Members {
		if m.SteamID == id {
			return m.Team
		}
	}
	return -1
}

func TestBrowserWS_PushesPublicListOnChange(t *testing.T) {
	e := newEnv(t, true)
	viewer := e.login(t, 1, "viewer")
	host := e.login(t, 2, "host")
	w := viewer.dial(t, "/ws/browser")
	first := w.next("list")
	var lobbies []lobby.Summary
	json.Unmarshal(first["lobbies"], &lobbies)
	if len(lobbies) != 0 {
		t.Fatalf("initial list: %v", lobbies)
	}
	host.post(t, "/lobbies", url.Values{"name": {"Fresh"}, "public": {"on"}})
	for i := 0; i < 5; i++ {
		m := w.next("list")
		json.Unmarshal(m["lobbies"], &lobbies)
		if len(lobbies) == 1 {
			if lobbies[0].Name != "Fresh" {
				t.Fatalf("name %q", lobbies[0].Name)
			}
			return
		}
	}
	t.Fatal("new lobby never pushed")
}

func TestLobbyWS_TeamMoveBroadcastsToOthers(t *testing.T) {
	e := newEnv(t, true)
	a := e.login(t, 1, "a")
	b := e.login(t, 2, "b")
	resp := a.post(t, "/lobbies", url.Values{"name": {"L"}, "public": {"on"}})
	path := resp.Header.Get("Location")
	b.get(t, path) // joins via link
	id := strings.TrimPrefix(path, "/lobby/")

	wa := a.dial(t, "/ws/lobby/"+id)
	wb := b.dial(t, "/ws/lobby/"+id)
	wb.stateUntil(func(s lobby.Snapshot) bool { return len(s.Members) == 2 })
	wa.send(map[string]any{"type": "team", "team": 1})
	snap := wb.stateUntil(func(s lobby.Snapshot) bool { return teamOf(s, a.id) == lobby.Team1 })
	if snap.ID != id {
		t.Fatal("wrong lobby")
	}
}

func TestLobbyWS_ErrorForTeamFull(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "host")
	resp := host.post(t, "/lobbies", url.Values{"name": {"L"}})
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/lobby/")
	for i := 2; i <= 6; i++ {
		e.lobbies.Join(id, lobby.Player{SteamID: sid(i), Name: "p"})
	}
	for i := 2; i <= 6; i++ {
		e.lobbies.SetTeam(id, sid(i), lobby.Team1)
	}
	w := host.dial(t, "/ws/lobby/"+id)
	w.next("state")
	w.send(map[string]any{"type": "team", "team": 1})
	m := w.next("error")
	var msg string
	json.Unmarshal(m["message"], &msg)
	if !strings.Contains(strings.ToLower(msg), "full") {
		t.Fatalf("message %q", msg)
	}
}

func TestLobbyWS_VoteAndReadyFlow(t *testing.T) {
	e := newEnv(t, true)
	a := e.login(t, 1, "a")
	b := e.login(t, 2, "b")
	resp := a.post(t, "/lobbies", url.Values{"name": {"L"}})
	path := resp.Header.Get("Location")
	b.get(t, path)
	id := strings.TrimPrefix(path, "/lobby/")
	wa := a.dial(t, "/ws/lobby/"+id)
	wb := b.dial(t, "/ws/lobby/"+id)
	wa.send(map[string]any{"type": "team", "team": 1})
	wb.send(map[string]any{"type": "team", "team": 2})
	wa.stateUntil(func(s lobby.Snapshot) bool { return teamOf(s, a.id) == 1 && teamOf(s, b.id) == 2 })
	wa.send(map[string]any{"type": "ready"})
	wb.stateUntil(func(s lobby.Snapshot) bool { return s.Status == lobby.StatusVoting })
	wb.send(map[string]any{"type": "vote", "map": "de_nuke"})
	snap := wa.stateUntil(func(s lobby.Snapshot) bool { return s.Tally["de_nuke"] == 1 })
	if snap.VoteRemainingMS <= 0 || snap.VoteRemainingMS > 30000 {
		t.Fatalf("remaining %d", snap.VoteRemainingMS)
	}
	// a non-host cannot stop the countdown
	wb.send(map[string]any{"type": "stop"})
	wb.next("error")
}

func TestLobbyWS_UnauthenticatedRefused(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "host")
	resp := host.post(t, "/lobbies", url.Values{"name": {"L"}})
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/lobby/")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, path := range []string{"/ws/lobby/" + id, "/ws/browser"} {
		_, r, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+path, nil)
		if err == nil {
			t.Fatalf("%s: unauthenticated dial succeeded", path)
		}
		if r == nil || r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: response %v", path, r)
		}
	}
}

func TestLobbyWS_NonMemberRefused(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "host")
	outsider := e.login(t, 2, "outsider")
	resp := host.post(t, "/lobbies", url.Values{"name": {"L"}})
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/lobby/")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, r, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws/lobby/"+id,
		&websocket.DialOptions{HTTPHeader: outsider.cookieHeader()})
	if err == nil || r == nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("err %v resp %v", err, r)
	}
}

func TestLobbyWS_AbortCommand(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "host")
	resp := host.post(t, "/lobbies", url.Values{"name": {"L"}})
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/lobby/")
	w := host.dial(t, "/ws/lobby/"+id)
	w.next("state")
	w.send(map[string]any{"type": "abort"})
	w.send(map[string]any{"type": "team", "team": 1}) // round trip: the abort was handled first
	w.stateUntil(func(s lobby.Snapshot) bool { return teamOf(s, host.id) == lobby.Team1 })
	e.matches.mu.Lock()
	defer e.matches.mu.Unlock()
	if len(e.matches.aborts) != 1 || e.matches.aborts[0] != id+":"+host.id {
		t.Fatalf("aborts %v", e.matches.aborts)
	}
}

func TestLobbyWS_TracksPresence(t *testing.T) {
	e := newEnv(t, true)
	host := e.login(t, 1, "host")
	resp := host.post(t, "/lobbies", url.Values{"name": {"L"}})
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/lobby/")
	eventually := func(want bool) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if e.lobbies.IsOnline(id, host.id) == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("IsOnline never became %v", want)
	}
	if e.lobbies.IsOnline(id, host.id) {
		t.Fatal("online before any page opened")
	}
	w := host.dial(t, "/ws/lobby/"+id)
	w.next("state")
	eventually(true)
	w.c.Close(websocket.StatusNormalClosure, "")
	eventually(false)
}
