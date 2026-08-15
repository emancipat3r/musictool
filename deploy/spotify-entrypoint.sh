#!/bin/sh
# Entrypoint for container 2: start busybox crond (hourly sync + weekly batch)
# and then run the server in the foreground. All logs go to stderr; the server
# never writes to stdout (MCP transport hygiene).
set -eu

# Per-provider credential guard: the engine serves one provider per deployment
# (MUSIC_PROVIDER, default spotify).
case "${MUSIC_PROVIDER:-spotify}" in
  spotify) : "${SPOTIFY_CLIENT_ID:?SPOTIFY_CLIENT_ID must be set (MUSIC_PROVIDER=spotify)}" ;;
  tidal)   : "${TIDAL_CLIENT_ID:?TIDAL_CLIENT_ID must be set (MUSIC_PROVIDER=tidal)}" ;;
  *) echo "unknown MUSIC_PROVIDER '${MUSIC_PROVIDER}' (want spotify or tidal)" >&2; exit 1 ;;
esac

# Shared self-signed TLS pair on the state volume (also used by zellij web in
# the sandbox). HTTPS is required for browser clipboard access in the terminal.
CERT_DIR=/data/zellij-certs
if [ ! -f "${CERT_DIR}/cert.pem" ] || [ ! -f "${CERT_DIR}/key.pem" ]; then
  echo "generating self-signed TLS pair at ${CERT_DIR}" >&2
  mkdir -p "${CERT_DIR}"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -keyout "${CERT_DIR}/key.pem" -out "${CERT_DIR}/cert.pem" \
    -days 3650 -nodes -subj "/CN=spotifytool" >/dev/null 2>&1
fi

# One initial sync on boot if a refresh token is present, so the dashboard has
# data immediately. Non-fatal if it fails (e.g. token not yet minted).
if [ -n "${SPOTIFY_REFRESH_TOKEN:-}" ] || [ -n "${TIDAL_REFRESH_TOKEN:-}" ]; then
  echo "boot: initial sync…" >&2
  spotifytool sync >/dev/null 2>>/data/sync.log || echo "boot sync failed (see /data/sync.log)" >&2
fi

# Launch cron in the background (busybox crond, foreground -f to a subshell).
crond -b -l 8

exec spotifytool serve
