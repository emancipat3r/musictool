#!/bin/sh
# Entrypoint for container 2: start busybox crond (hourly sync + weekly batch)
# and then run the server in the foreground. All logs go to stderr; the server
# never writes to stdout (MCP transport hygiene).
set -eu

: "${SPOTIFY_CLIENT_ID:?SPOTIFY_CLIENT_ID must be set}"

# One initial sync on boot if a refresh token is present, so the dashboard has
# data immediately. Non-fatal if it fails (e.g. token not yet minted).
if [ -n "${SPOTIFY_REFRESH_TOKEN:-}" ]; then
  echo "boot: initial sync…" >&2
  spotifytool sync >/dev/null 2>>/data/sync.log || echo "boot sync failed (see /data/sync.log)" >&2
fi

# Launch cron in the background (busybox crond, foreground -f to a subshell).
crond -b -l 8

exec spotifytool serve
