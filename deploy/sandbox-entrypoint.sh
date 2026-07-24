#!/usr/bin/env bash
# Entrypoint for container 1 (sandbox). Ensures a persistent named Zellij
# session ("claude") exists with Claude Code running in it, then serves Zellij's
# built-in web client over HTTPS in the foreground. Access is via Tailscale/LAN
# only — never the public internet.
set -euo pipefail

SESSION="${ZELLIJ_SESSION:-claude}"
WEB_PORT="${ZELLIJ_WEB_PORT:-8082}"
CERT_DIR="${ZELLIJ_CERT_DIR:-/certs}"       # mount `tailscale cert` output here
ZDIR=/root/.config/zellij

mkdir -p "${ZDIR}/layouts"

# Layout: first pane runs Claude Code in the mounted repo (/workspace), which
# carries .mcp.json (spotify MCP wiring) and CLAUDE.md (workflow doc).
if [[ ! -f "${ZDIR}/layouts/claude.kdl" ]]; then
  cat > "${ZDIR}/layouts/claude.kdl" <<'KDL'
layout {
    pane {
        command "claude"
        cwd "/workspace"
    }
    pane size=1 borderless=true {
        plugin location="zellij:status-bar"
    }
}
KDL
fi

# Config: default layout + web client enabled + TLS cert paths. Zellij web
# requires HTTPS; a `tailscale cert` for the tailnet hostname gives a valid LE
# cert with zero public exposure. Note: Zellij's CLI attach rejects self-signed
# certs by default, so prefer the tailscale cert over a private CA.
{
  echo "default_layout \"claude\""
  echo "web_server true"
  if [[ -f "${CERT_DIR}/cert.pem" && -f "${CERT_DIR}/key.pem" ]]; then
    echo "web_server_cert \"${CERT_DIR}/cert.pem\""
    echo "web_server_key \"${CERT_DIR}/key.pem\""
  else
    echo "WARNING: no cert at ${CERT_DIR}; Zellij web requires HTTPS." >&2
    echo "         Mount 'tailscale cert' output (cert.pem/key.pem) there." >&2
  fi
} > "${ZDIR}/config.kdl"

# Ensure the named session exists detached, so there is always an attachable
# target for the web client, `zellij attach` over Tailscale SSH, and the mobile
# app. --create-background creates it without needing a TTY; the `script` PTY
# wrapper is a fallback for zellij builds that lack the flag.
if ! zellij list-sessions --short 2>/dev/null | grep -qx "${SESSION}"; then
  echo "creating detached zellij session '${SESSION}'" >&2
  zellij attach "${SESSION}" --create-background >/dev/null 2>&1 || \
    (setsid script -qec "zellij --session '${SESSION}'" /dev/null >/dev/null 2>&1 &)
  sleep 2
fi

echo "serving zellij web on https://0.0.0.0:${WEB_PORT} (session: ${SESSION})" >&2
echo "first run: check 'zellij web --help' output in logs for the login token" >&2
exec zellij web --port "${WEB_PORT}"
