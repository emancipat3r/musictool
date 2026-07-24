package service

import (
	"context"
	"fmt"

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
}

// BuildOptions controls create_playlist_exact.
type BuildOptions struct {
	Name        string
	Description string
	Public      bool
	Queries     []model.TrackQuery
	// RecordBatchLabel, when non-empty, records this build as a discovery batch
	// (used by the weekly batch, not by ad-hoc builds).
	RecordBatchLabel string
}

// BuildExact resolves the tracklist, creates the playlist, adds exactly the
// resolved URIs, then reads the playlist back and diffs it against intent.
func (s *Service) BuildExact(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	res := &BuildResult{Name: opts.Name, Requested: len(opts.Queries)}

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

	user, err := s.SP.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	pl, err := s.SP.CreatePlaylist(ctx, user.ID, opts.Name, opts.Description, opts.Public)
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
