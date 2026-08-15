package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/emancipat3r/spotifytool/internal/model"
	"github.com/emancipat3r/spotifytool/internal/resolve"
	"github.com/emancipat3r/spotifytool/internal/store"
)

// BuildResult is the full outcome of an exact build: what was requested, what
// resolved, and the verified read-back so the caller can diff intent vs result.
// Gaps are reported, never silently substituted.
type BuildResult struct {
	PlaylistID   string               `json:"playlist_id"`
	PlaylistURI  string               `json:"playlist_uri"`
	Name         string               `json:"name"`
	Requested    int                  `json:"requested"`
	Added        int                  `json:"added"`
	Resolutions  []resolve.Resolution `json:"resolutions"`
	NotFound     []model.TrackQuery   `json:"not_found"`
	Ambiguous    []resolve.Resolution `json:"ambiguous"`
	ReadbackURIs []string             `json:"readback_uris"`
	// ReadbackMatches is nil when no write happened (e.g. the name_exists
	// no-op), so a benign refusal is never mistaken for a failed verification.
	ReadbackMatches *bool  `json:"readback_matches,omitempty"`
	Created         bool   `json:"created"`
	Reason          string `json:"reason,omitempty"`
	Note            string `json:"note,omitempty"`
	// DislikedSkipped lists picks that resolved to a recording in the user's
	// Disliked playlist. They are never added; remove the track from Disliked
	// to make it eligible again.
	DislikedSkipped []resolve.Resolution `json:"disliked_skipped,omitempty"`
}

// dislikedFilter holds the Disliked vote channel's keys, loaded once per
// operation. A pick matches by track id or ISRC, so another release of the
// same recording still counts as disliked.
type dislikedFilter struct{ ids, isrcs map[string]bool }

func (s *Service) loadDislikedFilter(ctx context.Context) dislikedFilter {
	ids, isrcs, err := s.DB.DislikedKeys(ctx)
	if err != nil {
		return dislikedFilter{}
	}
	return dislikedFilter{ids: ids, isrcs: isrcs}
}

func (f dislikedFilter) hits(t *model.Track) bool {
	if t == nil {
		return false
	}
	return f.ids[t.ID] || (t.ISRC != "" && f.isrcs[t.ISRC])
}

// BuildOptions controls create_playlist_exact.
type BuildOptions struct {
	Name        string
	Description string
	Public      bool
	Queries     []model.TrackQuery
	// AllowDuplicate permits creating a playlist whose name already exists.
	// Off by default so a repeated create is caught instead of silently
	// producing twins (idempotency guard).
	AllowDuplicate bool
	// RecordBatchLabel, when non-empty, records this build as a discovery batch
	// (used by the weekly batch, not by ad-hoc builds).
	RecordBatchLabel string
}

