package store

import (
	"context"
	"errors"
	"testing"
)

func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, p := range []Player{{SteamID: "1", Name: "a"}, {SteamID: "2", Name: "b"}} {
		if err := s.UpsertPlayer(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE players SET elo = 1120 WHERE steamid = '1'`); err != nil {
		t.Fatal(err)
	}
}

func newMatch() Match {
	return Match{ID: "m1", LobbyID: "l1", LobbyName: "LAN", Map: "de_dust2", SettingsJSON: "{}", Token: "tok", ConfigJSON: `{"matchid":"m1"}`, StartedAt: 100}
}

func TestCreateMatch_SnapshotsEloAndRoster(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	if err := s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMatch(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "pending" || m.Token != "tok" || m.ConfigJSON != `{"matchid":"m1"}` || m.LobbyID != "l1" {
		t.Fatalf("%+v", m)
	}
	roster, err := s.Roster(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 2 || roster[0].SteamID != "1" || roster[0].Team != 1 || roster[0].EloBefore != 1120 || roster[1].EloBefore != 1000 {
		t.Fatalf("roster %+v", roster)
	}
}

func TestGetMatch_NotFound(t *testing.T) {
	s := open(t)
	if _, err := s.GetMatch(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMatchLifecycle_RunningOnServer(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	if _, err := s.RunningMatchOnServer(ctx, "cs2-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending match counted as running: %v", err)
	}
	if err := s.SetMatchRunning(ctx, "m1", "cs2-1"); err != nil {
		t.Fatal(err)
	}
	m, err := s.RunningMatchOnServer(ctx, "cs2-1")
	if err != nil || m.ID != "m1" || m.Status != "running" {
		t.Fatalf("%+v %v", m, err)
	}
	if err := s.SetMatchStatus(ctx, "m1", "failed", 200); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunningMatchOnServer(ctx, "cs2-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed match still running: %v", err)
	}
	m, _ = s.GetMatch(ctx, "m1")
	if m.Status != "failed" || m.EndedAt != 200 {
		t.Fatalf("%+v", m)
	}
}

func TestPendingMatchOfLobby(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	m, err := s.ActiveMatchOfLobby(ctx, "l1")
	if err != nil || m.ID != "m1" {
		t.Fatalf("%+v %v", m, err)
	}
	s.SetMatchStatus(ctx, "m1", "aborted", 1)
	if _, err := s.ActiveMatchOfLobby(ctx, "l1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ended match still active: %v", err)
	}
}

func TestSetMatchRunning_OnlyFromPending(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	s.SetMatchStatus(ctx, "m1", MatchAborted, 5)
	if err := s.SetMatchRunning(ctx, "m1", "cs2-1"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("want ErrNotPending, got %v", err)
	}
	m, _ := s.GetMatch(ctx, "m1")
	if m.Status != MatchAborted || m.ServerID != "" {
		t.Fatalf("aborted match changed: %+v", m)
	}
}
