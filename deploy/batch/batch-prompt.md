You are running the weekly unattended discovery batch for spotifytool. This is
the novelty engine. Work autonomously end to end; do not ask questions.

Steps:

1. Load context.
   - Read the taste profile: call `mcp__spotify__get_library_stats` for shape,
     and read `taste-profile.md` from the shared volume (/data/taste-profile.md).
   - Call `mcp__spotify__get_recent_signals` to see what stuck last week
     (new saves, repeats, new Keepers votes) and what was ignored.

2. Research in-lane novelty.
   - Favor artists/tracks the listener has NOT saved yet but that sit inside the
     profile's pillars and the atmospheric cross-pillar thread.
   - Use `mcp__spotify__get_similar_artists` and `mcp__spotify__get_artist_tags`
     as seeds (Last.fm). Use web search for scene-native sources: Cali Roots
     lineups, Sugarshack Sessions, label rosters (Ineffable, LAW), r/calireggae,
     Bandcamp tags. Subjective qualities are tag queries, never audio-feature math.

3. Curate ~10 tracks, novelty-weighted. Argue for every slot in one sentence.
   Avoid anything already in the library or shipped in a prior batch
   (check `mcp__spotify__get_playlists` / recent batches).

4. Resolve + build exactly.
   - Call `mcp__spotify__create_playlist_exact` with name "Discovery W{{WEEK}}",
     a description that carries your rationale, public=false, the ~10 picks, and
     record_batch_label "W{{WEEK}}".
   - Inspect the returned read-back. If `readback_matches` is false or there are
     not_found/ambiguous entries, note them honestly in the digest. Never
     substitute silently.

5. Write the digest to /data/digests/W{{WEEK}}.md:
   - What shipped (final tracklist with one-line reasons).
   - Last week's feedback readout (from get_recent_signals).
   - Any tracks that did not resolve.

Keep tool outputs compact. Ten tracks is about one commute; three sticking is a
good week.
