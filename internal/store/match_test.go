package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
	if err := s.SetMatchRunning(ctx, "m1", "cs2-1", 1000); err != nil {
		t.Fatal(err)
	}
	m, err := s.RunningMatchOnServer(ctx, "cs2-1")
	if err != nil || m.ID != "m1" || m.Status != "running" || m.RunningAt != 1000 {
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
	if err := s.SetMatchRunning(ctx, "m1", "cs2-1", 1000); !errors.Is(err, ErrNotPending) {
		t.Fatalf("want ErrNotPending, got %v", err)
	}
	m, _ := s.GetMatch(ctx, "m1")
	if m.Status != MatchAborted || m.ServerID != "" {
		t.Fatalf("aborted match changed: %+v", m)
	}
}

func TestRunningMatches_ListsOnlyRunning(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	mk := func(id string, at int64, finish bool) {
		m := newMatch()
		m.ID, m.LobbyID = id, "lobby-"+id
		s.CreateMatch(ctx, m, []RosterEntry{{"1", 1}, {"2", 2}})
		s.SetMatchRunning(ctx, id, "cs2-"+id, at)
		if finish {
			s.SetMatchStatus(ctx, id, MatchAborted, 5)
		}
	}
	mk("a", 100, false)
	mk("b", 200, false)
	mk("c", 300, true) // ended: not listed
	m := newMatch()
	m.ID, m.LobbyID = "d", "lobby-d"
	s.CreateMatch(ctx, m, []RosterEntry{{"1", 1}, {"2", 2}}) // still pending: not listed
	got, err := s.RunningMatches(ctx)
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[0].RunningAt != 100 || got[1].ID != "b" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestOpen_MigratesDatabaseWithoutRunningAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// the schema as it was before running_at existed
	_, err = db.Exec(`CREATE TABLE players (steamid TEXT PRIMARY KEY, name TEXT NOT NULL, avatar TEXT NOT NULL DEFAULT '', elo INTEGER NOT NULL DEFAULT 1000);
	CREATE TABLE matches (id TEXT PRIMARY KEY, lobby_id TEXT NOT NULL DEFAULT '', lobby_name TEXT NOT NULL, server_id TEXT NOT NULL DEFAULT '', map TEXT NOT NULL,
		settings_json TEXT NOT NULL, token TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, team1_score INTEGER NOT NULL DEFAULT 0,
		team2_score INTEGER NOT NULL DEFAULT 0, winner INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE match_players (match_id TEXT NOT NULL REFERENCES matches(id) ON DELETE CASCADE, steamid TEXT NOT NULL, team INTEGER NOT NULL, kills INTEGER NOT NULL DEFAULT 0,
		deaths INTEGER NOT NULL DEFAULT 0, assists INTEGER NOT NULL DEFAULT 0, damage INTEGER NOT NULL DEFAULT 0, headshots INTEGER NOT NULL DEFAULT 0, rounds INTEGER NOT NULL DEFAULT 0,
		elo_before INTEGER NOT NULL DEFAULT 0, elo_after INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (match_id, steamid));
	INSERT INTO players (steamid, name, elo) VALUES ('1', 'old', 1234);
	INSERT INTO matches (id, lobby_name, map, settings_json, status, started_at) VALUES ('old1', 'Old', 'de_dust2', '{}', 'finished', 5);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("opening an old database: %v", err)
	}
	defer s.Close()
	m, err := s.GetMatch(context.Background(), "old1")
	if err != nil || m.RunningAt != 0 || m.Status != "finished" {
		t.Fatalf("old match unreadable after migration: %+v %v", m, err)
	}
	if p, _ := s.GetPlayer(context.Background(), "1"); p.Elo != 1234 {
		t.Fatalf("old player lost: %+v", p)
	}
	s.Close()
	s2, err := Open(path) // opening twice must not fail on "duplicate column"
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	s2.Close()
}

func TestGetMatchOfLobby_NewestOfAnyStatus(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	m := newMatch()
	m.ID, m.StartedAt = "first", 100
	s.CreateMatch(ctx, m, []RosterEntry{{"1", 1}, {"2", 2}})
	s.SetMatchStatus(ctx, "first", MatchAborted, 150)
	m.ID, m.StartedAt = "second", 200
	s.CreateMatch(ctx, m, []RosterEntry{{"1", 1}, {"2", 2}})
	got, err := s.GetMatchOfLobby(ctx, "l1")
	if err != nil || got.ID != "second" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.GetMatchOfLobby(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
