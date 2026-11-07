package match

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

// runningMatch starts a 2v2 lobby (players first..first+3, team 1 = first,first+1) and returns its lobby and match IDs.
func (r *rig) runningMatch(t *testing.T, first int) (lobbyID, matchID string) {
	t.Helper()
	lobbyID = r.lobbyWith(t, first)
	r.o.Start(lobbyID, "de_dust2")
	m, err := r.st.ActiveMatchOfLobby(context.Background(), lobbyID)
	if err != nil || m.Status != store.MatchRunning {
		t.Fatalf("match did not start: %+v %v", m, err)
	}
	return lobbyID, m.ID
}

func event(t *testing.T, name, matchID string) matchzy.Event {
	t.Helper()
	b, err := os.ReadFile("../matchzy/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := matchzy.ParseEvent([]byte(strings.ReplaceAll(string(b), "m-test-1", matchID)))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func ratingOf(t *testing.T, r *rig, n int) int {
	t.Helper()
	p, err := r.st.GetPlayer(context.Background(), steam(n))
	if err != nil {
		t.Fatal(err)
	}
	return p.Elo
}

func TestHandleEvent_MapResultAppliesStatsAndElo(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	if err := r.o.HandleEvent("s1", event(t, "map_result", matchID)); err != nil {
		t.Fatal(err)
	}
	// team 1 won 13-7 between equal ratings: +16 / -16
	if ratingOf(t, r, 1) != 1016 || ratingOf(t, r, 2) != 1016 || ratingOf(t, r, 3) != 984 || ratingOf(t, r, 4) != 984 {
		t.Fatalf("elo: %d %d %d %d", ratingOf(t, r, 1), ratingOf(t, r, 2), ratingOf(t, r, 3), ratingOf(t, r, 4))
	}
	m, _ := r.st.GetMatch(context.Background(), matchID)
	if m.Status != store.MatchFinished || m.Team1Score != 13 || m.Team2Score != 7 || m.Winner != 1 {
		t.Fatalf("%+v", m)
	}
	roster, _ := r.st.Roster(context.Background(), matchID)
	first := roster[0]
	if first.SteamID != steam(1) || first.Kills != 20 || first.Deaths != 12 || first.Damage != 2300 ||
		first.Headshots != 9 || first.EloBefore != 1000 || first.EloAfter != 1016 {
		t.Fatalf("%+v", first)
	}
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusFinished || snap.Score != [2]int{13, 7} {
		t.Fatalf("lobby %s %v", snap.Status, snap.Score)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server not released")
	}
	last := r.rcon.calls[len(r.rcon.calls)-1]
	if last.cmd != `sv_password ""` {
		t.Fatalf("server not reset, last rcon call %+v", last)
	}
}

func TestHandleEvent_DuplicateMapResultIgnored(t *testing.T) {
	r := newRig(t, 1)
	_, matchID := r.runningMatch(t, 1)
	ev := event(t, "map_result", matchID)
	if err := r.o.HandleEvent("s1", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.o.HandleEvent("s1", ev); err != nil {
		t.Fatalf("duplicate returned error: %v", err)
	}
	if ratingOf(t, r, 1) != 1016 || ratingOf(t, r, 3) != 984 {
		t.Fatalf("elo applied twice: %d %d", ratingOf(t, r, 1), ratingOf(t, r, 3))
	}
}

func TestHandleEvent_UnknownServerAndStaleMatchIgnored(t *testing.T) {
	r := newRig(t, 1)
	_, matchID := r.runningMatch(t, 1)
	if err := r.o.HandleEvent("nope", event(t, "map_result", matchID)); err != nil {
		t.Fatal(err)
	}
	if err := r.o.HandleEvent("s1", event(t, "map_result", "some-older-match")); err != nil {
		t.Fatal(err)
	}
	m, _ := r.st.GetMatch(context.Background(), matchID)
	if m.Status != store.MatchRunning || ratingOf(t, r, 1) != 1000 {
		t.Fatalf("ignored event changed state: %+v", m)
	}
}

func TestHandleEvent_RoundEndUpdatesScore(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	if err := r.o.HandleEvent("s1", event(t, "round_end", matchID)); err != nil {
		t.Fatal(err)
	}
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Score != [2]int{3, 2} || snap.Status != lobby.StatusInMatch {
		t.Fatalf("score %v status %s", snap.Score, snap.Status)
	}
}

func TestHandleEvent_ReleasesServerToWaitingLobby(t *testing.T) {
	r := newRig(t, 1)
	_, matchA := r.runningMatch(t, 1)
	b := r.lobbyWith(t, 10)
	r.o.Start(b, "de_nuke")
	if snap, _ := r.lobbies.Get(b); snap.Status != lobby.StatusAwaitingServer {
		t.Fatalf("B status %s", snap.Status)
	}
	if err := r.o.HandleEvent("s1", event(t, "map_result", matchA)); err != nil {
		t.Fatal(err)
	}
	snap, _ := r.lobbies.Get(b)
	if snap.Status != lobby.StatusInMatch || snap.Connect == "" {
		t.Fatalf("B after A finished: %s %q", snap.Status, snap.Connect)
	}
}

func TestAbort_NoStatsNoElo(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	if err := r.o.Abort(lobbyID, steam(1)); err != nil {
		t.Fatal(err)
	}
	m, _ := r.st.GetMatch(context.Background(), matchID)
	if m.Status != store.MatchAborted {
		t.Fatalf("status %s", m.Status)
	}
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusWaiting || snap.Connect != "" || !strings.Contains(snap.Error, "aborted") {
		t.Fatalf("lobby %s connect %q error %q", snap.Status, snap.Connect, snap.Error)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server not released")
	}
	// a late result for the aborted match must not count
	if err := r.o.HandleEvent("s1", event(t, "map_result", matchID)); err != nil {
		t.Fatal(err)
	}
	if ratingOf(t, r, 1) != 1000 || ratingOf(t, r, 3) != 1000 {
		t.Fatal("aborted match changed ratings")
	}
}

func TestAbort_OnlyHostOrAdmin(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	if err := r.o.Abort(lobbyID, steam(2)); !errors.Is(err, lobby.ErrNotHost) {
		t.Fatalf("non-host: %v", err)
	}
	if m, _ := r.st.GetMatch(context.Background(), matchID); m.Status != store.MatchRunning {
		t.Fatal("non-host abort took effect")
	}
	r.o.Admins = []string{steam(99)}
	if err := r.o.Abort(lobbyID, steam(99)); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if m, _ := r.st.GetMatch(context.Background(), matchID); m.Status != store.MatchAborted {
		t.Fatalf("status %s", m.Status)
	}
}

func TestAbort_NoMatch(t *testing.T) {
	r := newRig(t, 1)
	id := r.lobbyWith(t, 1)
	if err := r.o.Abort(id, steam(1)); !errors.Is(err, lobby.ErrBadState) {
		t.Fatalf("got %v", err)
	}
}

func TestLobbyGone_AbortsRunningMatch(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	r.o.LobbyGone(lobbyID)
	if m, _ := r.st.GetMatch(context.Background(), matchID); m.Status != store.MatchAborted {
		t.Fatalf("status %s", m.Status)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server still held by a deleted lobby")
	}
}

func TestResetServer_AbortsRunningMatchAndReleases(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, matchID := r.runningMatch(t, 1)
	if err := r.o.ResetServer("s1"); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.st.GetMatch(context.Background(), matchID); m.Status != store.MatchAborted {
		t.Fatalf("status %s", m.Status)
	}
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusWaiting || snap.Connect != "" {
		t.Fatalf("lobby %s %q", snap.Status, snap.Connect)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("server still busy")
	}
}

func TestResetServer_FreesLeakedServer(t *testing.T) {
	r := newRig(t, 1)
	r.pool.Acquire("ghost-lobby") // held, but no match is running on it
	if err := r.o.ResetServer("s1"); err != nil {
		t.Fatal(err)
	}
	if st := r.pool.State(); st[0].Busy {
		t.Fatal("leaked server not freed")
	}
	if err := r.o.ResetServer("nope"); err == nil {
		t.Fatal("unknown server accepted")
	}
}

func TestPoolState_ListsServers(t *testing.T) {
	r := newRig(t, 2)
	if got := r.o.PoolState(); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestRematch_SameLobbyCanPlayASecondMatch(t *testing.T) {
	r := newRig(t, 1)
	lobbyID, firstID := r.runningMatch(t, 1)
	if err := r.o.HandleEvent("s1", event(t, "map_result", firstID)); err != nil {
		t.Fatal(err)
	}
	firstConnect := ""
	if snap, _ := r.lobbies.Get(lobbyID); snap.Status != lobby.StatusFinished {
		t.Fatalf("status %s", snap.Status)
	} else {
		firstConnect = snap.Connect
	}
	if err := r.lobbies.Rematch(lobbyID, steam(1)); err != nil {
		t.Fatal(err)
	}
	r.o.Start(lobbyID, "de_nuke")

	m, err := r.st.ActiveMatchOfLobby(context.Background(), lobbyID)
	if err != nil || m.ID == firstID || m.Status != store.MatchRunning || m.Map != "de_nuke" {
		t.Fatalf("second match: %+v %v", m, err)
	}
	snap, _ := r.lobbies.Get(lobbyID)
	if snap.Status != lobby.StatusInMatch || snap.MatchID != m.ID || snap.Connect == "" || snap.Connect == firstConnect {
		t.Fatalf("lobby after second start: %s match %q connect %q (first %q)", snap.Status, snap.MatchID, snap.Connect, firstConnect)
	}
	if snap.Score != [2]int{0, 0} {
		t.Fatalf("old score still shown: %v", snap.Score)
	}
	// the first match's result is untouched
	old, _ := r.st.GetMatch(context.Background(), firstID)
	if old.Status != store.MatchFinished || old.Team1Score != 13 {
		t.Fatalf("first match changed: %+v", old)
	}
}
