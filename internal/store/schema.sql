CREATE TABLE IF NOT EXISTS players (
    steamid TEXT PRIMARY KEY,
    name    TEXT NOT NULL,
    avatar  TEXT NOT NULL DEFAULT '',
    elo     INTEGER NOT NULL DEFAULT 1000
);

CREATE TABLE IF NOT EXISTS matches (
    id            TEXT PRIMARY KEY,
    lobby_id      TEXT NOT NULL DEFAULT '',
    lobby_name    TEXT NOT NULL,
    server_id     TEXT NOT NULL DEFAULT '',
    map           TEXT NOT NULL,
    settings_json TEXT NOT NULL,
    token         TEXT NOT NULL DEFAULT '',
    config_json   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL,
    team1_score   INTEGER NOT NULL DEFAULT 0,
    team2_score   INTEGER NOT NULL DEFAULT 0,
    winner        INTEGER NOT NULL DEFAULT 0,
    started_at    INTEGER NOT NULL,
    running_at    INTEGER NOT NULL DEFAULT 0,
    ended_at      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS match_players (
    match_id   TEXT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    steamid    TEXT NOT NULL,
    team       INTEGER NOT NULL,
    kills      INTEGER NOT NULL DEFAULT 0,
    deaths     INTEGER NOT NULL DEFAULT 0,
    assists    INTEGER NOT NULL DEFAULT 0,
    damage     INTEGER NOT NULL DEFAULT 0,
    headshots  INTEGER NOT NULL DEFAULT 0,
    rounds     INTEGER NOT NULL DEFAULT 0,
    elo_before INTEGER NOT NULL DEFAULT 0,
    elo_after  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (match_id, steamid)
);
