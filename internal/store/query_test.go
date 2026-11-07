package store

import (
	"context"
	"errors"
	"testing"
)

// finished plays a complete match between team 1 = {a} and team 2 = {b}.
func finished(t *testing.T, s *Store, id string, a, b string, winner int, endedAt int64, dA, dB int) {
	t.Helper()
	ctx := context.Background()
	m := newMatch()
	m.ID, m.LobbyID = id, "lobby-"+id
	if err := s.CreateMatch(ctx, m, []RosterEntry{{a, 1}, {b, 2}}); err != nil {
		t.Fatal(err)
	}
	s.SetMatchRunning(ctx, id, "cs2-1", 1000)
	err := s.FinishMatch(ctx, FinishInput{MatchID: id, Team1Score: 13, Team2Score: 5, Winner: winner, EndedAt: endedAt,
		Players: []PlayerResult{
			{SteamID: a, Kills: 20, Deaths: 10, Assists: 2, Damage: 2400, Headshots: 10, Rounds: 20, EloDelta: dA},
			{SteamID: b, Kills: 5, Deaths: 20, Assists: 1, Damage: 600, Headshots: 1, Rounds: 20, EloDelta: dB},
		}})
	if err != nil {
		t.Fatal(err)
	}
}

func seedThree(t *testing.T, s *Store) {
	t.Helper()
	for _, p := range []Player{{SteamID: "1", Name: "alice"}, {SteamID: "2", Name: "bob"}, {SteamID: "3", Name: "carol"}} {
		s.UpsertPlayer(context.Background(), p)
	}
}

func TestLeaderboard_OrderedByElo_CompletedOnly(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	ctx := context.Background()
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	// carol only has an aborted match: she must not appear
	m := newMatch()
	m.ID, m.LobbyID = "m2", "lobby-m2"
	s.CreateMatch(ctx, m, []RosterEntry{{"3", 1}, {"2", 2}})
	s.SetMatchStatus(ctx, "m2", MatchAborted, 5)

	rows, err := s.Leaderboard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "alice" || rows[1].Name != "bob" {
		t.Fatalf("%+v", rows)
	}
	if rows[0].Matches != 1 || rows[0].Wins != 1 || rows[1].Wins != 0 || rows[1].Matches != 1 {
		t.Fatalf("matches/wins %+v", rows)
	}
	if rows[0].KD() != 2.0 || rows[0].ADR() != 120.0 || rows[0].HSPercent() != 50.0 {
		t.Fatalf("derived: kd %v adr %v hs %v", rows[0].KD(), rows[0].ADR(), rows[0].HSPercent())
	}
}

func TestDerivedStats_NoDivisionByZero(t *testing.T) {
	var r LeaderRow
	if r.KD() != 0 || r.ADR() != 0 || r.HSPercent() != 0 {
		t.Fatal("zero row should give zeros")
	}
	r.Kills = 5 // no deaths: K/D is just the kills
	if r.KD() != 5 {
		t.Fatalf("kd %v", r.KD())
	}
}

func TestPlayerSummary_RecentMatchesNewestFirst(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	ctx := context.Background()
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	finished(t, s, "m2", "2", "1", 1, 200, 0, 0)
	sum, err := s.PlayerSummary(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Name != "alice" || sum.Matches != 2 || len(sum.Recent) != 2 || sum.Recent[0].ID != "m2" || sum.Recent[1].ID != "m1" {
		t.Fatalf("%+v", sum)
	}
	if _, err := s.PlayerSummary(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown player: %v", err)
	}
	// a player with no matches still has a page
	sum, err = s.PlayerSummary(ctx, "3")
	if err != nil || sum.Matches != 0 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestMatchesOf_OnlyOwnAndCompleted(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	ctx := context.Background()
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	m := newMatch()
	m.ID, m.LobbyID = "m2", "lobby-m2"
	s.CreateMatch(ctx, m, []RosterEntry{{"3", 1}, {"2", 2}})
	s.SetMatchStatus(ctx, "m2", MatchAborted, 5)
	rows, err := s.MatchesOf(ctx, "3", 20)
	if err != nil || len(rows) != 0 {
		t.Fatalf("carol: %+v %v", rows, err)
	}
	rows, _ = s.MatchesOf(ctx, "2", 20)
	if len(rows) != 1 || rows[0].ID != "m1" || rows[0].MyTeam != 2 || rows[0].EloDelta != -16 {
		t.Fatalf("bob: %+v", rows)
	}
}

func TestMatchDetail_PlayersWithNamesAndDeltas(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	d, err := s.MatchDetail(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Team1Score != 13 || len(d.Players) != 2 || d.Players[0].Name != "alice" || d.Players[0].EloDelta() != 16 || d.Players[1].EloDelta() != -16 {
		t.Fatalf("%+v", d)
	}
	if _, err := s.MatchDetail(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestDeleteMatch_RemovesStats(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	ctx := context.Background()
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	if err := s.DeleteMatch(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Leaderboard(ctx)
	if len(rows) != 0 {
		t.Fatalf("leaderboard still has %+v", rows)
	}
	if err := s.DeleteMatch(ctx, "m1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestRecentMatches(t *testing.T) {
	s := open(t)
	seedThree(t, s)
	finished(t, s, "m1", "1", "2", 1, 100, 16, -16)
	finished(t, s, "m2", "1", "2", 1, 200, 30, -30)
	rows, err := s.RecentMatches(context.Background(), 1)
	if err != nil || len(rows) != 1 || rows[0].ID != "m2" {
		t.Fatalf("%+v %v", rows, err)
	}
}
