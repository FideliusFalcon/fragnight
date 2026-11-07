// Package matchzy builds MatchZy match configs and parses its webhook events.
// Field names follow MatchZy's Get5-compatible formats; see deploy/README.md for
// the state of verification against a real server.
package matchzy

import (
	"encoding/json"
	"fmt"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/settings"
)

type ConfigInput struct {
	MatchID   string
	Map       string
	Team1Name string
	Team2Name string
	Team1     []lobby.Player
	Team2     []lobby.Player
	Settings  settings.Match
}

type teamConfig struct {
	Name    string            `json:"name"`
	Players map[string]string `json:"players"`
}

type matchConfig struct {
	MatchID        string            `json:"matchid"`
	NumMaps        int               `json:"num_maps"`
	MapList        []string          `json:"maplist"`
	SkipVeto       bool              `json:"skip_veto"`
	PlayersPerTeam int               `json:"players_per_team"`
	MapSides       []string          `json:"map_sides"`
	ClinchSeries   bool              `json:"clinch_series"`
	Team1          teamConfig        `json:"team1"`
	Team2          teamConfig        `json:"team2"`
	Cvars          map[string]string `json:"cvars"`
}

func roster(ps []lobby.Player) map[string]string {
	m := make(map[string]string, len(ps))
	for _, p := range ps {
		m[p.SteamID] = p.Name
	}
	return m
}

// BuildConfig returns the JSON match config MatchZy loads with matchzy_loadmatch_url.
func BuildConfig(in ConfigInput) ([]byte, error) {
	if len(in.Team1) == 0 || len(in.Team1) != len(in.Team2) {
		return nil, fmt.Errorf("teams must be non-empty and equal in size (%d vs %d)", len(in.Team1), len(in.Team2))
	}
	side := "team1_ct"
	if in.Settings.KnifeRound {
		side = "knife"
	}
	cfg := matchConfig{
		MatchID: in.MatchID, NumMaps: 1, MapList: []string{in.Map}, SkipVeto: true,
		PlayersPerTeam: len(in.Team1), MapSides: []string{side}, ClinchSeries: true,
		Team1: teamConfig{Name: in.Team1Name, Players: roster(in.Team1)},
		Team2: teamConfig{Name: in.Team2Name, Players: roster(in.Team2)},
		Cvars: in.Settings.Cvars(),
	}
	return json.Marshal(cfg)
}

// LoadCommand is the RCON command that makes MatchZy fetch and load a match config.
func LoadCommand(configURL string) string {
	return fmt.Sprintf("matchzy_loadmatch_url %q", configURL)
}
