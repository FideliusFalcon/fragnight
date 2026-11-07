package store

import (
	"context"
	"errors"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertPlayer_KeepsElo(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.UpsertPlayer(ctx, Player{SteamID: "1", Name: "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE players SET elo = 1100 WHERE steamid = '1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPlayer(ctx, Player{SteamID: "1", Name: "new", AvatarURL: "a"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPlayer(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "new" || p.Elo != 1100 || p.AvatarURL != "a" {
		t.Fatalf("got %+v", p)
	}
}

func TestGetPlayer_NotFound(t *testing.T) {
	s := open(t)
	_, err := s.GetPlayer(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestNewPlayerStartsAt1000(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.UpsertPlayer(ctx, Player{SteamID: "2", Name: "x"})
	p, _ := s.GetPlayer(ctx, "2")
	if p.Elo != 1000 {
		t.Fatalf("elo %d", p.Elo)
	}
}
