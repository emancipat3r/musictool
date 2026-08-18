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
# Belt-and-braces with the Dockerfile ENV: the zellij server snapshots this
# environment at session creation, and panes inherit it. Without TERM, every
# TUI in the session renders monochrome.
export TERM="${TERM:-xterm-256color}" COLORTERM="${COLORTERM:-truecolor}"
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
      -subj "/CN=musictool-sandbox" >/dev/null 2>&1
  fi
fi

# Layout: first pane runs Claude Code in the mounted repo (/workspace), which
# carries .mcp.json (spotify MCP wiring) and CLAUDE.md (workflow doc).
# Owner's defaults: permission prompts off (contained, LAN-only sandbox; needs
# IS_SANDBOX=1 since we run as root) and Remote Control on, so the session is
# drivable from the Claude mobile app as "musictool".
if [[ ! -f "${ZDIR}/layouts/claude.kdl" ]]; then
  cat > "${ZDIR}/layouts/claude.kdl" <<'KDL'
layout {
    pane {
        command "claude"
        args "--allow-dangerously-skip-permissions" "--dangerously-skip-permissions" "--remote-control" "musictool"
        cwd "/workspace"
    }
    pane size=1 borderless=true {
        plugin location="zellij:status-bar"
    }
}
KDL
fi

# Config drives the web server; the zellij server starts it with the session.
# default_mode "locked": zellij passes ALL keystrokes through to the pane
# (Claude Code's Ctrl+O expand, Ctrl+P, etc. work), except Ctrl+G which
# toggles zellij's own controls when you actually want panes/tabs/scroll.
cat > "${ZDIR}/config.kdl" <<EOF
default_layout "claude"
default_mode "locked"
// No release-notes popup: it spawns as a FOCUSED floating plugin pane, so CLI
// injection (trigger-server write-chars) types into the popup and is silently
// swallowed. It is also the clipped popup seen on phone-width terminals.
show_release_notes false
web_server true
web_sharing "on"
web_server_ip "0.0.0.0"
web_server_port ${WEB_PORT}
web_server_cert "${CERT}"
web_server_key "${KEY}"
EOF

# A session can exist as a DEAD ghost: session serialization lives in
# ~/.local/share/zellij, which is volume-mounted for token persistence, so
# exited sessions survive container rebuilds and list-sessions --short shows
# them indistinguishably from live ones. Attaching to a ghost lands on the
# resurrection screen instead of Claude. Detect EXITED and recreate fresh.
ensure_session() {
  if zellij list-sessions --no-formatting 2>/dev/null | grep -E "^${SESSION} " | grep -q "EXITED"; then
    echo "session '${SESSION}' is dead; deleting and recreating" >&2
    zellij delete-session "${SESSION}" --force >/dev/null 2>&1 || true
  fi
  if ! zellij list-sessions --short 2>/dev/null | grep -qx "${SESSION}"; then
    echo "creating detached zellij session '${SESSION}'" >&2
    zellij attach "${SESSION}" --create-background >/dev/null 2>&1 || true
  fi
}
ensure_session

# Login token: mint one and capture it to the shared volume so it is always
# retrievable (dashboard terminal pane displays it; `cat` works too). Zellij
# only shows a token at mint time, so the capture file is the source of truth.
# The token store (~/.local/share/zellij) is volume-mounted in compose, so
# minted tokens and browser remember-me cookies survive rebuilds.
TOKEN_FILE=/data/zellij-web-token.txt
if [[ ! -s "$TOKEN_FILE" ]] || ! zellij web --list-tokens 2>/dev/null | grep -q token_; then
  token=$(zellij web --create-token 2>/dev/null | grep -oE '[0-9a-f]{8}-[0-9a-f-]{27}' | head -1 || true)
  if [[ -n "$token" ]]; then
    (umask 077 && printf '%s\n' "$token" > "$TOKEN_FILE")
    echo "zellij web login token minted; stored at ${TOKEN_FILE}" >&2
  else
    echo "WARNING: could not mint/capture a zellij web token" >&2
  fi
fi

# Trigger endpoint (compose-network only): lets the dashboard's "Run discovery
# batch" button type the allowlisted slash command into the live session.
node /usr/local/bin/trigger-server.js &

echo "zellij web serving on https://0.0.0.0:${WEB_PORT} (session: ${SESSION})" >&2

# PID1 watchdog: keep the container up, recreate the session if it dies.
while true; do
  sleep 30
  ensure_session
done
