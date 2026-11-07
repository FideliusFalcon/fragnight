package store

import (
	"context"
	"errors"
	"testing"
)

func finishInput() FinishInput {
	return FinishInput{
		MatchID: "m1", Team1Score: 13, Team2Score: 7, Winner: 1, EndedAt: 500,
		Players: []PlayerResult{
			{SteamID: "1", Kills: 20, Deaths: 12, Assists: 4, Damage: 2300, Headshots: 9, Rounds: 24, EloDelta: 16},
			{SteamID: "2", Kills: 5, Deaths: 20, Assists: 1, Damage: 700, Headshots: 1, Rounds: 24, EloDelta: -16},
		},
	}
}

func TestFinishMatch_WritesStatsAndRatings(t *testing.T) {
	s := open(t)
	seed(t, s) // player 1 has 1120, player 2 has 1000
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	s.SetMatchRunning(ctx, "m1", "cs2-1")
	if err := s.FinishMatch(ctx, finishInput()); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetMatch(ctx, "m1")
	if m.Status != MatchFinished || m.Team1Score != 13 || m.Team2Score != 7 || m.Winner != 1 || m.EndedAt != 500 {
		t.Fatalf("%+v", m)
	}
	roster, _ := s.Roster(ctx, "m1")
	r := roster[0]
	if r.SteamID != "1" || r.Kills != 20 || r.Deaths != 12 || r.Damage != 2300 || r.EloBefore != 1120 || r.EloAfter != 1136 {
		t.Fatalf("%+v", r)
	}
	p, _ := s.GetPlayer(ctx, "1")
	if p.Elo != 1136 {
		t.Fatalf("player elo %d", p.Elo)
	}
}

func TestFinishMatch_OnlyOnce(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	s.SetMatchRunning(ctx, "m1", "cs2-1")
	if err := s.FinishMatch(ctx, finishInput()); err != nil {
		t.Fatal(err)
	}
	second := finishInput()
	second.Players[0].EloDelta = 9999
	if err := s.FinishMatch(ctx, second); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	p, _ := s.GetPlayer(ctx, "1")
	if p.Elo != 1136 {
		t.Fatalf("second finish changed elo to %d", p.Elo)
	}
}

func TestFinishMatch_RefusesAbortedAndPending(t *testing.T) {
	s := open(t)
	seed(t, s)
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}})
	if err := s.FinishMatch(ctx, finishInput()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("pending: %v", err)
	}
	s.SetMatchRunning(ctx, "m1", "cs2-1")
	s.SetMatchStatus(ctx, "m1", MatchAborted, 1)
	if err := s.FinishMatch(ctx, finishInput()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("aborted: %v", err)
	}
	if p, _ := s.GetPlayer(ctx, "1"); p.Elo != 1120 {
		t.Fatalf("aborted match changed elo: %d", p.Elo)
	}
}

func TestFinishMatch_AppliesDeltaToCurrentRating(t *testing.T) {
	s := open(t)
	seed(t, s) // player 1 starts at 1120
	ctx := context.Background()
	s.CreateMatch(ctx, newMatch(), []RosterEntry{{"1", 1}, {"2", 2}}) // snapshots 1120
	// meanwhile another match finished and moved the player's rating
	if _, err := s.db.Exec(`UPDATE players SET elo = 1500 WHERE steamid = '1'`); err != nil {
		t.Fatal(err)
	}
	s.SetMatchRunning(ctx, "m1", "cs2-1")
	if err := s.FinishMatch(ctx, finishInput()); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetPlayer(ctx, "1")
	if p.Elo != 1516 {
		t.Fatalf("rating %d, want 1516 (current 1500 + 16), the other match's change was overwritten", p.Elo)
	}
	roster, _ := s.Roster(ctx, "m1")
	if roster[0].EloAfter != 1136 { // per-match display: snapshot + this match's delta
		t.Fatalf("elo_after %d", roster[0].EloAfter)
	}
}
