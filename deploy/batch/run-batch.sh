#!/bin/sh
# Weekly discovery batch wrapper (container 2, cron-driven).
#
# This is the ONLY place the Agent SDK monthly credit is spent. It runs a
# headless `claude -p` session that: loads the taste profile + recent signals,
# researches in-lane novelty (web + Last.fm + scene sources), curates ~10
# tracks, resolves them to exact URIs, creates "Discovery W##", verifies via
# read-back, and writes a digest.
#
# Requirements in this container: the `claude` CLI on PATH, logged in (or an
# API key), the Agent SDK monthly credit opted-in, and the spotify MCP server
# registered (see .mcp.json). Keep overflow/usage-credits DISABLED so a spent
# credit pauses batches rather than incurring cost.
set -eu

WEEK="$(date +%V)"
PROMPT_FILE="/app/batch/batch-prompt.md"

if ! command -v claude >/dev/null 2>&1; then
  echo "$(date -u +%FT%TZ) claude CLI not found; skipping batch" >&2
  exit 0
fi

echo "$(date -u +%FT%TZ) starting discovery batch for week W${WEEK}" >&2

# --print runs unattended. MCP wiring comes from mcp-batch.json (the server is
# in this same container, so the URL is loopback, not the compose hostname).
# The playlist name convention is enforced in the prompt (Discovery W##).
WEEK="${WEEK}" claude -p "$(sed "s/{{WEEK}}/${WEEK}/g" "${PROMPT_FILE}")" \
  --mcp-config /app/batch/mcp-batch.json \
  --permission-mode acceptEdits \
  --allowedTools "mcp__spotify__*" \
  || { echo "$(date -u +%FT%TZ) batch run failed" >&2; exit 1; }

echo "$(date -u +%FT%TZ) discovery batch W${WEEK} complete" >&2
