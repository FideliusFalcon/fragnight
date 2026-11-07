package settings

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// DefaultMapPool is the current CS2 Active Duty pool (as of July 2026: Cache
// replaced Overpass). It is what a new lobby starts with. Valve rotates it a
// couple of times a year, so update it here when it changes.
var DefaultMapPool = []string{
	"de_ancient", "de_anubis", "de_cache", "de_dust2", "de_inferno", "de_mirage", "de_nuke",
}

// OtherMaps are official maps outside Active Duty that hosts can add in Settings.
// The ids follow Valve's naming; de_cache and de_canals are recent, so confirm
// them with `maps *` on a server if a map fails to load.
var OtherMaps = []string{
	"de_overpass", "de_train", "de_vertigo", "de_canals", "cs_office", "cs_italy",
}

// MaxMaps limits how many maps a pool may hold.
const MaxMaps = 20

// mapNameRe is deliberately strict: map names are sent to the game server, so
// only plain lowercase ids are accepted (no paths, spaces, quotes or ';').
// Workshop maps are not supported.
var mapNameRe = regexp.MustCompile(`^(de|cs|ar|dz)_[a-z0-9_]{1,40}$`)

// Match holds the host-editable settings of a match.
type Match struct {
	MR              int      `json:"mr"`
	Overtime        bool     `json:"overtime"`
	OTMaxRounds     int      `json:"ot_max_rounds"`
	OTStartMoney    int      `json:"ot_start_money"`
	KnifeRound      bool     `json:"knife_round"`
	FriendlyFire    bool     `json:"friendly_fire"`
	DeadTalk        bool     `json:"dead_talk"`
	TimeoutsPerTeam int      `json:"timeouts_per_team"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	Maps            []string `json:"maps"`
}

// Defaults returns the tournament defaults (ESL/BLAST style).
func Defaults() Match {
	return Match{
		MR: 12, Overtime: true, OTMaxRounds: 6, OTStartMoney: 10000,
		KnifeRound: true, FriendlyFire: true, DeadTalk: false,
		TimeoutsPerTeam: 4, TimeoutSeconds: 30,
		Maps: slices.Clone(DefaultMapPool),
	}
}

func (m Match) Validate() error {
	if len(m.Maps) == 0 {
		return fmt.Errorf("pick at least one map")
	}
	if len(m.Maps) > MaxMaps {
		return fmt.Errorf("at most %d maps", MaxMaps)
	}
	seen := map[string]bool{}
	for _, name := range m.Maps {
		if !mapNameRe.MatchString(name) {
			return fmt.Errorf("%q is not a valid map name (use ids like de_dust2)", name)
		}
		if seen[name] {
			return fmt.Errorf("map %q is listed twice", name)
		}
		seen[name] = true
	}
	if m.MR < 1 {
		return fmt.Errorf("MR must be at least 1")
	}
	if m.Overtime && (m.OTMaxRounds < 2 || m.OTMaxRounds%2 != 0) {
		return fmt.Errorf("overtime rounds must be an even number")
	}
	if m.OTStartMoney < 0 {
		return fmt.Errorf("overtime money must not be negative")
	}
	if m.TimeoutsPerTeam < 0 || m.TimeoutSeconds < 0 {
		return fmt.Errorf("timeouts must not be negative")
	}
	return nil
}

func b(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// Cvars returns the server cvars for the match config. Knife round is not a
// cvar; it is part of the MatchZy match config.
func (m Match) Cvars() map[string]string {
	return map[string]string{
		"mp_maxrounds":           strconv.Itoa(m.MR * 2),
		"mp_overtime_enable":     b(m.Overtime),
		"mp_overtime_maxrounds":  strconv.Itoa(m.OTMaxRounds),
		"mp_overtime_startmoney": strconv.Itoa(m.OTStartMoney),
		"mp_friendlyfire":        b(m.FriendlyFire),
		"sv_deadtalk":            b(m.DeadTalk),
		"mp_team_timeout_max":    strconv.Itoa(m.TimeoutsPerTeam),
		"mp_team_timeout_time":   strconv.Itoa(m.TimeoutSeconds),
	}
}
