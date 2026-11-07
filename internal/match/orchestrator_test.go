package match

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/pool"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

type call struct{ server, cmd string }

type fakeRCON struct {
	mu      sync.Mutex
	calls   []call
	failFor map[string]bool
	// onExec, if set, runs after a command is recorded (without the lock held),
	// letting a test act "while the command is in flight".
	onExec func(call)
}

func (f *fakeRCON) Exec(_ context.Context, s pool.Server, cmd string) (string, error) {
	f.mu.Lock()
	c := call{s.ID, cmd}
	f.calls = append(f.calls, c)
	fail := f.failFor[s.ID]
	hook := f.onExec
	f.mu.Unlock()
	if hook != nil {
		hook(c)
	}
	if fail {
		return "", errors.New("connection refused")
	}
	return "ok", nil
}

func (f *fakeRCON) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

type rig struct {
	o       *Orchestrator
	lobbies *lobby.Manager
	st      *store.Store
	pool    *pool.Pool
	rcon    *fakeRCON
}

func newRig(t *testing.T, nServers int) *rig {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var servers []pool.Server
	for i := 1; i <= nServers; i++ {
		servers = append(servers, pool.Server{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("Server %d", i), Host: "10.0.0.5", Port: 27014 + i})
	}
	r := &rig{
		lobbies: lobby.NewManager(time.Now, rand.New(rand.NewSource(1))),
		st:      st,
		pool:    pool.New(servers),
		rcon:    &fakeRCON{failFor: map[string]bool{}},
	}
	r.o = New(Deps{Lobbies: r.lobbies, Pool: r.pool, RCON: r.rcon, Store: st, BaseURL: "http://lan.local", WebhookSecret: "sekret"})
	return r
}

func steam(n int) string { return fmt.Sprintf("7656119800000%04d", n) }