// BuildExact resolves the tracklist, creates the playlist, adds exactly the
// resolved URIs, then reads the playlist back and diffs it against intent.
func (s *Service) BuildExact(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	res := &BuildResult{Name: opts.Name, Requested: len(opts.Queries)}

	// Idempotency guard: a same-named playlist already existing is far more
	// often a repeated call than an intent to make twins. The count is read
	// live from the playlist itself (the summary field can lag).
	if !opts.AllowDuplicate {
		if existing, err := s.findPlaylist(ctx, opts.Name); err == nil {
			liveCount := existing.TrackCount
			if uris, err := s.SP.ReadbackURIs(ctx, existing.ID); err == nil {
				liveCount = len(uris)
			}
			res.PlaylistID = existing.ID
			res.PlaylistURI = s.SP.PlaylistURI(existing.ID)
			res.Created = false
			res.Reason = "name_exists"
			res.Note = fmt.Sprintf("a playlist named %q already exists (%d tracks); nothing was created. This is a no-op, not a failure. Use add_to_playlist_exact to extend it, or set allow_duplicate to force a twin.",
				existing.Name, liveCount)
			return res, nil
		}
	}

	resolutions, err := s.Res.ResolveList(ctx, opts.Queries)
	if err != nil {
		return nil, err
	}
	res.Resolutions = resolutions

	disliked := s.loadDislikedFilter(ctx)
	var accepted []string
	seen := map[string]bool{}
	for _, r := range resolutions {
		switch r.Bucket {
		case resolve.Exact, resolve.Probable:
			if disliked.hits(r.Chosen) {
				res.DislikedSkipped = append(res.DislikedSkipped, r)
				continue
			}
			if r.Chosen != nil && r.Chosen.URI != "" && !seen[r.Chosen.URI] {
				accepted = append(accepted, r.Chosen.URI)
				seen[r.Chosen.URI] = true
			}
		case resolve.Ambiguous:
			res.Ambiguous = append(res.Ambiguous, r)
		case resolve.NotFound:
			res.NotFound = append(res.NotFound, r.Query)
		}
	}

	pl, err := s.SP.CreatePlaylist(ctx, opts.Name, opts.Description, opts.Public)
	if err != nil {
		return nil, err
	}
	res.PlaylistID = pl.ID
	res.PlaylistURI = s.SP.PlaylistURI(pl.ID)

	if len(accepted) > 0 {
		if err := s.SP.AddTracks(ctx, pl.ID, accepted); err != nil {
			return res, err
		}
	}

	// Verify: read the playlist back and diff against the exact URIs we added.
	readback, err := s.SP.ReadbackURIs(ctx, pl.ID)
	if err != nil {
		return res, err
	}
	res.ReadbackURIs = readback
	res.Added = len(readback)
	res.ReadbackMatches = boolPtr(equalURIs(accepted, readback))
	res.Created = true

	if opts.RecordBatchLabel != "" {
		_ = s.DB.RecordBatch(ctx, store.Batch{
			Label:      opts.RecordBatchLabel,
			PlaylistID: pl.ID,
			TrackCount: len(readback),
			Digest:     fmt.Sprintf("%d requested, %d added, %d not found", res.Requested, res.Added, len(res.NotFound)),
		}, resolutions)
	}
	return res, nil
}

func boolPtr(b bool) *bool { return &b }

// equalURIs reports whether two URI slices are identical in content and order.
func equalURIs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AppendExact resolves picks and appends them to an EXISTING playlist
// (referenced by id or exact name, matched against the live playlist list),
// then read-back-verifies that the appended tail matches the resolved URIs.
// Duplicates already in the playlist are skipped, reported via Note. This is
// the fix-up path for ambiguous picks settled after a build.
func (s *Service) AppendExact(ctx context.Context, playlistRef string, queries []model.TrackQuery) (*BuildResult, error) {
	pl, err := s.findPlaylist(ctx, playlistRef)
	if err != nil {
		return nil, err
	}
	res := &BuildResult{Name: pl.Name, PlaylistID: pl.ID, PlaylistURI: s.SP.PlaylistURI(pl.ID), Requested: len(queries)}

	resolutions, err := s.Res.ResolveList(ctx, queries)
	if err != nil {
		return nil, err
	}
	res.Resolutions = resolutions

	existing, err := s.SP.ReadbackURIs(ctx, pl.ID)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, u := range existing {
		present[u] = true
	}

	disliked := s.loadDislikedFilter(ctx)
	var accepted []string
	for _, r := range resolutions {
		switch r.Bucket {
		case resolve.Exact, resolve.Probable:
			if disliked.hits(r.Chosen) {
				res.DislikedSkipped = append(res.DislikedSkipped, r)
				continue
			}
			if r.Chosen != nil && r.Chosen.URI != "" && !present[r.Chosen.URI] {
				accepted = append(accepted, r.Chosen.URI)
				present[r.Chosen.URI] = true
			}
		case resolve.Ambiguous:
			res.Ambiguous = append(res.Ambiguous, r)
		case resolve.NotFound:
			res.NotFound = append(res.NotFound, r.Query)
		}
	}

	if len(accepted) > 0 {
		if err := s.SP.AddTracks(ctx, pl.ID, accepted); err != nil {
			return res, err
		}
	}
	readback, err := s.SP.ReadbackURIs(ctx, pl.ID)
	if err != nil {
		return res, err
	}
	res.ReadbackURIs = readback
	res.Added = len(accepted)
	// Verify: previous contents plus the accepted tail, in order.
	res.ReadbackMatches = boolPtr(equalURIs(append(append([]string{}, existing...), accepted...), readback))
	return res, nil
}

