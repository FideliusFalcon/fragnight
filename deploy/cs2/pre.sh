#!/bin/bash
# Runs before the CS2 server starts (mounted into joedwards32/cs2 as its pre-start hook).
# Idempotently installs Metamod, CounterStrikeSharp and MatchZy and writes the MatchZy webhook config.
set -euo pipefail
GAME="${CS2_DIR:-/home/steam/cs2-dedicated}/game/csgo"
cd "$GAME"
tmp="$(mktemp -d)"

if [ ! -d addons/metamod ]; then
  mm_file="$(curl -fsSL https://mms.alliedmods.net/mmsdrop/2.0/mmsource-latest-linux)"
  curl -fsSL "https://mms.alliedmods.net/mmsdrop/2.0/${mm_file}" | tar -xz -C "$GAME"
fi

if [ ! -d addons/counterstrikesharp ]; then
  url="$(curl -fsSL https://api.github.com/repos/roflmuffin/CounterStrikeSharp/releases/latest \
    | jq -r '.assets[] | select(.name|test("with-runtime-linux")) | .browser_download_url' | head -1)"
  curl -fsSL "$url" -o "$tmp/css.zip" && unzip -oq "$tmp/css.zip" -d "$GAME"
fi

if [ ! -d addons/counterstrikesharp/plugins/MatchZy ]; then
  url="$(curl -fsSL https://api.github.com/repos/shobhit-pathak/MatchZy/releases/latest \
    | jq -r '.assets[] | select(.name|test("linux")) | select(.name|test("with-cssharp")|not) | .browser_download_url' | head -1)"
  curl -fsSL "$url" -o "$tmp/matchzy.zip" && unzip -oq "$tmp/matchzy.zip" -d "$GAME"
fi

# Steam updates overwrite gameinfo.gi; re-add the Metamod entry when missing.
if ! grep -q 'csgo/addons/metamod' gameinfo.gi; then
  sed -i '/Game_LowViolence/a\\t\t\tGame\tcsgo/addons/metamod' gameinfo.gi
fi

mkdir -p cfg/MatchZy
cat > cfg/MatchZy/config.cfg <<CFG
matchzy_remote_log_url "${MATCHZY_LOG_URL}"
matchzy_remote_log_header_key "X-Webhook-Secret"
matchzy_remote_log_header_value "${WEBHOOK_SECRET}"
CFG
rm -rf "$tmp"
