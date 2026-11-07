// Package lobby holds the rules for lobbies: membership, teams, host handover,
// the map vote and live updates. It does no I/O.
package lobby

import (
	"errors"

	"github.com/fideliusfalcon/fragnight/internal/settings"
)

const (
	// TeamSize is the number of slots on each team.
	TeamSize = 5
	// Capacity is the number of team slots in a lobby.
	Capacity = TeamSize * 2
)

type Team int

const (
	TeamNone Team = 0
	Team1    Team = 1
	Team2    Team = 2
)

type Status string

const (
	StatusWaiting        Status = "waiting"
	StatusVoting         Status = "voting"
	StatusAwaitingServer Status = "awaiting_server"
	StatusInMatch        Status = "in_match"
	StatusFinished       Status = "finished"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrAlreadyInLobby = errors.New("already in a lobby")
	ErrTeamFull       = errors.New("team is full")
	ErrNotHost        = errors.New("only the host can do that")
	ErrBadState       = errors.New("not possible right now")
	ErrBadTeams       = errors.New("both teams need the same number of players, at least one each")
	ErrNotVoter       = errors.New("only players on a team can vote")
	ErrBadTeam        = errors.New("invalid team")
	ErrBadMap         = errors.New("that map is not in the pool")
)

type Player struct {
	SteamID   string `json:"steamid"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar"`
}

type Member struct {
	Player
	Team Team   `json:"team"`
	Vote string `json:"vote"`
}

// Snapshot is an immutable copy of a lobby's state, safe to send to clients.
type Snapshot struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	HostSteamID     string         `json:"host"`
	Public          bool           `json:"public"`
	Status          Status         `json:"status"`
	Members         []Member       `json:"members"`
	Settings        settings.Match `json:"settings"`
	VoteRemainingMS int64          `json:"vote_remaining_ms"`
	Tally           map[string]int `json:"tally"`
	Map             string         `json:"map"`
	Connect         string         `json:"connect"`
	Score           [2]int         `json:"score"`
	Error           string         `json:"error"`
}

// Summary is one row of the lobby browser.
type Summary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	HostName        string `json:"host_name"`
	Players         int    `json:"players"`
	Capacity        int    `json:"capacity"`
	MapInfo         string `json:"map_info"`
	Status          Status `json:"status"`
	VoteRemainingMS int64  `json:"vote_remaining_ms"`
	Score           [2]int `json:"score"`
}