// RemoveResult reports an exact removal.
type RemoveResult struct {
	PlaylistID       string             `json:"playlist_id"`
	Name             string             `json:"name"`
	Removed          int                `json:"removed"`
	RemovedURIs      []string           `json:"removed_uris"`
	NotInPlaylist    []model.TrackQuery `json:"not_in_playlist,omitempty"`
	ReadbackVerified bool               `json:"readback_verified"`
}

// RemoveExact removes picks from an existing playlist. Picks are matched
// against the playlist's actual contents by normalized title+artist (no
// search involved), plus any explicit URIs. Read-back verifies the removals.
func (s *Service) RemoveExact(ctx context.Context, playlistRef string, queries []model.TrackQuery, uris []string) (*RemoveResult, error) {
	pl, err := s.findPlaylist(ctx, playlistRef)
	if err != nil {
		return nil, err
	}
	tracks, err := s.SP.PlaylistTracks(ctx, pl.ID)
	if err != nil {
		return nil, err
	}
	res := &RemoveResult{PlaylistID: pl.ID, Name: pl.Name}

	toRemove := map[string]bool{}
	for _, u := range uris {
		toRemove[u] = true
	}
	for _, q := range queries {
		matched := false
		for _, t := range tracks {
			if resolve.NormalizeTitle(t.Title) == resolve.NormalizeTitle(q.Title) &&
				resolve.NormalizeArtist(t.ArtistName()) == resolve.NormalizeArtist(q.Artist) {
				toRemove[t.URI] = true
				matched = true
			}
		}
		if !matched {
			res.NotInPlaylist = append(res.NotInPlaylist, q)
		}
	}
	// Only remove URIs actually present.
	present := map[string]bool{}
	for _, t := range tracks {
		present[t.URI] = true
	}
	for u := range toRemove {
		if present[u] {
			res.RemovedURIs = append(res.RemovedURIs, u)
		}
	}
	sort.Strings(res.RemovedURIs)

	if len(res.RemovedURIs) > 0 {
		if err := s.SP.RemoveTracks(ctx, pl.ID, res.RemovedURIs); err != nil {
			return res, err
		}
	}
	res.Removed = len(res.RemovedURIs)

	readback, err := s.SP.ReadbackURIs(ctx, pl.ID)
	if err != nil {
		return res, err
	}
	stillThere := map[string]bool{}
	for _, u := range readback {
		stillThere[u] = true
	}
	res.ReadbackVerified = true
	for _, u := range res.RemovedURIs {
		if stillThere[u] {
			res.ReadbackVerified = false
		}
	}
	return res, nil
}