// lobbyWith creates a 2v2 lobby using players first..first+3.
func (r *rig) lobbyWith(t *testing.T, first int) string {
	t.Helper()
	ctx := context.Background()
	var id string
	for i := 0; i < 4; i++ {
		p := lobby.Player{SteamID: steam(first + i), Name: fmt.Sprintf("p%d", first+i)}
		if err := r.st.UpsertPlayer(ctx, store.Player{SteamID: p.SteamID, Name: p.Name}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			snap, err := r.lobbies.Create(p, fmt.Sprintf("lobby %d", first), true)
			if err != nil {
				t.Fatal(err)
			}
			id = snap.ID
		} else if _, err := r.lobbies.Join(id, p); err != nil {
			t.Fatal(err)
		}
		team := lobby.Team1
		if i >= 2 {
			team = lobby.Team2
		}
		if err := r.lobbies.SetTeam(id, p.SteamID, team); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestStart_LoadsMatchAndSetsConnectString(t *testing.T) {
	r := newRig(t, 1)
	id := r.lobbyWith(t, 1)
	r.o.Start(id, "de_dust2")

	if r.rcon.count() != 2 {
		t.Fatalf("rcon calls: %+v", r.rcon.calls)
	}
	pwCall, loadCall := r.rcon.calls[0], r.rcon.calls[1]
	if pwCall.server != "s1" || !strings.HasPrefix(pwCall.cmd, "sv_password ") {
		t.Fatalf("first call %+v", pwCall)
	}
	password := strings.TrimPrefix(pwCall.cmd, "sv_password ")
	if len(password) < 6 {
		t.Fatalf("password too short: %q", password)
	}
	if !strings.HasPrefix(loadCall.cmd, `matchzy_loadmatch_url "http://lan.local/internal/match/`) {
		t.Fatalf("second call %+v", loadCall)
	}

	snap, _ := r.lobbies.Get(id)
	if snap.Status != lobby.StatusInMatch {
		t.Fatalf("status %s", snap.Status)
	}
	if want := "connect 10.0.0.5:27015; password " + password; snap.Connect != want {
		t.Fatalf("connect %q, want %q", snap.Connect, want)
	}

	m, err := r.st.ActiveMatchOfLobby(context.Background(), id)
	if err != nil || m.Status != store.MatchRunning || m.ServerID != "s1" || m.Map != "de_dust2" {
		t.Fatalf("match %+v %v", m, err)
	}
	if snap.MatchID != m.ID {
		t.Fatalf("lobby match id %q, want %q", snap.MatchID, m.ID)
	}
	if !strings.Contains(m.ConfigJSON, steam(1)) || !strings.Contains(m.ConfigJSON, "de_dust2") {
		t.Fatalf("config %s", m.ConfigJSON)
	}
	// the URL MatchZy was told to fetch carries this match's id and token
	raw := strings.TrimSuffix(strings.TrimPrefix(loadCall.cmd, `matchzy_loadmatch_url "`), `"`)
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/internal/match/"+m.ID+"/config.json" || u.Query().Get("token") != m.Token || m.Token == "" {
		t.Fatalf("url %q token %q", raw, m.Token)
	}
	roster, _ := r.st.Roster(context.Background(), m.ID)
	if len(roster) != 4 {
		t.Fatalf("roster %+v", roster)
	}
}

func TestStart_Twice_OnlyOneMatch(t *testing.T) {
	r := newRig(t, 2)
	id := r.lobbyWith(t, 1)
	r.o.Start(id, "de_dust2")
	r.o.Start(id, "de_dust2")
	if r.rcon.count() != 2 {
		t.Fatalf("second Start launched again: %+v", r.rcon.calls)
	}
}

func TestStart_WhenPoolFull_ShowsAwaitingServerThenStartsOnRelease(t *testing.T) {
	r := newRig(t, 1)
	a := r.lobbyWith(t, 1)
	b := r.lobbyWith(t, 10)
	r.o.Start(a, "de_dust2")
	r.o.Start(b, "de_nuke")

	snapB, _ := r.lobbies.Get(b)
	if snapB.Status != lobby.StatusAwaitingServer || r.rcon.count() != 2 {
		t.Fatalf("B status %s, rcon calls %d", snapB.Status, r.rcon.count())
	}
	r.o.ReleaseServer("s1")
	snapB, _ = r.lobbies.Get(b)
	if snapB.Status != lobby.StatusInMatch || !strings.HasPrefix(snapB.Connect, "connect 10.0.0.5:27015; password ") || r.rcon.count() != 4 {
		t.Fatalf("B after release: status %s connect %q calls %d", snapB.Status, snapB.Connect, r.rcon.count())
	}
}

func TestStart_RCONFailure_ReleasesServerAndReturnsToWaiting(t *testing.T) {
	r := newRig(t, 1)
	id := r.lobbyWith(t, 1)
	r.rcon.failFor["s1"] = true
	r.o.Start(id, "de_dust2")

	snap, _ := r.lobbies.Get(id)
	if snap.Status != lobby.StatusWaiting || !strings.Contains(snap.Error, "Server 1") {
		t.Fatalf("status %s error %q", snap.Status, snap.Error)
	}
	if snap.Connect != "" {
		t.Fatalf("connect string set on failure: %q", snap.Connect)
	}
	if _, ok := r.pool.Acquire("someone-else"); !ok {
		t.Fatal("server was not returned to the pool")
	}
	if _, err := r.st.ActiveMatchOfLobby(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed match still active: %v", err)
	}
}

func TestLobbyGone_ForgetsQueuedLobby(t *testing.T) {
	r := newRig(t, 1)
	a := r.lobbyWith(t, 1)
	b := r.lobbyWith(t, 10)
	r.o.Start(a, "de_dust2")
	r.o.Start(b, "de_dust2")
	r.o.LobbyGone(b)
	before := r.rcon.count()
	r.o.ReleaseServer("s1")
	if r.rcon.count() != before {
		t.Fatal("a gone lobby was launched")
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server should be free")
	}
	if _, err := r.st.ActiveMatchOfLobby(context.Background(), b); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pending match of gone lobby still active: %v", err)
	}
}

// abortOn makes the host abort the lobby the first time a command with the given prefix is sent.
func abortOn(r *rig, lobbyID, prefix string) {
	done := false
	r.rcon.onExec = func(c call) {
		if !done && strings.HasPrefix(c.cmd, prefix) {
			done = true
			if err := r.o.Abort(lobbyID, steam(1)); err != nil {
				panic(err)
			}
		}
	}
}

func assertAbortedNotRevived(t *testing.T, r *rig, lobbyID string) {
	t.Helper()
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusWaiting || snap.Connect != "" {
		t.Fatalf("aborted lobby was revived: status %s connect %q", snap.Status, snap.Connect)
	}
	if _, err := r.st.ActiveMatchOfLobby(context.Background(), lobbyID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("aborted match is active again: %v", err)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server is still held after the abort")
	}
}

func TestAbortDuringLaunch_BeforeLoad_DoesNotLoadTheMatch(t *testing.T) {
	r := newRig(t, 1)
	id := r.lobbyWith(t, 1)
	abortOn(r, id, "sv_password")
	r.o.Start(id, "de_dust2")
	for _, c := range r.rcon.calls {
		if strings.HasPrefix(c.cmd, "matchzy_loadmatch_url") {
			t.Fatalf("aborted match was loaded on the server: %+v", r.rcon.calls)
		}
	}
	assertAbortedNotRevived(t, r, id)
}

func TestAbortDuringLaunch_AfterLoad_DoesNotRunTheMatch(t *testing.T) {
	r := newRig(t, 1)
	id := r.lobbyWith(t, 1)
	abortOn(r, id, "matchzy_loadmatch_url")
	r.o.Start(id, "de_dust2")
	assertAbortedNotRevived(t, r, id)
}
