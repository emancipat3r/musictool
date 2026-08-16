# musictool

Self-hosted, LAN-only AI music curation stack. Claude curates, your streaming
provider plays, playlists are the persistence layer between them. `musictool`
is the junction: a single static Go binary that does PKCE auth, syncs the
library into SQLite, resolves curated picks into exact track URIs
deterministically, builds and verifies playlists, and serves both an MCP tool
surface (for Claude) and a read-only dashboard.

Two providers are supported behind one interface: **Spotify** (default) and
**TIDAL**. One deployment curates against one provider, selected by
`MUSIC_PROVIDER`; the store is stamped with the provider and refuses to open
under the other.

> **Formerly `spotifytool`.** Renamed when the engine went multi-provider.
> Pre-rename deployments keep working without changes: legacy `SPOTIFYTOOL_*`
> environment variables are read as fallbacks for the `MUSICTOOL_*` names, and
> existing `spotifytool` config/data directories and `spotifytool.db` stay in
> use wherever they already exist.

## What it is

- **Claude** is the taste engine (curation, discovery, taste-profile upkeep).
- **The provider** (Spotify or TIDAL) is the media catalog and player.
- **Playlists** are the interface contract between them.
- **musictool** is the deterministic translator plus the store of listening
  metrics the provider APIs do not expose.

The providers' own generative playlist engines and connectors are deliberately
not used: they cannot read the library reliably, and their create paths
substitute tracks. The deterministic resolver here is the core differentiator:
exact URIs with a read-back diff, no silent substitution.

## Provider capability matrix

Both backends were live-verified against real accounts (Spotify 2026-07;
TIDAL 2026-08-14). The write path — resolve, create, add, read-back-verify,
remove, delete — is identical on both. They differ in the *feedback* channels:

| Capability                          | Spotify | TIDAL |
| ----------------------------------- | ------- | ----- |
| Search + deterministic resolver     | yes     | yes   |
| ISRC on every track                 | yes     | yes (plus first-class `filter[isrc]` lookup) |
| Exact playlist build + read-back    | yes     | yes   |
| Liked songs / collection sync       | yes     | yes   |
| Keepers / Disliked vote channels    | yes     | yes (they are just playlists) |
| Truly private playlists             | yes     | no — API playlists are `PUBLIC` or `UNLISTED` only |
| Play history (`recently_played`)    | yes     | **no** (no such endpoint) |
| Listen telemetry (skip/completion)  | yes     | **no** (no player-state endpoint) |

Practical consequences of the TIDAL gaps:

- The affinity model (`get_taste_deltas`) runs on **explicit signals only**:
  Keepers/Disliked votes and saves. Vote a little more deliberately.
- `ignored_from_last_batch` and skip detection are unavailable; the poller and
  the play-history sync stage disable themselves cleanly (capability flags).
- "Private" builds land as UNLISTED: not browsable or searchable, but anyone
  with the link can view.

TIDAL data-shape notes (normalized inside `internal/tidal`, invisible
upstream): popularity is a 0..1 float (scaled to 0..100), durations are
ISO-8601 (parsed to ms), performance variants live in a separate `version`
attribute (folded into the title so variant scoring matches Spotify), track
ids are numeric strings and playlist ids UUIDs (URIs are namespaced
`tidal:track:` / `tidal:playlist:` internally).

## Architecture

Two containers, one shared volume, LAN-only (reached via Tailscale):

- **Container 1, `sandbox`**: Claude Code inside a named Zellij session, exposed
  to the browser via Zellij's built-in web client. This is the chat interface.
- **Container 2, `engine`**: `musictool serve` (MCP over Streamable HTTP on
  the compose network) plus the read-only dashboard, talking to whichever
  provider `MUSIC_PROVIDER` selects. Cron runs the hourly sync. Pure Go, no
  model invocations: all Claude usage lives in container 1's interactive
  session on the Max subscription.

Nothing is published to the public internet. See `deploy/docker-compose.yml`.

## Project layout

```
cmd/musictool/        CLI entrypoint + serve command
internal/
  auth/                 PKCE, loopback + OOB flows, token cache with rotation
                        (provider-parameterized endpoints)
  provider/             the provider abstraction: interface + capability flags
  spotify/              Spotify Web API client (implements provider.Client)
  tidal/                TIDAL API v2 (JSON:API) client (implements provider.Client)
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

Prerequisites: Go 1.26+, a premium account on your provider, a registered
app for it (both are free, self-service registrations).

### Option A: Spotify (default)

1. **Register the app** (one-time, human). [Spotify Developer
   Dashboard](https://developer.spotify.com/dashboard), Create app, select Web
   API, set the redirect URI to exactly `http://127.0.0.1:8888/callback`, copy
   the Client ID (ignore the secret — PKCE only).

2. **Build and authorize.**
   ```sh
   make build
   export SPOTIFY_CLIENT_ID=<your-client-id>
   ./bin/musictool auth                       # opens a browser, one-shot loopback
   # headless machine? use the out-of-band flow:
   ./bin/musictool auth --no-listener
   # capture the refresh token for the container:
   ./bin/musictool auth --print-refresh-token
   ```

### Option B: TIDAL

