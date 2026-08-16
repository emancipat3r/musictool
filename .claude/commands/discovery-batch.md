---
description: Run the weekly discovery batch interactively (subscription-billed)
---

Run the weekly discovery batch for musictool. This replaces the old unattended
cron batch; it runs here in the interactive session so it bills the Max
subscription, never the API or Agent SDK credit. Work end to end; only pause if
a resolution is ambiguous and you need a pick confirmed.

Determine the ISO week number first (`date +%V`); call it W##.

1. Load context.
   - Read the taste profile from the shared volume (/data/taste-profile.md).
   - Call `mcp__music__get_recent_signals` to see what stuck since the last
     batch (new saves, repeats, Keepers/Disliked votes, early skips) and what
     was ignored.
   - Call `mcp__music__get_taste_deltas` and regenerate the profile's
     `<!-- signals:auto -->` block per the CLAUDE.md rules (trust the trend
     field; cite evidence counts; never touch text outside the block).
   - Call `mcp__music__get_library_stats` for overall shape.

2. Research in-lane novelty.
   - Favor artists/tracks the listener has NOT saved yet but that sit inside the
     profile's pillars and the atmospheric cross-pillar thread.
   - Use `mcp__music__get_similar_artists` and `mcp__music__get_artist_tags`
     as seeds (Last.fm). Use web search for scene-native sources: Cali Roots
     lineups, Sugarshack Sessions, label rosters (Ineffable, LAW), r/calireggae,
     Bandcamp tags. Subjective qualities are tag queries, never audio-feature
     math.

3. Curate ~10 tracks, novelty-weighted. Argue for every slot in one sentence.
   Avoid anything already in the library or shipped in a prior batch (check
   `mcp__music__get_playlists` and recent batches).

4. Resolve and build exactly.
   - Call `mcp__music__create_playlist_exact` with name "Discovery W##", a
     description carrying your rationale, public=false, the ~10 picks, and
     record_batch_label "W##".
   - Second run in the same week: the create returns `created: false` with
     `reason: "name_exists"` (a benign guard, not an error). NEVER set
     allow_duplicate. Instead append the new picks to the existing
     "Discovery W##" with `mcp__music__add_to_playlist_exact` (duplicates
     are skipped automatically) and title the digest section "W## second
     run". One playlist per week, always.
   - Inspect the returned read-back. If `readback_matches` is false or there
     are not_found/ambiguous entries, report them honestly. Never substitute
     silently.

5. Write the digest to /data/digests/W##.md:
   - What shipped (final tracklist with one-line reasons).
   - The feedback readout from step 1.
   - Any tracks that did not resolve.

Keep tool outputs compact. Ten tracks is about one commute; three sticking is a
good week.
