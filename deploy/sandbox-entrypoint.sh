#!/usr/bin/env bash
# Entrypoint for container 1 (sandbox). Verified against zellij 0.43.1:
#   - `zellij attach <name> --create-background` creates a detached session
#     headlessly (no TTY needed).
#   - The web client is served by the zellij SERVER, driven by config keys
#     (web_server / web_sharing / web_server_ip / web_server_port /
#     web_server_cert / web_server_key). Running `zellij web` as a second
#     starter causes "Address in use" crash loops; do not.
#   - `zellij web --create-token` mints the login token (shown once).
# Access is via Tailscale/LAN only; never the public internet.
set -euo pipefail

SESSION="${ZELLIJ_SESSION:-claude}"
WEB_PORT="${ZELLIJ_WEB_PORT:-8082}"
CERT_DIR="${ZELLIJ_CERT_DIR:-/certs}"      # optional ro mount of cert.pem/key.pem
GEN_DIR="/data/zellij-certs"                # fallback: self-signed, persisted on the shared volume
ZDIR=/root/.config/zellij

mkdir -p "${ZDIR}/layouts"

# Pick certs: mounted ones win; otherwise generate a self-signed pair once and
# persist it so the browser warning is a one-time accept, not a per-boot one.
if [[ -f "${CERT_DIR}/cert.pem" && -f "${CERT_DIR}/key.pem" ]]; then
  CERT="${CERT_DIR}/cert.pem"; KEY="${CERT_DIR}/key.pem"
  echo "using mounted TLS certs from ${CERT_DIR}" >&2
else
  CERT="${GEN_DIR}/cert.pem"; KEY="${GEN_DIR}/key.pem"
  if [[ ! -f "$CERT" || ! -f "$KEY" ]]; then
    echo "no mounted certs; generating self-signed pair at ${GEN_DIR}" >&2
    mkdir -p "${GEN_DIR}"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
      -keyout "$KEY" -out "$CERT" -days 3650 -nodes \
      -subj "/CN=spotifytool-sandbox" >/dev/null 2>&1
  fi
fi

# Layout: first pane runs Claude Code in the mounted repo (/workspace), which
# carries .mcp.json (spotify MCP wiring) and CLAUDE.md (workflow doc).
# Owner's defaults: permission prompts off (contained, LAN-only sandbox; needs
# IS_SANDBOX=1 since we run as root) and Remote Control on, so the session is
# drivable from the Claude mobile app as "spotifytool".
if [[ ! -f "${ZDIR}/layouts/claude.kdl" ]]; then
  cat > "${ZDIR}/layouts/claude.kdl" <<'KDL'
layout {
    pane {
        command "claude"
        args "--allow-dangerously-skip-permissions" "--dangerously-skip-permissions" "--remote-control" "spotifytool"
        cwd "/workspace"
    }
    pane size=1 borderless=true {
        plugin location="zellij:status-bar"
    }
}
KDL
fi

# Config drives the web server; the zellij server starts it with the session.
cat > "${ZDIR}/config.kdl" <<EOF
default_layout "claude"
web_server true
web_sharing "on"
web_server_ip "0.0.0.0"
web_server_port ${WEB_PORT}
web_server_cert "${CERT}"
web_server_key "${KEY}"
EOF

ensure_session() {
  if ! zellij list-sessions --short 2>/dev/null | grep -qx "${SESSION}"; then
    echo "creating detached zellij session '${SESSION}'" >&2
    zellij attach "${SESSION}" --create-background >/dev/null 2>&1 || true
  fi
}
ensure_session

# Login token: mint one on first boot and print it. It is shown once and
# cannot be retrieved later; grab it from `docker logs` and keep it. Extra
# tokens can be minted any time with: zellij web --create-token
if ! zellij web --list-tokens 2>/dev/null | grep -q token_; then
  echo "=== zellij web login token (save this; shown once) ===" >&2
  zellij web --create-token >&2 || true
  echo "=======================================================" >&2
fi

echo "zellij web serving on https://0.0.0.0:${WEB_PORT} (session: ${SESSION})" >&2

# PID1 watchdog: keep the container up, recreate the session if it dies.
while true; do
  sleep 30
  ensure_session
done
