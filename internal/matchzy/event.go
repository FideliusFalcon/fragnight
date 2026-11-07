package matchzy

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Event is one webhook delivery from MatchZy.
type Event struct {
	Name    string
	MatchID string
	Raw     json.RawMessage
}

// ParseEvent reads the event name and match id from a webhook body.
func ParseEvent(body []byte) (Event, error) {
	var head struct {
		Event   string `json:"event"`
		MatchID any    `json:"matchid"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return Event{}, fmt.Errorf("bad event body: %w", err)
	}
	if head.Event == "" {
		return Event{}, errors.New("event has no name")
	}
	id := ""
	switch v := head.MatchID.(type) {
	case string:
		id = v
	case float64:
		id = fmt.Sprintf("%.0f", v)
	}
	return Event{Name: head.Event, MatchID: id, Raw: json.RawMessage(body)}, nil
}

type rawTeam struct {
	Score   int `json:"score"`
	Players []struct {
		SteamID string `json:"steamid"`
		Stats   struct {
			Kills     int `json:"kills"`
			TeamKills int `json:"teamkills"`
			Deaths    int `json:"deaths"`
			Assists   int `json:"assists"`
			Damage    int `json:"damage"`
			Headshots int `json:"head_shot_kills"`
			Rounds    int `json:"rounds_played"`
		} `json:"stats"`
	} `json:"players"`
}

type rawScoreEvent struct {
	Winner struct {
		Team string `json:"team"`
	} `json:"winner"`
	Team1 rawTeam `json:"team1"`
	Team2 rawTeam `json:"team2"`
}

// Scores returns the current team scores from a round_end or map_result event.
func Scores(raw json.RawMessage) (team1, team2 int, err error) {
	var e rawScoreEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return 0, 0, err
	}
	return e.Team1.Score, e.Team2.Score, nil
}

type PlayerStats struct {
	SteamID   string
	Team      int
	Kills     int
	Deaths    int
	Assists   int
	Damage    int
	Headshots int
	Rounds    int
}

type Result struct {
	Team1Score int
	Team2Score int
	Winner     int // 1, 2, or 0 for none
	Players    []PlayerStats
}

// MapResult reads the final result of a map. Team kills are subtracted from kills.
func MapResult(raw json.RawMessage) (Result, error) {
	var e rawScoreEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return Result{}, err
	}
	r := Result{Team1Score: e.Team1.Score, Team2Score: e.Team2.Score}
	switch e.Winner.Team {
	case "team1":
		r.Winner = 1
	case "team2":
		r.Winner = 2
	}
	for i, t := range []rawTeam{e.Team1, e.Team2} {
		for _, p := range t.Players {
			s := p.Stats
			r.Players = append(r.Players, PlayerStats{
				SteamID: p.SteamID, Team: i + 1, Kills: max(s.Kills-s.TeamKills, 0), Deaths: s.Deaths,
				Assists: s.Assists, Damage: s.Damage, Headshots: s.Headshots, Rounds: s.Rounds,
			})
		}
	}
	return r, nil
}
