package lobby

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/settings"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type chosen struct{ lobby, mapName string }

// voteLobby builds a lobby with 2 players per team and returns the clock and a
// log of OnMapChosen calls.
func voteLobby(t *testing.T) (*Manager, *fakeClock, Snapshot, *[]chosen) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	m := NewManager(clk.Now, rand.New(rand.NewSource(7)))
	var log []chosen
	m.OnMapChosen = func(id, mp string) { log = append(log, chosen{id, mp}) }
	s := mustCreate(t, m, pl(1), true)
	for i := 2; i <= 4; i++ {
		if _, err := m.Join(s.ID, pl(i)); err != nil {
			t.Fatal(err)
		}
	}
	teams := []Team{Team1, Team1, Team2, Team2}
	for i, tm := range teams {
		if err := m.SetTeam(s.ID, pl(i+1).SteamID, tm); err != nil {
			t.Fatal(err)
		}
	}
	return m, clk, s, &log
}

func host() string { return pl(1).SteamID }

func TestReady_RequiresEqualNonEmptyTeams(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	m := NewManager(clk.Now, rand.New(rand.NewSource(1)))
	s := mustCreate(t, m, pl(1), true)
	if err := m.Ready(s.ID, host()); err != ErrBadTeams {
		t.Fatalf("empty teams: %v", err)
	}
	m.Join(s.ID, pl(2))
	m.SetTeam(s.ID, pl(1).SteamID, Team1)
	if err := m.Ready(s.ID, host()); err != ErrBadTeams {
		t.Fatalf("1v0: %v", err)
	}
	m.SetTeam(s.ID, pl(2).SteamID, Team2)
	if err := m.Ready(s.ID, host()); err != nil {
		t.Fatalf("1v1: %v", err)
	}
	got, _ := m.Get(s.ID)
	if got.Status != StatusVoting || got.VoteRemainingMS != 30000 {
		t.Fatalf("status %s remaining %d", got.Status, got.VoteRemainingMS)
	}
	if err := m.Ready(s.ID, host()); err != ErrBadState {
		t.Fatalf("double ready: %v", err)
	}
}

func TestReady_OnlyHost(t *testing.T) {
	m, _, s, _ := voteLobby(t)
	if err := m.Ready(s.ID, pl(2).SteamID); err != ErrNotHost {
		t.Fatalf("got %v", err)
	}
}

func TestVote_CountdownEndsAfter30Seconds(t *testing.T) {
	m, clk, s, log := voteLobby(t)
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(1).SteamID, "de_dust2")
	clk.Advance(29 * time.Second)
	m.Tick()
	if len(*log) != 0 {
		t.Fatal("finished early")
	}
	clk.Advance(time.Second)
	m.Tick()
	m.Tick() // must not fire twice
	if len(*log) != 1 || (*log)[0].lobby != s.ID || (*log)[0].mapName != "de_dust2" {
		t.Fatalf("log: %+v", *log)
	}
	got, _ := m.Get(s.ID)
	if got.Map != "de_dust2" || got.Status != StatusAwaitingServer {
		t.Fatalf("map %q status %s", got.Map, got.Status)
	}
}

func TestVote_ChangeVoteBeforeEnd(t *testing.T) {
	m, _, s, _ := voteLobby(t)
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(2).SteamID, "de_dust2")
	m.CastVote(s.ID, pl(2).SteamID, "de_nuke")
	got, _ := m.Get(s.ID)
	if got.Tally["de_dust2"] != 0 || got.Tally["de_nuke"] != 1 {
		t.Fatalf("tally %v", got.Tally)
	}
}

func TestVote_WinnerIsHighestTally(t *testing.T) {
	m, clk, s, log := voteLobby(t)
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(1).SteamID, "de_mirage")
	m.CastVote(s.ID, pl(2).SteamID, "de_inferno")
	m.CastVote(s.ID, pl(3).SteamID, "de_mirage")
	clk.Advance(30 * time.Second)
	m.Tick()
	if (*log)[0].mapName != "de_mirage" {
		t.Fatalf("got %s", (*log)[0].mapName)
	}
}