1. **Register the app** (one-time, human). [TIDAL developer
   dashboard](https://developer.tidal.com/dashboard), create an app, set
   Redirect URI 1 to exactly `http://127.0.0.1:8888/callback`, and **enable
   these scopes** on the app (unchecked scopes fail the authorize step):
   `playlists.read`, `playlists.write`, `search.read`, `collection.read`,
   `user.read`. Copy the Client ID; no secret is used.

2. **Build and authorize.**
   ```sh
   make build
   export MUSIC_PROVIDER=tidal
   export TIDAL_CLIENT_ID=<your-client-id>
   ./bin/musictool auth                       # same flow, TIDAL login page
   ./bin/musictool auth --print-refresh-token # for the container
   ```

Every command below respects `MUSIC_PROVIDER`; with it exported once, the rest
of the workflow is identical on either provider.

### Then, either provider

3. **Sync and inspect.**
   ```sh
   ./bin/musictool sync           # liked songs, playlists, plays*, Keepers
   ./bin/musictool stats          # distilled library stats (JSON)
   ./bin/musictool signals        # recent feedback since last batch (JSON)
   ```
   *Play-history and telemetry stages skip themselves on TIDAL (see the
   capability matrix).

4. **Build an exact playlist** from a list of picks:
   ```sh
   echo '[{"artist":"Sublime","title":"Santeria"},
          {"artist":"Stick Figure","title":"Smoke Stack"}]' \
     | ./bin/musictool build --name "Test Build" --desc "resolver check"
   ```
   The result includes the read-back so you can diff intent vs result. Picks
   may carry an optional `duration_ms`; candidates within 3 seconds of it get a
   scoring tiebreak (studio cut vs live version disambiguation).

5. **Run the server** (MCP + dashboard):
   ```sh
   ./bin/musictool serve          # MCP :8080/mcp, dashboard :8081, and (when
                                    # MUSICTOOL_ZELLIJ_UPSTREAM is set) an
                                    # auth-injecting zellij terminal proxy :8083
   ```

6. **Deploy** the full two-container stack. The env file lives next to the
   compose file so compose auto-loads it:
   ```sh
   cp .env.example deploy/.env      # set MUSIC_PROVIDER + that provider's
                                    # client id and refresh token, plus LAN_IP
   docker compose -f deploy/docker-compose.yml up -d --build
   ```
   All Claude usage is interactive and bills the Max subscription: log in once
   inside the sandbox session and you're set. There is no unattended model cron
   and no API-key or Agent SDK credit usage anywhere in the stack. The weekly
   discovery batch is the `/discovery-batch` command, run from the session
   (terminal, Zellij web, or mobile attach).

Switching providers on an existing deployment means a fresh data dir (or
volume): the store is provider-stamped and track ids do not translate. ISRCs
are stored on every track, so a migration tool is feasible — it just does not
exist yet.

## CLI conventions

Data (JSON) goes to stdout; all logs go to stderr. In `serve` mode stdout is
silent. Exit codes: `0` ok, `1` auth, `2` API, `3` partial.

## MCP tools

`sync_library`, `get_library_stats`, `get_recent_signals`, `get_liked_songs`,
`get_playlists`, `read_playlist`, `search_tracks`, `resolve_tracklist`,
`create_playlist_exact`, `add_to_playlist_exact`, `remove_from_playlist_exact`,
`delete_playlist`, `get_taste_deltas`, `get_batches`, `get_artist_tags`,
`get_similar_artists`. All return compact structured fields, never full
provider objects, and all are provider-agnostic: the same tool surface
(`mcp__music__*`, server key `music` in `.mcp.json`) drives either backend.

## Security posture

- PKCE only, no client secrets. Token caches are `0600` (one per provider),
  never logged or committed.
- Only musictool writes SQLite. The dashboard is read-only except the taste
  profile editor and the vote buttons (which write to the real Keepers/Disliked
  playlists — the provider stays the source of truth).
- All binds are compose-network or LAN. Tailscale is the sole remote path. No
  port forwards, no public DNS.

## Milestone status

- M1 read core (auth, sync, liked songs): done.
- M2 write core (resolver, build, read-back verify): done.
- M3 serve + compose (MCP HTTP, two-container compose, Zellij web wiring): done.
- M4 feedback layer (hourly sync cron, play history, Keepers, signals): done
  (Spotify; on TIDAL the implicit half is structurally unavailable).
- M5 taste + batch (profile, weekly batch, dashboard v1): done; the batch is the
  interactive /discovery-batch command (subscription-billed, no unattended model
  cron by owner's choice), dashboard live.
- M6 multi-provider (provider interface, TIDAL backend): done. Live-verified
  end to end against a real TIDAL account (2026-08-14): PKCE auth + token
  refresh cache, sync (with the play-history stage skipping itself), exact
  build with ordered read-back match, name-scoped remove with itemId-targeted
  deletion, live playlist read, and delete — via both the CLI and the MCP
  server.

## Development

```sh
make test        # unit tests (PKCE known-answer, normalization, resolver,
                 #             store round-trip, MCP protocol, TIDAL JSON:API
                 #             parsing)
make vet
make dist        # cross-compile linux/amd64 + linux/arm64, static (CGO off)
```
