package store

import (
	"context"
	"database/sql"
	"errors"
)

// LeaderRow is one player's totals over all completed matches.
type LeaderRow struct {
	SteamID, Name                                     string
	Elo, Matches, Wins                                int
	Kills, Deaths, Assists, Damage, Headshots, Rounds int
}

func (r LeaderRow) KD() float64 {
	if r.Deaths == 0 {
		return float64(r.Kills)
	}
	return float64(r.Kills) / float64(r.Deaths)
}

func (r LeaderRow) ADR() float64 {
	if r.Rounds == 0 {
		return 0
	}
	return float64(r.Damage) / float64(r.Rounds)
}

func (r LeaderRow) HSPercent() float64 {
	if r.Kills == 0 {
		return 0
	}
	return 100 * float64(r.Headshots) / float64(r.Kills)
}

const leaderSelect = `SELECT p.steamid, p.name, p.elo,
	COUNT(mp.match_id),
	COALESCE(SUM(CASE WHEN m.winner = mp.team THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(mp.kills), 0), COALESCE(SUM(mp.deaths), 0), COALESCE(SUM(mp.assists), 0),
	COALESCE(SUM(mp.damage), 0), COALESCE(SUM(mp.headshots), 0), COALESCE(SUM(mp.rounds), 0)
	FROM players p
	LEFT JOIN match_players mp ON mp.steamid = p.steamid
	LEFT JOIN matches m ON m.id = mp.match_id AND m.status = 'finished'`

// Note: the LEFT JOINs above would count players' unfinished matches in COUNT
// and SUMs, so every query below restricts to finished matches in its WHERE.

func scanLeader(row interface{ Scan(...any) error }) (LeaderRow, error) {
	var r LeaderRow
	err := row.Scan(&r.SteamID, &r.Name, &r.Elo, &r.Matches, &r.Wins,
		&r.Kills, &r.Deaths, &r.Assists, &r.Damage, &r.Headshots, &r.Rounds)
	return r, err
}

// Leaderboard returns every player with at least one completed match, best rating first.
func (s *Store) Leaderboard(ctx context.Context) ([]LeaderRow, error) {
	rows, err := s.db.QueryContext(ctx, leaderSelect+`
		WHERE m.id IS NOT NULL GROUP BY p.steamid ORDER BY p.elo DESC, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaderRow
	for rows.Next() {
		r, err := scanLeader(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type MatchRow struct {
	ID, LobbyName, Map, Status     string
	Team1Score, Team2Score, Winner int
	MyTeam, EloDelta               int
	EndedAt                        int64
}

type PlayerSummary struct {
	LeaderRow
	Recent []MatchRow
}

// PlayerSummary returns a player's totals and recent completed matches.
func (s *Store) PlayerSummary(ctx context.Context, steamID string) (PlayerSummary, error) {
	p, err := s.GetPlayer(ctx, steamID)
	if err != nil {
		return PlayerSummary{}, err
	}
	sum := PlayerSummary{LeaderRow: LeaderRow{SteamID: p.SteamID, Name: p.Name, Elo: p.Elo}}
	row := s.db.QueryRowContext(ctx, leaderSelect+` WHERE p.steamid = ? AND m.id IS NOT NULL GROUP BY p.steamid`, steamID)
	if r, err := scanLeader(row); err == nil {
		sum.LeaderRow = r
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PlayerSummary{}, err
	}
	sum.Recent, err = s.MatchesOf(ctx, steamID, 10)
	return sum, err
}

// MatchesOf returns a player's completed matches, newest first.
func (s *Store) MatchesOf(ctx context.Context, steamID string, limit int) ([]MatchRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.lobby_name, m.map, m.status, m.team1_score, m.team2_score,
		m.winner, mp.team, mp.elo_after - mp.elo_before, m.ended_at
		FROM match_players mp JOIN matches m ON m.id = mp.match_id
		WHERE mp.steamid = ? AND m.status = 'finished' ORDER BY m.ended_at DESC, m.id DESC LIMIT ?`, steamID, limit)
	if err != nil {
		return nil, err
	}
	return scanMatchRows(rows)
}

// RecentMatches returns the latest matches of any status (for the admin page).
func (s *Store) RecentMatches(ctx context.Context, limit int) ([]MatchRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.lobby_name, m.map, m.status, m.team1_score, m.team2_score,
		m.winner, 0, 0, m.ended_at FROM matches m ORDER BY m.started_at DESC, m.ended_at DESC, m.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanMatchRows(rows)
}

func scanMatchRows(rows *sql.Rows) ([]MatchRow, error) {
	defer rows.Close()
	var out []MatchRow
	for rows.Next() {
		var r MatchRow
		if err := rows.Scan(&r.ID, &r.LobbyName, &r.Map, &r.Status, &r.Team1Score, &r.Team2Score,
			&r.Winner, &r.MyTeam, &r.EloDelta, &r.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EloDelta is the rating change this match caused for the player.
func (r RosterRow) EloDelta() int { return r.EloAfter - r.EloBefore }

type DetailRow struct {
	RosterRow
	Name string
}

type MatchDetail struct {
	Match
	Players []DetailRow
}

// MatchDetail returns a match with its players (names included) for the scoreboard.
func (s *Store) MatchDetail(ctx context.Context, id string) (MatchDetail, error) {
	m, err := s.GetMatch(ctx, id)
	if err != nil {
		return MatchDetail{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT mp.steamid, COALESCE(p.name, mp.steamid), mp.team, mp.kills, mp.deaths,
		mp.assists, mp.damage, mp.headshots, mp.rounds, mp.elo_before, mp.elo_after
		FROM match_players mp LEFT JOIN players p ON p.steamid = mp.steamid
		WHERE mp.match_id = ? ORDER BY mp.team, mp.kills DESC, mp.steamid`, id)
	if err != nil {
		return MatchDetail{}, err
	}
	defer rows.Close()
	d := MatchDetail{Match: m}
	for rows.Next() {
		var r DetailRow
		if err := rows.Scan(&r.SteamID, &r.Name, &r.Team, &r.Kills, &r.Deaths, &r.Assists, &r.Damage,
			&r.Headshots, &r.Rounds, &r.EloBefore, &r.EloAfter); err != nil {
			return MatchDetail{}, err
		}
		d.Players = append(d.Players, r)
	}
	return d, rows.Err()
}

// DeleteMatch removes a match and its player rows. Ratings are not reverted.
func (s *Store) DeleteMatch(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM matches WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