func TestVote_TieIsRandomAmongTied(t *testing.T) {
	seen := map[string]bool{}
	for seed := int64(0); seed < 40; seed++ {
		clk := &fakeClock{t: time.Unix(1, 0)}
		m := NewManager(clk.Now, rand.New(rand.NewSource(seed)))
		var got string
		m.OnMapChosen = func(_, mp string) { got = mp }
		s := mustCreate(t, m, pl(1), true)
		m.Join(s.ID, pl(2))
		m.SetTeam(s.ID, pl(1).SteamID, Team1)
		m.SetTeam(s.ID, pl(2).SteamID, Team2)
		m.Ready(s.ID, host())
		m.CastVote(s.ID, pl(1).SteamID, "de_dust2")
		m.CastVote(s.ID, pl(2).SteamID, "de_nuke")
		clk.Advance(30 * time.Second)
		m.Tick()
		if got != "de_dust2" && got != "de_nuke" {
			t.Fatalf("seed %d picked untied map %q", seed, got)
		}
		seen[got] = true
	}
	if len(seen) != 2 {
		t.Fatalf("tie never varied: %v", seen)
	}
}

func TestVote_NoVotesPicksFromPool(t *testing.T) {
	m, clk, s, log := voteLobby(t)
	m.Ready(s.ID, host())
	clk.Advance(30 * time.Second)
	m.Tick()
	if len(*log) != 1 {
		t.Fatal("no map chosen")
	}
	found := false
	for _, mp := range settings.DefaultMapPool {
		found = found || mp == (*log)[0].mapName
	}
	if !found {
		t.Fatalf("map %q not in pool", (*log)[0].mapName)
	}
}

func TestVote_Rules(t *testing.T) {
	m, _, s, _ := voteLobby(t)
	if err := m.CastVote(s.ID, pl(1).SteamID, "de_dust2"); err != ErrBadState {
		t.Fatalf("vote before ready: %v", err)
	}
	m.Join(s.ID, pl(5)) // unassigned
	m.Ready(s.ID, host())
	if err := m.CastVote(s.ID, pl(5).SteamID, "de_dust2"); err != ErrNotVoter {
		t.Fatalf("unassigned: %v", err)
	}
	if err := m.CastVote(s.ID, pl(1).SteamID, "de_notamap"); err != ErrBadMap {
		t.Fatalf("bad map: %v", err)
	}
	if err := m.CastVote(s.ID, pl(9).SteamID, "de_dust2"); err != ErrNotFound {
		t.Fatalf("non-member: %v", err)
	}
}

func TestStopVote_ThenReadyResetsTo30s(t *testing.T) {
	m, clk, s, log := voteLobby(t)
	m.Ready(s.ID, host())
	clk.Advance(20 * time.Second)
	if err := m.StopVote(s.ID, pl(2).SteamID); err != ErrNotHost {
		t.Fatalf("non-host stop: %v", err)
	}
	if err := m.StopVote(s.ID, host()); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(s.ID)
	if got.Status != StatusWaiting || got.VoteRemainingMS != 0 {
		t.Fatalf("after stop: %s %d", got.Status, got.VoteRemainingMS)
	}
	clk.Advance(time.Minute)
	m.Tick()
	if len(*log) != 0 {
		t.Fatal("stopped vote finished")
	}
	m.Ready(s.ID, host())
	got, _ = m.Get(s.ID)
	if got.VoteRemainingMS != 30000 {
		t.Fatalf("restart remaining %d", got.VoteRemainingMS)
	}
}

func TestStopVote_KeepsVotes(t *testing.T) {
	m, _, s, _ := voteLobby(t)
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(1).SteamID, "de_nuke")
	m.StopVote(s.ID, host())
	got, _ := m.Get(s.ID)
	if got.Tally["de_nuke"] != 1 {
		t.Fatalf("votes lost: %v", got.Tally)
	}
}

func TestHostLeavesDuringVote_VoteContinues(t *testing.T) {
	m, clk, s, log := voteLobby(t)
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(2).SteamID, "de_anubis")
	m.Leave(s.ID, host())
	got, _ := m.Get(s.ID)
	if got.HostSteamID != pl(2).SteamID || got.Status != StatusVoting {
		t.Fatalf("host %s status %s", got.HostSteamID, got.Status)
	}
	clk.Advance(30 * time.Second)
	m.Tick()
	if len(*log) != 1 || (*log)[0].mapName != "de_anubis" {
		t.Fatalf("log %+v", *log)
	}
}
