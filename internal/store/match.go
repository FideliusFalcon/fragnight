package store

import (
	"context"
	"database/sql"
	"errors"
)

// Match statuses.
const (
	MatchPending  = "pending"  // created, waiting for a server
	MatchRunning  = "running"  // loaded on a server
	MatchFinished = "finished" // ended normally; counts for stats and ELO
	MatchAborted  = "aborted"  // ended early; counts for nothing
	MatchFailed   = "failed"   // never started
)

type Match struct {
	ID, LobbyID, LobbyName, ServerID, Map string
	SettingsJSON, Token, ConfigJSON       string
	Status                                string
	Team1Score, Team2Score, Winner        int
	StartedAt, EndedAt                    int64
}

type RosterEntry struct {
	SteamID string
	Team    int
}

// RosterRow is a match_players row.
type RosterRow struct {
	SteamID                                           string
	Team                                              int
	Kills, Deaths, Assists, Damage, Headshots, Rounds int
	EloBefore, EloAfter                               int
}

const matchCols = `id, lobby_id, lobby_name, server_id, map, settings_json, token, config_json, status,
	team1_score, team2_score, winner, started_at, ended_at`

func scanMatch(row interface{ Scan(...any) error }) (Match, error) {
	var m Match
	err := row.Scan(&m.ID, &m.LobbyID, &m.LobbyName, &m.ServerID, &m.Map, &m.SettingsJSON, &m.Token, &m.ConfigJSON,
		&m.Status, &m.Team1Score, &m.Team2Score, &m.Winner, &m.StartedAt, &m.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Match{}, ErrNotFound
	}
	return m, err
}

// CreateMatch inserts a pending match and its roster, snapshotting each player's rating.
func (s *Store) CreateMatch(ctx context.Context, m Match, roster []RosterEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO matches
		(id, lobby_id, lobby_name, server_id, map, settings_json, token, config_json, status, started_at)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)`,
		m.ID, m.LobbyID, m.LobbyName, m.Map, m.SettingsJSON, m.Token, m.ConfigJSON, MatchPending, m.StartedAt); err != nil {
		return err
	}
	for _, r := range roster {
		if _, err := tx.ExecContext(ctx, `INSERT INTO match_players (match_id, steamid, team, elo_before, elo_after)
			VALUES (?, ?, ?, COALESCE((SELECT elo FROM players WHERE steamid = ?), 1000),
			        COALESCE((SELECT elo FROM players WHERE steamid = ?), 1000))`,
			m.ID, r.SteamID, r.Team, r.SteamID, r.SteamID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetMatch(ctx context.Context, id string) (Match, error) {
	return scanMatch(s.db.QueryRowContext(ctx, `SELECT `+matchCols+` FROM matches WHERE id = ?`, id))
}

// Roster returns the match's players in a stable order (team, then steamid).
func (s *Store) Roster(ctx context.Context, matchID string) ([]RosterRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT steamid, team, kills, deaths, assists, damage, headshots, rounds,
		elo_before, elo_after FROM match_players WHERE match_id = ? ORDER BY team, steamid`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RosterRow
	for rows.Next() {
		var r RosterRow
		if err := rows.Scan(&r.SteamID, &r.Team, &r.Kills, &r.Deaths, &r.Assists, &r.Damage, &r.Headshots, &r.Rounds,
			&r.EloBefore, &r.EloAfter); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetMatchRunning(ctx context.Context, id, serverID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE matches SET status = ?, server_id = ? WHERE id = ?`, MatchRunning, serverID, id)
	return err
}

// SetMatchStatus ends a match as failed or aborted.
func (s *Store) SetMatchStatus(ctx context.Context, id, status string, endedAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE matches SET status = ?, ended_at = ? WHERE id = ?`, status, endedAt, id)
	return err
}

// RunningMatchOnServer returns the running match on a server, or ErrNotFound.
func (s *Store) RunningMatchOnServer(ctx context.Context, serverID string) (Match, error) {
	return scanMatch(s.db.QueryRowContext(ctx, `SELECT `+matchCols+` FROM matches
		WHERE server_id = ? AND status = ? ORDER BY started_at DESC LIMIT 1`, serverID, MatchRunning))
}

// ActiveMatchOfLobby returns the lobby's pending or running match, or ErrNotFound.
func (s *Store) ActiveMatchOfLobby(ctx context.Context, lobbyID string) (Match, error) {
	return scanMatch(s.db.QueryRowContext(ctx, `SELECT `+matchCols+` FROM matches
		WHERE lobby_id = ? AND status IN (?, ?) ORDER BY started_at DESC LIMIT 1`, lobbyID, MatchPending, MatchRunning))
}
