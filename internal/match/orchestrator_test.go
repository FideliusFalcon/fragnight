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

	if r.rcon.count() != 3 {
		t.Fatalf("rcon calls: %+v", r.rcon.calls)
	}
	if r.rcon.calls[0].cmd != "kickall" {
		t.Fatalf("players of the previous match must be kicked first, got %+v", r.rcon.calls[0])
	}
	pwCall, loadCall := r.rcon.calls[1], r.rcon.calls[2]
	if pwCall.server != "s1" || !strings.HasPrefix(pwCall.cmd, "sv_password ") {
		t.Fatalf("first call %+v", pwCall)
	}
	password := strings.TrimPrefix(pwCall.cmd, "sv_password ")
	if len(password) < 16 {
		t.Fatalf("match password too short (%d chars): %q", len(password), password)
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
	if r.rcon.count() != 3 {
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
	if snapB.Status != lobby.StatusAwaitingServer || r.rcon.count() != 3 {
		t.Fatalf("B status %s, rcon calls %d", snapB.Status, r.rcon.count())
	}
	r.o.ReleaseServer("s1")
	snapB, _ = r.lobbies.Get(b)
	if snapB.Status != lobby.StatusInMatch || !strings.HasPrefix(snapB.Connect, "connect 10.0.0.5:27015; password ") || r.rcon.count() != 6 {
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

// abortWhile starts Abort in another goroutine the first time a command with the
// given prefix is sent, then lets the launch carry on. It returns a func that waits
// for the abort and reports what the match looked like while the launch was
// still in flight.
func abortWhile(r *rig, lobbyID, prefix string) (wait func() (statusDuringLaunch string)) {
	var (
		once   sync.Once
		done   = make(chan struct{})
		during = make(chan string, 1)
	)
	r.rcon.onExec = func(c call) {
		if !strings.HasPrefix(c.cmd, prefix) {
			return
		}
		once.Do(func() {
			go func() {
				defer close(done)
				if err := r.o.Abort(lobbyID, steam(1)); err != nil {
					panic(err)
				}
			}()
			time.Sleep(60 * time.Millisecond) // give the abort time to start and block on the lobby lock
			m, err := r.st.GetMatchOfLobby(context.Background(), lobbyID)
			if err != nil {
				during <- "error: " + err.Error()
				return
			}
			during <- m.Status
		})
	}
	return func() string {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			panic("abort never finished (deadlock?)")
		}
		return <-during
	}
}

func assertEndedCleanly(t *testing.T, r *rig, lobbyID string) {
	t.Helper()
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusWaiting || snap.Connect != "" {
		t.Fatalf("lobby after abort: status %s connect %q", snap.Status, snap.Connect)
	}
	if _, err := r.st.ActiveMatchOfLobby(context.Background(), lobbyID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("aborted match is still active: %v", err)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server is still held after the abort")
	}
}

func TestAbortDuringLaunch_WaitsForTheLaunchThenEndsTheMatch(t *testing.T) {
	for _, prefix := range []string{"sv_password", "matchzy_loadmatch_url"} {
		r := newRig(t, 1)
		id := r.lobbyWith(t, 1)
		wait := abortWhile(r, id, prefix)
		r.o.Start(id, "de_dust2")
		during := wait()
		if during == store.MatchAborted {
			t.Fatalf("[%s] the abort ran in the middle of the launch instead of waiting for it", prefix)
		}
		assertEndedCleanly(t, r, id)
		// the launch finished first, so the abort stopped a running match properly
		var sawEnd bool
		for _, c := range r.rcon.calls {
			sawEnd = sawEnd || c.cmd == "get5_endmatch"
		}
		if !sawEnd {
			t.Fatalf("[%s] running match was not ended on the server: %+v", prefix, r.rcon.calls)
		}
	}
}

func TestLaunch_NeverReleasesAServerHeldByAnotherLobby(t *testing.T) {
	r := newRig(t, 1)
	holder := r.lobbyWith(t, 1)
	srv, ok := r.pool.Acquire(holder)
	if !ok {
		t.Fatal("no server")
	}
	// a stale launch for a lobby that has no match and does not hold the server
	r.o.launch("ghost-lobby", srv)
	if st := r.pool.State(); !st[0].Busy || st[0].LobbyID != holder {
		t.Fatalf("someone else's server was released: %+v", st[0])
	}
	if r.rcon.count() != 0 {
		t.Fatalf("stale launch talked to the server: %+v", r.rcon.calls)
	}
}
