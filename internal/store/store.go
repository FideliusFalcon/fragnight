package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

type Player struct {
	SteamID   string `json:"steamid"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar"`
	Elo       int    `json:"elo"`
}

// Open opens (and migrates) the SQLite database at path. ":memory:" is allowed.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	} else {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps :memory: databases consistent and serializes writes.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate upgrades databases created by older versions. CREATE TABLE IF NOT
// EXISTS does not add columns, so they are added here when missing.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('matches')`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if !have["running_at"] {
		if _, err := db.Exec(`ALTER TABLE matches ADD COLUMN running_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// UpsertPlayer inserts a player or updates name/avatar, keeping the rating.
func (s *Store) UpsertPlayer(ctx context.Context, p Player) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO players (steamid, name, avatar) VALUES (?, ?, ?)
		ON CONFLICT(steamid) DO UPDATE SET name = excluded.name, avatar = excluded.avatar`,
		p.SteamID, p.Name, p.AvatarURL)
	return err
}

func (s *Store) GetPlayer(ctx context.Context, steamID string) (Player, error) {
	var p Player
	err := s.db.QueryRowContext(ctx,
		`SELECT steamid, name, avatar, elo FROM players WHERE steamid = ?`, steamID).
		Scan(&p.SteamID, &p.Name, &p.AvatarURL, &p.Elo)
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, ErrNotFound
	}
	return p, err
}
