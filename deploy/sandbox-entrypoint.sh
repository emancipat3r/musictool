#!/usr/bin/env bash
# Entrypoint for container 1 (sandbox). Creates a persistent named Zellij session
# ("claude") so there is always an attachable target, then starts Zellij's
# built-in web client over HTTPS. Access is via Tailscale/LAN only — never the
# public internet.
set -euo pipefail

SESSION="${ZELLIJ_SESSION:-claude}"
WEB_PORT="${ZELLIJ_WEB_PORT:-8082}"
WEB_HOST="${ZELLIJ_WEB_HOST:-0.0.0.0}"     # bound to the compose/LAN interface
CERT_DIR="${ZELLIJ_CERT_DIR:-/certs}"       # mount a `tailscale cert` here

# 1) Ensure the named session exists (detached) so attach always works.
if ! zellij list-sessions 2>/dev/null | grep -q "^${SESSION}"; then
  echo "creating detached zellij session '${SESSION}'" >&2
  # Start Claude Code as the session's first command in the working tree.
  zellij --session "${SESSION}" options --default-shell bash >/dev/null 2>&1 || true
  setsid zellij --session "${SESSION}" --new-session-with-layout default \
    >/tmp/zellij-session.log 2>&1 < /dev/null &
  sleep 2
fi

# 2) Zellij web client (off by default upstream; explicitly enabled here).
#    HTTPS is required; point it at a tailscale cert for the tailnet hostname
#    (valid LE cert, zero public exposure) or a private CA. Login token auth is
#    on top of the tailnet perimeter.
ARGS=(web -d --port "${WEB_PORT}")
if [[ -f "${CERT_DIR}/cert.pem" && -f "${CERT_DIR}/key.pem" ]]; then
  ARGS+=(--cert "${CERT_DIR}/cert.pem" --key "${CERT_DIR}/key.pem")
else
  echo "WARNING: no cert at ${CERT_DIR}; Zellij web requires HTTPS. Provide a" >&2
  echo "         'tailscale cert' (cert.pem/key.pem) mounted at ${CERT_DIR}." >&2
fi

echo "starting zellij web on https://${WEB_HOST}:${WEB_PORT} (session: ${SESSION})" >&2
exec zellij "${ARGS[@]}"
