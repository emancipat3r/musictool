# spotifytool

Self-hosted, LAN-only AI music curation stack. Claude curates, Spotify plays,
playlists are the persistence layer between them. `spotifytool` is the junction:
a single static Go binary that does PKCE auth, syncs the library into SQLite,
resolves curated picks into exact track URIs deterministically, builds and
verifies playlists, and serves both an MCP tool surface (for Claude) and a
read-only dashboard.

## What it is

- **Claude** is the taste engine (curation, discovery, taste-profile upkeep).
- **Spotify** is the media provider and player.
- **Playlists** are the interface contract between them.
- **spotifytool** is the deterministic translator plus the store of listening
  metrics Spotify does not expose.

Spotify's generative playlist engine and connector are deliberately not used:
the connector cannot read the library, and the create endpoint substitutes
tracks. The deterministic resolver here is the core differentiator: exact URIs
with a read-back diff, no silent substitution.

## Architecture

Two containers, one shared volume, LAN-only (reached via Tailscale):

- **Container 1, `sandbox`**: Claude Code inside a named Zellij session, exposed
  to the browser via Zellij's built-in web client. This is the chat interface.
- **Container 2, `spotify`**: `spotifytool serve` (MCP over Streamable HTTP on
  the compose network) plus the read-only dashboard. Cron runs the hourly sync.
  Pure Go, no model invocations: all Claude usage lives in container 1's
  interactive session on the Max subscription.

Nothing is published to the public internet. See `deploy/docker-compose.yml`.

## Project layout

```
cmd/spotifytool/        CLI entrypoint + serve command
internal/
  auth/                 PKCE, loopback + OOB flows, token cache with rotation
  spotify/              raw HTTP client, library/search/playlist endpoints
  resolve/              normalization + deterministic scoring/bucketing
  store/                SQLite engine (single writer), schema, stats, signals
  service/              orchestration shared by CLI and MCP (sync, build-exact)
  mcp/                  hand-rolled Streamable HTTP JSON-RPC server + tools
  lastfm/               tags + similar-artists discovery seeds
  dashboard/            read-only web UI + taste-profile editor
  profile/              taste-profile.md read/write
  model/ config/ apperr/ logx/    shared types, paths, exit codes, logging
.claude/commands/       /discovery-batch (interactive weekly batch playbook)
deploy/                 Dockerfiles, compose, cron, entrypoints
```

## Quick start

Prerequisites: Go 1.26+, a Spotify Premium account, a registered Spotify app.

1. **Register the Spotify app** (one-time, human). Developer Dashboard, Create
   app, select Web API, set the redirect URI to exactly
   `http://127.0.0.1:8888/callback`, copy the Client ID (ignore the secret).

2. **Build and authorize.**
   ```sh
   make build
   export SPOTIFY_CLIENT_ID=<your-client-id>
   ./bin/spotifytool auth                       # opens a browser, one-shot loopback
   # headless machine? use the out-of-band flow:
   ./bin/spotifytool auth --no-listener
   # capture the refresh token for the container:
   ./bin/spotifytool auth --print-refresh-token
   ```

3. **Sync and inspect.**
   ```sh
   ./bin/spotifytool sync           # liked songs, playlists, plays, Keepers
   ./bin/spotifytool stats          # distilled library stats (JSON)
   ./bin/spotifytool signals        # recent feedback since last batch (JSON)
   ```

4. **Build an exact playlist** from a list of picks:
   ```sh
   echo '[{"artist":"Sublime","title":"Santeria"},
          {"artist":"Stick Figure","title":"Smoke Stack"}]' \
     | ./bin/spotifytool build --name "Test Build" --desc "resolver check"
   ```
   The result includes the read-back so you can diff intent vs result. Picks
   may carry an optional `duration_ms`; candidates within 3 seconds of it get a
   scoring tiebreak (studio cut vs live version disambiguation).

5. **Run the server** (MCP + dashboard):
   ```sh
   ./bin/spotifytool serve          # MCP :8080/mcp, dashboard :8081
   ```

6. **Deploy** the full two-container stack:
   ```sh
   cp .env.example .env             # fill in client id, refresh token, LAN_IP
   docker compose -f deploy/docker-compose.yml up -d --build
   ```
   All Claude usage is interactive and bills the Max subscription: log in once
   inside the sandbox session and you're set. There is no unattended model cron
   and no API-key or Agent SDK credit usage anywhere in the stack. The weekly
   discovery batch is the `/discovery-batch` command, run from the session
   (terminal, Zellij web, or mobile attach).

## CLI conventions

Data (JSON) goes to stdout; all logs go to stderr. In `serve` mode stdout is
silent. Exit codes: `0` ok, `1` auth, `2` API, `3` partial.

## MCP tools

`sync_library`, `get_library_stats`, `get_recent_signals`, `get_liked_songs`,
`get_playlists`, `read_playlist`, `search_tracks`, `resolve_tracklist`,
`create_playlist_exact`, `get_artist_tags`, `get_similar_artists`. All return
compact structured fields, never full Spotify objects.

## Security posture

- PKCE only, no client secret. Token cache is `0600`, never logged or committed.
- Only spotifytool writes SQLite. The dashboard is read-only except the taste
  profile editor.
- All binds are compose-network or LAN. Tailscale is the sole remote path. No
  port forwards, no public DNS.

## Milestone status

- M1 read core (auth, sync, liked songs): done.
- M2 write core (resolver, build, read-back verify): done.
- M3 serve + compose (MCP HTTP, two-container compose, Zellij web wiring): done,
  pending live verification on the homelab.
- M4 feedback layer (hourly sync cron, play history, Keepers, signals): done.
- M5 taste + batch (profile, weekly batch, dashboard v1): done; the batch is the
  interactive /discovery-batch command (subscription-billed, no unattended model
  cron by owner's choice), dashboard live.

Live Spotify verification (the human-in-the-loop steps: app registration, first
auth, confirming a test playlist lands with no substitutions) is required to
close M1/M2 against the real library.

## Development

```sh
make test        # unit tests (PKCE known-answer, normalization, resolver,
                 #             store round-trip, MCP protocol)
make vet
make dist        # cross-compile linux/amd64 + linux/arm64, static (CGO off)
```
