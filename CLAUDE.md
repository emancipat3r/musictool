# spotifytool — workflow for curation sessions

This file documents the *workflow*. Tool mechanics (arguments, schemas) are in
the MCP manifest — call `tools/list` or read the tool descriptions. Your job is
to be the taste engine; Spotify plays; playlists are the persistence layer.

## Hard rule: use the project's tools, never the claude.ai connector

All Spotify operations go through the project MCP server (`mcp__spotify__*`,
from `.mcp.json` → http://spotify:8080/mcp). NEVER use the claude.ai Spotify
connector (`mcp__claude_ai_Spotify__*`): it cannot read the library, its
create path substitutes tracks, and it acts on whatever account claude.ai has
linked — all failure modes this project exists to eliminate. If the project
tools are not loaded, say so and stop (check `/mcp`; the fix is usually
restarting Claude Code after a `git pull`) — do not fall back to the
connector.

## The contract

- **Playlists are the interface.** Curation materializes as exact playlists;
  library signals (saves, repeats, Keepers, skips) flow back as feedback.
- **The resolver is deterministic and never substitutes.** When you build, you
  get a read-back diff. Report gaps honestly — never pretend a not_found landed.
- **Keep it compact.** Prefer `get_library_stats` and `get_recent_signals` over
  dumping the library. Paginate `get_liked_songs`. These sessions stay light.

## Taste profile

- Lives at `taste-profile.md` on the shared volume (`/data/taste-profile.md`).
- Load it at the start of any curation work. It is the compression of the whole
  library into a few KB: pillars, texture preferences, artist tiers (core /
  worn-out / no, with exceptions), context notes.
- **User edits are ground truth.** If the user edits the profile, that wins over
  anything inferred from data. You may propose regenerations; the user's file is
  authoritative.

## Keepers semantics

- The **Keepers** playlist is the explicit-positive vote channel: one tap = one
  strong vote. `get_recent_signals` surfaces new Keepers since the last batch.
- An optional **Nope** playlist is a negative channel; otherwise skips + silence
  cover negatives. Real skip data isn't in the API, so
  `ignored_from_last_batch` (batch tracks with zero plays since shipping) is the
  honest negative proxy.

## Building playlists

1. Curate a list of `{artist, title, album?}` picks.
2. `resolve_tracklist` to preview buckets (exact / probable / ambiguous /
   not_found), or go straight to `create_playlist_exact`.
3. `create_playlist_exact` creates the playlist, adds exactly the resolved URIs,
   and returns the read-back. Check `readback_matches`; surface `not_found` and
   `ambiguous` to the user. For ambiguous picks, choose from the returned top-3.
4. To settle an ambiguous pick (or add late picks) use `add_to_playlist_exact`
   on the existing playlist — never rebuild from scratch for a fix-up.

## Discovery batches (convention)

- Naming: `Discovery W##` (ISO week), rationale in the playlist description.
- The weekly batch runs interactively via the `/discovery-batch` command in this
  session (subscription-billed; there is no unattended model cron by design).
  It records itself via `record_batch_label`. Ad-hoc playlist builds any time
  are fine; only a `/discovery-batch` run counts as a "batch."
- Ten tracks ≈ one commute; three sticking is a good week.

## Discovery inputs (API reality)

- No audio-feature math (deprecated upstream). Subjective qualities
  ("atmospheric", "lush") are **tag queries** via `get_artist_tags`.
- `get_similar_artists` (Last.fm) is the discovery seed replacing Spotify's dead
  related-artists. Combine with web search of scene-native sources (Cali Roots
  lineups, Sugarshack Sessions, label rosters, subreddits, Bandcamp tags).

## Data hygiene

- Only spotifytool writes SQLite (you reach it via MCP tools; the dashboard
  reads through the same engine). Don't try to touch the DB directly.
- `sync_library` refreshes liked songs, playlists, plays (append-only), and
  Keepers. Cron runs an hourly sync already; call it manually only when you need
  fresh signal mid-session.
