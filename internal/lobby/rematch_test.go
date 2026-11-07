package lobby

import (
	"testing"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/settings"
)

// tm returns a member's team.
func tm(s Snapshot, id string) Team {
	t, _ := teamOf(s, id)
	return t
}

// finishedLobby is a 2v2 lobby (p1,p2 on team 1; p3,p4 on team 2) whose match has finished.
func finishedLobby(t *testing.T) (*Manager, *fakeClock, Snapshot, *[]chosen) {
	t.Helper()
	m, clk, s, log := voteLobby(t)
	ns := settings.Defaults()
	ns.MR = 15
	if err := m.SetSettings(s.ID, host(), ns); err != nil {
		t.Fatal(err)
	}
	m.Ready(s.ID, host())
	m.CastVote(s.ID, pl(1).SteamID, "de_nuke")
	clk.Advance(30 * time.Second)
	m.Tick()
	m.SetMatchID(s.ID, "match-1")
	m.SetLive(s.ID, "connect 10.0.0.5:27015; password abc")
	m.SetStatus(s.ID, StatusInMatch, "")
	m.SetScore(s.ID, 13, 7)
	m.SetStatus(s.ID, StatusFinished, "")
	return m, clk, s, log
}

func TestRematch_ResetsMatchStateKeepsPlayersTeamsAndSettings(t *testing.T) {
	m, _, s, _ := finishedLobby(t)
	if err := m.Rematch(s.ID, host()); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(s.ID)
	if got.Status != StatusWaiting || got.Map != "" || got.Connect != "" || got.MatchID != "" || got.Error != "" ||
		got.Score != [2]int{0, 0} || got.VoteRemainingMS != 0 {
		t.Fatalf("match state not reset: %+v", got)
	}
	if len(got.Members) != 4 || tm(got, pl(1).SteamID) != Team1 || tm(got, pl(2).SteamID) != Team1 ||
		tm(got, pl(3).SteamID) != Team2 || tm(got, pl(4).SteamID) != Team2 {
		t.Fatalf("members or teams changed: %+v", got.Members)
	}
	if got.Settings.MR != 15 {
		t.Fatalf("settings lost: MR %d", got.Settings.MR)
	}
	for _, mp := range got.Settings.Maps {
		if got.Tally[mp] != 0 {
			t.Fatalf("old votes survived: %v", got.Tally)
		}
	}
	for _, mem := range got.Members {
		if mem.Vote != "" {
			t.Fatalf("%s still has a vote", mem.Name)
		}
	}
}

func TestRematch_OnlyHostAndOnlyWhenFinished(t *testing.T) {
	m, _, s, _ := finishedLobby(t)
	if err := m.Rematch(s.ID, pl(2).SteamID); err != ErrNotHost {
		t.Fatalf("non-host: %v", err)
	}
	for _, st := range []Status{StatusWaiting, StatusVoting, StatusAwaitingServer, StatusInMatch} {
		m.SetStatus(s.ID, st, "")
		if err := m.Rematch(s.ID, host()); err != ErrBadState {
			t.Fatalf("%s: want ErrBadState, got %v", st, err)
		}
	}
	if err := m.Rematch("nope", host()); err != ErrNotFound {
		t.Fatalf("unknown lobby: %v", err)
	}
}

func TestRematch_LobbyCanBeReusedForAFullCycle(t *testing.T) {
	m, clk, s, log := finishedLobby(t)
	before := len(*log)
	if err := m.Rematch(s.ID, host()); err != nil {
		t.Fatal(err)
	}
	// players switch around: p2 moves to team 2, p3 to team 1
	if err := m.SetTeam(s.ID, pl(2).SteamID, TeamNone); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTeam(s.ID, pl(3).SteamID, Team1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTeam(s.ID, pl(2).SteamID, Team2); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(s.ID, host()); err != nil {
		t.Fatalf("ready after rematch: %v", err)
	}
	m.CastVote(s.ID, pl(3).SteamID, "de_cache")
	m.CastVote(s.ID, pl(4).SteamID, "de_cache")
	clk.Advance(30 * time.Second)
	m.Tick()
	if len(*log) != before+1 || (*log)[before].lobby != s.ID || (*log)[before].mapName != "de_cache" {
		t.Fatalf("second vote did not choose a map: %+v", *log)
	}
}

func TestSwapTeams_SwapsTeamsOnlyForHostWhileWaiting(t *testing.T) {
	m, _, s, _ := voteLobby(t) // p1,p2 on team 1; p3,p4 on team 2
	m.Join(s.ID, pl(5))        // unassigned
	if err := m.SwapTeams(s.ID, pl(2).SteamID); err != ErrNotHost {
		t.Fatalf("non-host: %v", err)
	}
	if err := m.SwapTeams(s.ID, host()); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(s.ID)
	if tm(got, pl(1).SteamID) != Team2 || tm(got, pl(2).SteamID) != Team2 ||
		tm(got, pl(3).SteamID) != Team1 || tm(got, pl(4).SteamID) != Team1 || tm(got, pl(5).SteamID) != TeamNone {
		t.Fatalf("teams after swap: %+v", got.Members)
	}
	m.Ready(s.ID, host())
	if err := m.SwapTeams(s.ID, host()); err != ErrBadState {
		t.Fatalf("swap while voting: %v", err)
	}
}
