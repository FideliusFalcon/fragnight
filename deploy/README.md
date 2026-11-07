# Deploying the CS2 LAN lobby

**Run this on a Linux host** (for example pippin). The CS2 dedicated server is x86 Linux and will not run properly on an Apple Silicon Mac.

## CS2 servers

Image approach: the community image `joedwards32/cs2` plus a mounted `cs2/pre.sh` start hook that installs Metamod, CounterStrikeSharp and MatchZy (latest releases) and writes MatchZy's webhook config from environment variables. **This setup is unverified: it has not been run yet.** Things to confirm on first run:

- the image really executes `/etc/pre.sh` before starting the server (check the image README for its pre-start hook path);
- `meta list` over RCON shows CounterStrikeSharp and MatchZy loaded;
- `gameinfo.gi` keeps the Metamod line after Steam updates (the script re-adds it on every start).

Each server has its own volume, so each downloads about 60 GB once. Sharing a single read-only install between containers was not attempted because CS2 writes into its install directory; if you want to save disk, bind the same volume read-only plus an overlay and verify the server still starts.

## Running the stack

```
cd deploy && cp .env.example .env     # then fill in the values (see below)
docker compose up -d --build
```

The lobby is then at `BASE_URL` (port 8080). Players log in with Steam, create or join a lobby, pick teams, vote on a map, and get a `connect …; password …` line to paste into the CS2 console.

**Login needs internet.** Steam OpenID is verified by the lobby server calling `steamcommunity.com`, and players' browsers are sent there to sign in. A LAN with no internet cannot log in with Steam. For an offline test set `DEV_LOGIN=1` and use `/login`'s developer form (never leave it on in a shared setup: anyone can sign in as any SteamID).

## Environment (`deploy/.env`)

| Variable | Needed | Meaning |
|---|---|---|
| `BASE_URL` | yes | Address players use to reach the lobby, e.g. `http://192.168.1.10:8080`. Used as the Steam login realm. |
| `LOBBY_PORT` | no | Host port the lobby is published on. Default `8080`; `BASE_URL` must use the same port. |
| `SESSION_SECRET` | yes | Long random string that signs login cookies. |
| `WEBHOOK_SECRET` | yes | Shared with the CS2 servers; they send it with every MatchZy event. |
| `RCON_PASSWORD_1`, `RCON_PASSWORD_2` | yes | RCON password of each CS2 server (must match `servers.json`). |
| `ADMIN_STEAMIDS` | no | Comma-separated SteamID64s allowed to use `/admin` and abort any match. |
| `STEAM_API_KEY` | no | Only for nicer names and avatars; the app works without it. |
| `DEV_LOGIN` | no | `1` enables the developer login. Default `0`. |

`servers.json` must list the same servers as the compose file: `id` (matches the service name, e.g. `cs2-1`), display `name`, the LAN `host` players connect to, the game `port` and the `rcon_password`.

## Adding a CS2 server

1. Copy a `cs2-N` block in `docker-compose.yml`: new service name, volume, `CS2_PORT`, published port, `MATCHZY_LOG_URL` ending in the new id, and a `RCON_PASSWORD_N`.
2. Add the matching entry to `servers.json`.
3. `docker compose up -d`.

## Not verified yet

The pieces below could not be tested without a Linux host running CS2. Check them on the first real run and note what you find here:

- CS2 containers start, install the plugins and load MatchZy (see "CS2 servers" above).
- MatchZy accepts `matchzy_loadmatch_url`, fetches the config from the lobby and uses the field names the app sends (`internal/matchzy/config.go`).
- MatchZy's real webhook payloads match the parser (`internal/matchzy/event.go`); fixtures are hand-written until replaced (see below). Run the tests with `STRICT_FIXTURES=1` once real captures are in.
- The admin "Abort match" action relies on the console command `get5_endmatch`.
- A full end-to-end match: lobby, vote, connect, knife round, finish (use a short `MR` in Settings for testing), then check that the score updated live, the scoreboard shows stats and rating changes, and the server returned to the pool.

## MatchZy payload fixtures

`internal/matchzy/testdata/*.json` are currently **hand-written** in Get5's event style and marked `"_unverified": true`. To capture real ones: point `matchzy_remote_log_url` at a throwaway listener (for example `nc -l 9999`), load a minimal match config, play or bot a short match, and replace each fixture with a real body (drop the `_unverified` key). MatchZy version captured against: _not yet recorded_.
