# Deploying the CS2 LAN lobby

**Run this on a Linux host** (for example pippin). The CS2 dedicated server is x86 Linux and will not run properly on an Apple Silicon Mac.

## CS2 servers

Image approach: the community image `joedwards32/cs2` plus a mounted `cs2/pre.sh` start hook that installs Metamod, CounterStrikeSharp and MatchZy (latest releases) and writes MatchZy's webhook config from environment variables. **This setup is unverified: it has not been run yet.** Things to confirm on first run:

- the image really executes `/etc/pre.sh` before starting the server (check the image README for its pre-start hook path);
- `meta list` over RCON shows CounterStrikeSharp and MatchZy loaded;
- `gameinfo.gi` keeps the Metamod line after Steam updates (the script re-adds it on every start).

Each server has its own volume, so each downloads about 60 GB once. Sharing a single read-only install between containers was not attempted because CS2 writes into its install directory; if you want to save disk, bind the same volume read-only plus an overlay and verify the server still starts.

## Environment (`deploy/.env`)

```
WEBHOOK_SECRET=<random string shared with the lobby app>
RCON_PASSWORD_1=<random>
RCON_PASSWORD_2=<random>
```

`servers.json` must list the same servers: `id`, display `name`, the LAN `host` players connect to, the game `port` and the RCON password.

## MatchZy payload fixtures

`internal/matchzy/testdata/*.json` are currently **hand-written** in Get5's event style and marked `"_unverified": true`. To capture real ones: point `matchzy_remote_log_url` at a throwaway listener (for example `nc -l 9999`), load a minimal match config, play or bot a short match, and replace each fixture with a real body (drop the `_unverified` key). MatchZy version captured against: _not yet recorded_.
