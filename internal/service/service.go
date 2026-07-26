// Package service is the high-level orchestration layer shared by the CLI and
// the MCP server. It wires the Spotify client, the SQLite store, the resolver,
// and Last.fm together, and exposes the operations both front ends need (sync,
// build-exact, stats, signals, discovery seeds).
package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emancipat3r/spotifytool/internal/apperr"
	"github.com/emancipat3r/spotifytool/internal/auth"
	"github.com/emancipat3r/spotifytool/internal/config"
	"github.com/emancipat3r/spotifytool/internal/lastfm"
	"github.com/emancipat3r/spotifytool/internal/logx"
	"github.com/emancipat3r/spotifytool/internal/resolve"
	"github.com/emancipat3r/spotifytool/internal/spotify"
	"github.com/emancipat3r/spotifytool/internal/store"
)

// KeepersPlaylistName is the conventional explicit-positive vote channel.
const KeepersPlaylistName = "Keepers"

// Service holds the wired-up engine.
type Service struct {
	Cfg config.Config
	SP  *spotify.Client
	DB  *store.Store
	LF  *lastfm.Client
	Res *resolve.Resolver
}

// New builds a Service: token source from cache or SPOTIFY_REFRESH_TOKEN, the
// Spotify client, the store (single writer), Last.fm, and the resolver (backed
// by the store's resolution cache).
func New(cfg config.Config) (*Service, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	ts := auth.NewTokenSource(cfg.ClientID, cfg.TokenPath(), cfg.RefreshToken, &http.Client{Timeout: 30 * time.Second})
	sp := spotify.NewClient(ts)
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	lf := lastfm.New(cfg.LastFMKey)
	res := resolve.New(sp, db)
	return &Service{Cfg: cfg, SP: sp, DB: db, LF: lf, Res: res}, nil
}

// Close releases resources.
func (s *Service) Close() error {
	if s.DB != nil {
		return s.DB.Close()
	}
	return nil
}

// SkippedPlaylist records a playlist the deep sync could not read.
type SkippedPlaylist struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// SyncResult summarizes a sync run.
type SyncResult struct {
	SavedTracks    int               `json:"saved_tracks"`
	Playlists      int               `json:"playlists"`
	NewPlays       int               `json:"new_plays"`
	Keepers        int               `json:"keepers"`
	PlaylistsDeep  int               `json:"playlists_deep_synced"`
	Skipped        []SkippedPlaylist `json:"skipped,omitempty"`
	NonOwnedSkipped int              `json:"non_owned_skipped,omitempty"`
}

// Sync refreshes liked songs, playlist metadata, recently-played history
// (append-only), and Keepers membership. If full is set, it also refreshes
// every playlist's track membership (heavier; use for periodic deep syncs).
//
// Partial-progress discipline (PRD): each stage commits independently, so a
// failure after the first successful stage returns exit 3 (partial) with the
// cause, never pretending nothing landed. Auth failures keep exit 1 so the
// "run auth" signal stays loud.
func (s *Service) Sync(ctx context.Context, full bool) (SyncResult, error) {
	var res SyncResult
	progressed := false
	fail := func(err error) (SyncResult, error) {
		if progressed && apperr.Code(err) != apperr.CodeAuth {
			return res, apperr.Partial(fmt.Errorf("sync stopped after partial progress: %w", err))
		}
		return res, err
	}

	logx.Infof("sync: fetching liked songs…")
	saved, err := s.SP.SavedTracks(ctx)
	if err != nil {
		return fail(err)
	}
	if err := s.DB.ReplaceSavedTracks(ctx, saved); err != nil {
		return fail(err)
	}
	res.SavedTracks = len(saved)
	progressed = true

	logx.Infof("sync: fetching playlists…")
	pls, err := s.SP.Playlists(ctx)
	if err != nil {
		return fail(err)
	}
	if err := s.DB.ReplacePlaylists(ctx, pls); err != nil {
		return fail(err)
	}
	res.Playlists = len(pls)

	logx.Infof("sync: fetching recently played…")
	plays, err := s.SP.RecentlyPlayed(ctx)
	if err != nil {
		return fail(err)
	}
	added, err := s.DB.AppendPlays(ctx, plays)
	if err != nil {
		return fail(err)
	}
	res.NewPlays = added

	// Record the current user id so name-scoped operations and owned-only
	// filtering work. Non-fatal on failure.
	var userID string
	if user, err := s.SP.CurrentUser(ctx); err == nil {
		userID = user.ID
		_ = s.DB.SetMeta(ctx, "user_id", userID)
	} else {
		logx.Errorf("sync: could not fetch current user: %v", err)
	}

	// Keepers + batch playlists always get full track membership so votes and
	// ignored-track detection work; other playlists only when full is set.
	//
	// Per-playlist error isolation: a 403 on one playlist (typically a
	// followed playlist owned by someone else) is logged and skipped, never
	// fatal to the sweep. The deep sweep also skips non-owned playlists by
	// default — they are the usual 403 source and are not curation targets.
	logx.Infof("sync: playlist tracks (full=%v)…", full)
	batchPlaylistIDs := s.batchPlaylistIDs(ctx)
	for _, p := range pls {
		isKeepers := strings.EqualFold(p.Name, KeepersPlaylistName)
		mustHave := isKeepers || batchPlaylistIDs[p.ID]
		if !full && !mustHave {
			continue
		}
		if full && !mustHave && userID != "" && p.OwnerID != userID {
			res.NonOwnedSkipped++
			continue
		}
		tracks, err := s.SP.PlaylistTracks(ctx, p.ID)
		if err != nil {
			logx.Errorf("sync: skipping playlist %q (%s): %v", p.Name, p.ID, err)
			res.Skipped = append(res.Skipped, SkippedPlaylist{Name: p.Name, ID: p.ID, Reason: err.Error()})
			continue
		}
		if err := s.DB.ReplacePlaylistTracks(ctx, p.ID, tracks); err != nil {
			return fail(err)
		}
		res.PlaylistsDeep++
		if isKeepers {
			ids := make([]string, 0, len(tracks))
			for _, t := range tracks {
				ids = append(ids, t.ID)
			}
			if err := s.DB.SyncKeepers(ctx, ids); err != nil {
				return fail(err)
			}
			res.Keepers = len(ids)
			logx.Infof("sync: keepers membership: %d", len(ids))
		}
	}
	if len(res.Skipped) > 0 {
		logx.Infof("sync: done with %d playlist(s) skipped (see errors above)", len(res.Skipped))
	}
	return res, nil
}

func (s *Service) batchPlaylistIDs(ctx context.Context) map[string]bool {
	ids := map[string]bool{}
	batches, err := s.DB.ListBatches(ctx, 50)
	if err != nil {
		return ids
	}
	for _, b := range batches {
		if b.PlaylistID != "" {
			ids[b.PlaylistID] = true
		}
	}
	return ids
}
