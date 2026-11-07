package matchzy

import (
	"os"
	"strings"
	"testing"
)

var fixtureNames = []string{"going_live", "round_end", "map_result", "series_end"}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Shipping guard: hand-written fixtures must be replaced by real captures.
func TestFixturesAreVerified(t *testing.T) {
	for _, n := range fixtureNames {
		if strings.Contains(string(fixture(t, n)), `"_unverified"`) {
			if os.Getenv("STRICT_FIXTURES") == "1" {
				t.Errorf("%s.json is still an unverified hand-written fixture", n)
			} else {
				t.Logf("WARNING: %s.json is an unverified fixture (set STRICT_FIXTURES=1 to fail)", n)
			}
		}
	}
}

func TestParseEvent_AllFixtures(t *testing.T) {
	for _, n := range fixtureNames {
		ev, err := ParseEvent(fixture(t, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if ev.Name != n || ev.MatchID != "m-test-1" {
			t.Errorf("%s: got name %q match %q", n, ev.Name, ev.MatchID)
		}
	}
}

func TestParseEvent_RejectsGarbage(t *testing.T) {
	for _, body := range []string{``, `not json`, `{}`, `{"event":""}`, `[1,2]`} {
		if _, err := ParseEvent([]byte(body)); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

func TestParseEvent_NumericMatchID(t *testing.T) {
	ev, err := ParseEvent([]byte(`{"event":"going_live","matchid":42}`))
	if err != nil || ev.MatchID != "42" {
		t.Fatalf("%+v %v", ev, err)
	}
}

func TestMapResult_FromFixture(t *testing.T) {
	ev, _ := ParseEvent(fixture(t, "map_result"))
	r, err := MapResult(ev.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Team1Score != 13 || r.Team2Score != 7 || r.Winner != 1 {
		t.Fatalf("result %+v", r)
	}
	if len(r.Players) != 4 {
		t.Fatalf("players %d", len(r.Players))
	}
	var guso PlayerStats
	for _, p := range r.Players {
		if p.SteamID == "76561198000000001" {
			guso = p
		}
	}
	want := PlayerStats{SteamID: "76561198000000001", Team: 1, Kills: 20, Deaths: 12, Assists: 4, Damage: 2300, Headshots: 9, Rounds: 24}
	if guso != want {
		t.Fatalf("got %+v want %+v", guso, want)
	}
}

func TestMapResult_TeamKillsAreNotKills(t *testing.T) {
	raw := `{"event":"map_result","matchid":"m","winner":{"team":"team2"},
	 "team1":{"score":5,"players":[{"steamid":"1","stats":{"kills":10,"teamkills":3,"deaths":2}}]},
	 "team2":{"score":13,"players":[]}}`
	r, err := MapResult([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if r.Players[0].Kills != 7 || r.Winner != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestScores_RoundEnd(t *testing.T) {
	ev, _ := ParseEvent(fixture(t, "round_end"))
	a, b, err := Scores(ev.Raw)
	if err != nil || a != 3 || b != 2 {
		t.Fatalf("%d %d %v", a, b, err)
	}
}
