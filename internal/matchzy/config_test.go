package matchzy

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/settings"
)

func input() ConfigInput {
	return ConfigInput{
		MatchID:   "m-test-1",
		Map:       "de_dust2",
		Team1Name: "Team 1",
		Team2Name: "Team 2",
		Team1:     []lobby.Player{{SteamID: "76561198000000001", Name: "guso"}, {SteamID: "76561198000000002", Name: "emil"}},
		Team2:     []lobby.Player{{SteamID: "76561198000000003", Name: "mads"}, {SteamID: "76561198000000004", Name: "jonas"}},
		Settings:  settings.Defaults(),
	}
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, b)
	}
	return m
}

func TestBuildConfig_Golden(t *testing.T) {
	got, err := BuildConfig(input())
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := os.ReadFile("testdata/config_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if g, w := decode(t, got), decode(t, wantBytes); !reflect.DeepEqual(g, w) {
		t.Fatalf("config differs from golden.\n got: %s\nwant: %s", got, wantBytes)
	}
}

func TestBuildConfig_KnifeRoundOff(t *testing.T) {
	in := input()
	in.Settings.KnifeRound = false
	got, err := BuildConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	sides := decode(t, got)["map_sides"].([]any)
	if len(sides) != 1 || sides[0] == "knife" {
		t.Fatalf("map_sides %v", sides)
	}
}

func TestBuildConfig_UnevenTeamsRejected(t *testing.T) {
	in := input()
	in.Team2 = in.Team2[:1]
	if _, err := BuildConfig(in); err == nil {
		t.Fatal("uneven teams accepted")
	}
}

func TestBuildConfig_NamesWithQuotesStayValidJSON(t *testing.T) {
	in := input()
	in.Team1[0].Name = `evil"; rcon_password x`
	got, err := BuildConfig(in)
	if err != nil {
		t.Fatal(err) // names are JSON strings, not cvars, so this is fine
	}
	decode(t, got) // must still be valid JSON
}

func TestLoadCommand(t *testing.T) {
	got := LoadCommand("http://lobby:8080/internal/match/m1/config.json?token=abc")
	want := `matchzy_loadmatch_url "http://lobby:8080/internal/match/m1/config.json?token=abc"`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"guso", "guso"},
		{"  spaced  out  ", "spaced out"},
		{`evil"; rcon_password x`, "evil rcon_password x"},
		{"line\nbreak\r\ttab", "linebreaktab"},
		{"back\\slash `tick` $var", "backslash tick var"},
		{"Zoë ★ 日本語", "Zoë ★ 日本語"},
		{"\x00\x07bell", "bell"},
		{"", "76561198000000001"},
		{`";;;"`, "76561198000000001"},
	}
	for _, c := range cases {
		if got := SafeName(c.in, "76561198000000001"); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSafeName_CapsLength(t *testing.T) {
	long := strings.Repeat("abcdefgh", 10)
	got := SafeName(long, "76561198000000001")
	if len([]rune(got)) != 32 || !strings.HasPrefix(long, got) {
		t.Fatalf("got %q (%d runes)", got, len([]rune(got)))
	}
}

func TestBuildConfig_NamesAreSanitized(t *testing.T) {
	in := input()
	in.Team1[0].Name = `evil"; quit`
	got, err := BuildConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	players := decode(t, got)["team1"].(map[string]any)["players"].(map[string]any)
	if players["76561198000000001"] != "evil quit" {
		t.Fatalf("name in config: %q", players["76561198000000001"])
	}
}
