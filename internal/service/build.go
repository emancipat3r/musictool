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
	PlaylistID      string               `json:"playlist_id"`
	PlaylistURI     string               `json:"playlist_uri"`
	Name            string               `json:"name"`
	Requested       int                  `json:"requested"`
	Added           int                  `json:"added"`
	Resolutions     []resolve.Resolution `json:"resolutions"`
	NotFound        []model.TrackQuery   `json:"not_found"`
	Ambiguous       []resolve.Resolution `json:"ambiguous"`
	ReadbackURIs    []string             `json:"readback_uris"`
	ReadbackMatches bool                 `json:"readback_matches"`
	Note            string               `json:"note,omitempty"`
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
	// often a repeated call than an intent to make twins.
	if !opts.AllowDuplicate {
		if existing, err := s.findPlaylist(ctx, opts.Name); err == nil {
			res.PlaylistID = existing.ID
			res.PlaylistURI = "spotify:playlist:" + existing.ID
			res.Note = fmt.Sprintf("a playlist named %q already exists (%d tracks); nothing was created. Use add_to_playlist_exact to extend it, or set allow_duplicate to force a twin.",
				existing.Name, existing.TrackCount)
			return res, nil
		}
	}

	resolutions, err := s.Res.ResolveList(ctx, opts.Queries)
	if err != nil {
		return nil, err
	}
	res.Resolutions = resolutions

	var accepted []string
	seen := map[string]bool{}
	for _, r := range resolutions {
		switch r.Bucket {
		case resolve.Exact, resolve.Probable:
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
	res.PlaylistURI = "spotify:playlist:" + pl.ID

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
	res.ReadbackMatches = equalURIs(accepted, readback)

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
	res := &BuildResult{Name: pl.Name, PlaylistID: pl.ID, PlaylistURI: "spotify:playlist:" + pl.ID, Requested: len(queries)}

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

	var accepted []string
	for _, r := range resolutions {
		switch r.Bucket {
		case resolve.Exact, resolve.Probable:
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
	res.ReadbackMatches = equalURIs(append(append([]string{}, existing...), accepted...), readback)
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

// findPlaylist resolves an id or exact (case-insensitive) name against the
// live playlist list, so just-created playlists work without waiting for a
// sync.
func (s *Service) findPlaylist(ctx context.Context, ref string) (model.Playlist, error) {
	pls, err := s.SP.Playlists(ctx)
	if err != nil {
		return model.Playlist{}, err
	}
	for _, p := range pls {
		if p.ID == ref {
			return p, nil
		}
	}
	for _, p := range pls {
		if strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	return model.Playlist{}, fmt.Errorf("no playlist matches %q (by id or exact name)", ref)
}
