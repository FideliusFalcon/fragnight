package lobby

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/settings"
)

func pl(n int) Player {
	return Player{SteamID: fmt.Sprintf("7656119800000%04d", n), Name: fmt.Sprintf("p%d", n)}
}

func newMgr() *Manager {
	return NewManager(time.Now, rand.New(rand.NewSource(1)))
}

func mustCreate(t *testing.T, m *Manager, host Player, public bool) Snapshot {
	t.Helper()
	s, err := m.Create(host, "test lobby", public)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func teamOf(s Snapshot, id string) (Team, bool) {
	for _, mem := range s.Members {
		if mem.SteamID == id {
			return mem.Team, true
		}
	}
	return TeamNone, false
}

func TestCreate_HostIsUnassignedWithDefaults(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	if s.HostSteamID != pl(1).SteamID || s.Status != StatusWaiting || len(s.ID) != 32 {
		t.Fatalf("%+v", s)
	}
	if tm, ok := teamOf(s, pl(1).SteamID); !ok || tm != TeamNone {
		t.Fatal("host should be a member, unassigned")
	}
	if len(s.Settings.Maps) == 0 || s.Settings.MR != 12 {
		t.Fatalf("settings: %+v", s.Settings)
	}
}

func TestJoin_PutsPlayerUnassigned(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	s, err := m.Join(s.ID, pl(2))
	if err != nil {
		t.Fatal(err)
	}
	if tm, ok := teamOf(s, pl(2).SteamID); !ok || tm != TeamNone {
		t.Fatalf("%+v", s.Members)
	}
}

func TestJoin_RejectsPlayerInAnotherLobby(t *testing.T) {
	m := newMgr()
	a := mustCreate(t, m, pl(1), true)
	b := mustCreate(t, m, pl(2), true)
	if _, err := m.Join(a.ID, pl(2)); err != ErrAlreadyInLobby {
		t.Fatalf("want ErrAlreadyInLobby, got %v", err)
	}
	if _, err := m.Create(pl(1), "again", true); err != ErrAlreadyInLobby {
		t.Fatalf("create: want ErrAlreadyInLobby, got %v", err)
	}
	_ = b
}

func TestJoin_SameLobbyTwiceIsIdempotent(t *testing.T) {
	m := newMgr()
	a := mustCreate(t, m, pl(1), true)
	if _, err := m.Join(a.ID, pl(1)); err != nil {
		t.Fatalf("rejoin own lobby: %v", err)
	}
	s, _ := m.Get(a.ID)
	if len(s.Members) != 1 {
		t.Fatalf("duplicate member: %+v", s.Members)
	}
}

func TestJoin_UnknownLobby(t *testing.T) {
	if _, err := newMgr().Join("nope", pl(1)); err != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestSetTeam_MoveBetweenTeamsAndLeave(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	id := pl(1).SteamID
	for _, want := range []Team{Team1, Team2, TeamNone} {
		if err := m.SetTeam(s.ID, id, want); err != nil {
			t.Fatal(err)
		}
		got, _ := m.Get(s.ID)
		if tm, _ := teamOf(got, id); tm != want {
			t.Fatalf("team %v, want %v", tm, want)
		}
	}
	if err := m.SetTeam(s.ID, id, Team(7)); err == nil {
		t.Fatal("invalid team accepted")
	}
	if err := m.SetTeam(s.ID, pl(9).SteamID, Team1); err != ErrNotFound {
		t.Fatalf("non-member: %v", err)
	}
}

func TestSetTeam_FullTeam(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	for i := 2; i <= 6; i++ {
		m.Join(s.ID, pl(i))
	}
	for i := 1; i <= 5; i++ {
		if err := m.SetTeam(s.ID, pl(i).SteamID, Team1); err != nil {
			t.Fatalf("p%d: %v", i, err)
		}
	}
	if err := m.SetTeam(s.ID, pl(6).SteamID, Team1); err != ErrTeamFull {
		t.Fatalf("want ErrTeamFull, got %v", err)
	}
	if err := m.SetTeam(s.ID, pl(6).SteamID, Team2); err != nil {
		t.Fatal(err)
	}
	// A player already on the full team can "move" to the same team without error.
	if err := m.SetTeam(s.ID, pl(1).SteamID, Team1); err != nil {
		t.Fatalf("same team: %v", err)
	}
}

func TestSetTeam_LastSlotRace(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	for i := 2; i <= 24; i++ {
		m.Join(s.ID, pl(i))
	}
	for i := 1; i <= 4; i++ {
		if err := m.SetTeam(s.ID, pl(i).SteamID, Team1); err != nil {
			t.Fatal(err)
		}
	}
	var ok, full int32
	var wg sync.WaitGroup
	for i := 5; i <= 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch err := m.SetTeam(s.ID, pl(i).SteamID, Team1); err {
			case nil:
				atomic.AddInt32(&ok, 1)
			case ErrTeamFull:
				atomic.AddInt32(&full, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 || full != 19 {
		t.Fatalf("ok=%d full=%d, want 1 and 19", ok, full)
	}
	got, _ := m.Get(s.ID)
	n := 0
	for _, mem := range got.Members {
		if mem.Team == Team1 {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("team1 has %d players", n)
	}
}

func TestKick_OnlyHost(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Join(s.ID, pl(2))
	m.Join(s.ID, pl(3))
	if err := m.Kick(s.ID, pl(2).SteamID, pl(3).SteamID); err != ErrNotHost {
		t.Fatalf("want ErrNotHost, got %v", err)
	}
	if err := m.Kick(s.ID, pl(1).SteamID, pl(3).SteamID); err != nil {
		t.Fatal(err)
	}
	if _, in := m.LobbyOf(pl(3).SteamID); in {
		t.Fatal("kicked player still in a lobby")
	}
	// kicked player is free to join another lobby
	other := mustCreate(t, m, pl(3), true)
	if other.ID == s.ID {
		t.Fatal("same lobby")
	}
}

func TestHostLeaves_OldestBecomesHost(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Join(s.ID, pl(2))
	m.Join(s.ID, pl(3))
	if err := m.Leave(s.ID, pl(1).SteamID); err != nil {
		t.Fatal(err)
	}
	got, ok := m.Get(s.ID)
	if !ok || got.HostSteamID != pl(2).SteamID {
		t.Fatalf("host: %q", got.HostSteamID)
	}
	if _, in := m.LobbyOf(pl(1).SteamID); in {
		t.Fatal("left player still mapped")
	}
}

func TestLastLeave_DeletesLobbyAndCallsOnEmpty(t *testing.T) {
	m := newMgr()
	var gone string
	m.OnEmpty = func(id string) { gone = id }
	s := mustCreate(t, m, pl(1), true)
	if err := m.Leave(s.ID, pl(1).SteamID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get(s.ID); ok {
		t.Fatal("lobby still exists")
	}
	if gone != s.ID {
		t.Fatalf("OnEmpty got %q", gone)
	}
	if len(m.ListPublic()) != 0 {
		t.Fatal("deleted lobby still listed")
	}
}

func TestListPublic_HidesPrivate(t *testing.T) {
	m := newMgr()
	mustCreate(t, m, pl(1), true)
	mustCreate(t, m, pl(2), false)
	list := m.ListPublic()
	if len(list) != 1 || list[0].HostName != "p1" || list[0].Players != 1 || list[0].Capacity != 10 {
		t.Fatalf("%+v", list)
	}
}

func TestSetSettings_OnlyHostAndOnlyWhenWaiting(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Join(s.ID, pl(2))
	ns := settings.Defaults()
	ns.MR = 15
	if err := m.SetSettings(s.ID, pl(2).SteamID, ns); err != ErrNotHost {
		t.Fatalf("want ErrNotHost, got %v", err)
	}
	bad := settings.Defaults()
	bad.Maps = nil
	if err := m.SetSettings(s.ID, pl(1).SteamID, bad); err == nil {
		t.Fatal("invalid settings accepted")
	}
	if err := m.SetSettings(s.ID, pl(1).SteamID, ns); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(s.ID)
	if got.Settings.MR != 15 {
		t.Fatalf("MR %d", got.Settings.MR)
	}
	m.SetStatus(s.ID, StatusInMatch, "")
	if err := m.SetSettings(s.ID, pl(1).SteamID, ns); err != ErrBadState {
		t.Fatalf("want ErrBadState, got %v", err)
	}
}

func TestSetTeam_OnlyWhenWaiting(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	m.SetStatus(s.ID, StatusInMatch, "")
	if err := m.SetTeam(s.ID, pl(1).SteamID, Team1); err != ErrBadState {
		t.Fatalf("want ErrBadState, got %v", err)
	}
}

func TestSubscribe_ReceivesInitialAndChanges(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	ch, cancel := m.Subscribe(s.ID)
	defer cancel()
	first := <-ch
	if first.ID != s.ID {
		t.Fatal("no initial snapshot")
	}
	m.SetTeam(s.ID, pl(1).SteamID, Team1)
	select {
	case got := <-ch:
		if tm, _ := teamOf(got, pl(1).SteamID); tm != Team1 {
			t.Fatalf("snapshot team %v", tm)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot after change")
	}
}

func TestSubscribeList_ReceivesPublicChanges(t *testing.T) {
	m := newMgr()
	ch, cancel := m.SubscribeList()
	defer cancel()
	if got := <-ch; len(got) != 0 {
		t.Fatalf("initial: %v", got)
	}
	mustCreate(t, m, pl(1), true)
	select {
	case got := <-ch:
		if len(got) != 1 {
			t.Fatalf("list: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no list update")
	}
}

func TestSlowSubscriberNeverBlocksManager(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	_, cancel := m.Subscribe(s.ID) // never read from
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			m.SetTeam(s.ID, pl(1).SteamID, Team(i%3))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manager blocked on slow subscriber")
	}
}

func TestSetMatchID_AppearsInSnapshot(t *testing.T) {
	m := newMgr()
	s := mustCreate(t, m, pl(1), true)
	m.SetMatchID(s.ID, "match-123")
	got, _ := m.Get(s.ID)
	if got.MatchID != "match-123" {
		t.Fatalf("match id %q", got.MatchID)
	}
	m.SetMatchID("nope", "x") // unknown lobby is harmless
}