// DeletePlaylist unfollows (removes) a playlist from the library.
func (s *Service) DeletePlaylist(ctx context.Context, playlistRef string) (map[string]any, error) {
	pl, err := s.findPlaylist(ctx, playlistRef)
	if err != nil {
		return nil, err
	}
	if err := s.SP.UnfollowPlaylist(ctx, pl.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": pl.Name, "playlist_id": pl.ID}, nil
}

// Vote records an explicit vote from the dashboard. The canonical write is to
// the real Keepers/Disliked playlist (Spotify stays the source of truth and
// the hourly sync reconciles); the local snapshot updates immediately so the
// UI reflects the vote. Voting one way removes the opposite vote.
func (s *Service) Vote(ctx context.Context, uri, action string) (map[string]any, error) {
	if action != "keeper" && action != "dislike" {
		return nil, fmt.Errorf("action must be keeper or dislike")
	}
	trackID, ok := s.SP.TrackID(uri)
	if !ok {
		return nil, fmt.Errorf("uri must be a %s track URI", s.SP.Name())
	}
	target, opposite := KeepersPlaylistName, DislikedPlaylistName
	if action == "dislike" {
		target, opposite = DislikedPlaylistName, KeepersPlaylistName
	}
	pl, err := s.findPlaylist(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("vote needs a playlist named %q that you own: %w", target, err)
	}
	// Best-effort removal from the opposite channel.
	if op, err := s.findPlaylist(ctx, opposite); err == nil {
		_ = s.SP.RemoveTracks(ctx, op.ID, []string{uri})
	}
	// Add if absent (duplicate votes are no-ops).
	existing, err := s.SP.ReadbackURIs(ctx, pl.ID)
	if err != nil {
		return nil, err
	}
	present := false
	for _, u := range existing {
		if u == uri {
			present = true
			break
		}
	}
	if !present {
		if err := s.SP.AddTracks(ctx, pl.ID, []string{uri}); err != nil {
			return nil, err
		}
	}
	if err := s.DB.SetLocalVote(ctx, action, trackID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "action": action, "playlist": pl.Name}, nil
}

// findPlaylist resolves an id or exact (case-insensitive) name against the
// live playlist list, so just-created playlists work without waiting for a
// sync. Safety rails (most of the library is followed playlists owned by
// strangers): empty refs are refused, name matching is scoped to playlists
// the user OWNS, and an ambiguous name errors instead of picking one.
// Non-owned playlists are reachable by explicit id only.
func (s *Service) findPlaylist(ctx context.Context, ref string) (model.Playlist, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Playlist{}, fmt.Errorf("empty playlist reference refused")
	}
	pls, err := s.SP.Playlists(ctx)
	if err != nil {
		return model.Playlist{}, err
	}
	for _, p := range pls {
		if p.ID == ref {
			return p, nil
		}
	}
	userID := ""
	if u, err := s.SP.CurrentUser(ctx); err == nil {
		userID = u.ID
	}
	var matches []model.Playlist
	for _, p := range pls {
		if strings.EqualFold(p.Name, ref) && (userID == "" || p.OwnerID == userID) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return model.Playlist{}, fmt.Errorf("no playlist you own matches %q (name matching is owner-scoped; use an explicit id for followed playlists)", ref)
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return model.Playlist{}, fmt.Errorf("%d playlists you own are named %q (%s); use an explicit id", len(matches), ref, strings.Join(ids, ", "))
	}
}

// PlaylistContents is the live read of a playlist for read_playlist: the three
// states are distinguishable — unknown playlist errors, an empty playlist
// returns an empty (never null) track list, and tracks come back compact.
type PlaylistContents struct {
	PlaylistID string           `json:"playlist_id"`
	Name       string           `json:"name"`
	TrackCount int              `json:"track_count"`
	Tracks     []store.TrackRef `json:"tracks"`
}

// ReadPlaylist fetches a playlist's contents LIVE from Spotify (the local
// store is a cache for stats, not the source of truth for auditing).
func (s *Service) ReadPlaylist(ctx context.Context, ref string) (*PlaylistContents, error) {
	pl, err := s.findPlaylist(ctx, ref)
	if err != nil {
		return nil, err
	}
	tracks, err := s.SP.PlaylistTracks(ctx, pl.ID)
	if err != nil {
		return nil, err
	}
	out := &PlaylistContents{PlaylistID: pl.ID, Name: pl.Name, Tracks: make([]store.TrackRef, 0, len(tracks))}
	for _, t := range tracks {
		out.Tracks = append(out.Tracks, store.TrackRef{ID: t.ID, URI: t.URI, Title: t.Title, Artist: t.ArtistName()})
	}
	out.TrackCount = len(out.Tracks)
	return out, nil
}
