package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fideliusfalcon/fragnight/internal/store"
)

// playMatch records a finished 1v1 match between players a (team 1) and b (team 2).
func playMatch(t *testing.T, e *env, id string, a, b int, nameA, nameB string, winner int, eloA, eloB int) {
	t.Helper()
	ctx := context.Background()
	e.store.UpsertPlayer(ctx, store.Player{SteamID: sid(a), Name: nameA})
	e.store.UpsertPlayer(ctx, store.Player{SteamID: sid(b), Name: nameB})
	err := e.store.CreateMatch(ctx, store.Match{ID: id, LobbyID: "lobby-" + id, LobbyName: "Night " + id, Map: "de_dust2", SettingsJSON: "{}", Token: "t",
		ConfigJSON: "{}", StartedAt: 1}, []store.RosterEntry{{SteamID: sid(a), Team: 1}, {SteamID: sid(b), Team: 2}})
	if err != nil {
		t.Fatal(err)
	}
	e.store.SetMatchRunning(ctx, id, "cs2-1")
	err = e.store.FinishMatch(ctx, store.FinishInput{MatchID: id, Team1Score: 13, Team2Score: 7, Winner: winner, EndedAt: 100,
		Players: []store.PlayerResult{
			{SteamID: sid(a), Kills: 20, Deaths: 10, Damage: 2400, Headshots: 10, Rounds: 20, EloAfter: eloA},
			{SteamID: sid(b), Kills: 5, Deaths: 20, Damage: 600, Headshots: 1, Rounds: 20, EloAfter: eloB},
		}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatsPages_RequireLogin(t *testing.T) {
	e := newEnv(t, true)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/stats", "/history", "/player/" + sid(1), "/match/m1", "/admin"} {
		resp, err := c.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 3 {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func TestStatsPage_LeaderboardOrderedAndCompletedOnly(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	// carol has only an aborted match
	ctx := context.Background()
	e.store.UpsertPlayer(ctx, store.Player{SteamID: sid(3), Name: "carol"})
	e.store.CreateMatch(ctx, store.Match{ID: "m2", LobbyID: "x", LobbyName: "x", Map: "de_dust2", SettingsJSON: "{}"}, []store.RosterEntry{{SteamID: sid(3), Team: 1}})
	e.store.SetMatchStatus(ctx, "m2", store.MatchAborted, 1)

	u := e.login(t, 1, "alice")
	resp, body := u.get(t, "/stats")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ia, ib := strings.Index(body, "alice"), strings.Index(body, "bob")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("order wrong: alice at %d, bob at %d", ia, ib)
	}
	if strings.Contains(body, "carol") {
		t.Fatal("player with only an aborted match is on the leaderboard")
	}
	if !strings.Contains(body, "1016") || !strings.Contains(body, "2.00") || !strings.Contains(body, "120.0") {
		t.Fatalf("missing rating/K-D/ADR in: %s", body)
	}
}

func TestStatsPage_EscapesNames(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, `<b onmouseover=x>`, "bob", 1, 1016, 984)
	u := e.login(t, 2, "bob")
	for _, path := range []string{"/stats", "/player/" + sid(1), "/match/m1"} {
		_, body := u.get(t, path)
		if strings.Contains(body, "<b onmouseover") {
			t.Errorf("%s: unescaped name", path)
		}
	}
}

func TestPlayerPage_ShowsTotalsAndRecent(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	u := e.login(t, 2, "bob")
	resp, body := u.get(t, "/player/"+sid(1))
	if resp.StatusCode != 200 || !strings.Contains(body, "alice") || !strings.Contains(body, "Night m1") || !strings.Contains(body, "/match/m1") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	resp, _ = u.get(t, "/player/"+sid(777))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown player: %d", resp.StatusCode)
	}
	resp, _ = u.get(t, "/player/not-a-steamid")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
}

func TestHistory_OnlyOwnMatches(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	alice := e.login(t, 1, "alice")
	_, body := alice.get(t, "/history")
	if !strings.Contains(body, "Night m1") || !strings.Contains(body, "+16") {
		t.Fatalf("alice history: %s", body)
	}
	dave := e.login(t, 4, "dave")
	_, body = dave.get(t, "/history")
	if strings.Contains(body, "Night m1") {
		t.Fatal("dave sees someone else's match")
	}
}

func TestMatchPage_ShowsEloDelta(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	u := e.login(t, 1, "alice")
	resp, body := u.get(t, "/match/m1")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"+16", "-16", "13", "alice", "bob", "de_dust2"} {
		if !strings.Contains(body, want) {
			t.Errorf("match page missing %q", want)
		}
	}
	resp, _ = u.get(t, "/match/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown match: %d", resp.StatusCode)
	}
}

func TestAdmin_ForbiddenForNonAdmin(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	u := e.login(t, 1, "alice")
	resp, _ := u.get(t, "/admin")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /admin: %d", resp.StatusCode)
	}
	if r := u.post(t, "/admin/servers/cs2-1/reset", url.Values{}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("reset: %d", r.StatusCode)
	}
	if r := u.post(t, "/admin/matches/m1/delete", url.Values{}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if len(e.matches.resets) != 0 {
		t.Fatal("non-admin reset a server")
	}
	if _, err := e.store.GetMatch(context.Background(), "m1"); err != nil {
		t.Fatal("non-admin deleted a match")
	}
}

func TestAdmin_PageResetAndDelete(t *testing.T) {
	e := newEnv(t, true)
	playMatch(t, e, "m1", 1, 2, "alice", "bob", 1, 1016, 984)
	admin := e.login(t, 900, "root")
	resp, body := admin.get(t, "/admin")
	if resp.StatusCode != 200 || !strings.Contains(body, "Server 1") || !strings.Contains(body, "Night m1") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if r := admin.post(t, "/admin/servers/cs2-1/reset", url.Values{}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset: %d", r.StatusCode)
	}
	if len(e.matches.resets) != 1 || e.matches.resets[0] != "cs2-1" {
		t.Fatalf("resets %v", e.matches.resets)
	}
	if r := admin.post(t, "/admin/matches/m1/delete", url.Values{}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if _, err := e.store.GetMatch(context.Background(), "m1"); err == nil {
		t.Fatal("match not deleted")
	}
}
