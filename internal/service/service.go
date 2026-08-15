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

	"github.com/emancipat3r/musictool/internal/apperr"
	"github.com/emancipat3r/musictool/internal/auth"
	"github.com/emancipat3r/musictool/internal/config"
	"github.com/emancipat3r/musictool/internal/lastfm"
	"github.com/emancipat3r/musictool/internal/logx"
	"github.com/emancipat3r/musictool/internal/provider"
	"github.com/emancipat3r/musictool/internal/resolve"
	"github.com/emancipat3r/musictool/internal/spotify"
	"github.com/emancipat3r/musictool/internal/store"
	"github.com/emancipat3r/musictool/internal/tidal"
)

// KeepersPlaylistName is the conventional explicit-positive vote channel.
const KeepersPlaylistName = "Keepers"

// DislikedPlaylistName is the explicit-negative vote channel (the PRD's
// optional "Nope" playlist): the user drops tracks here that a build got
// wrong, and the engine refuses to re-add them.
const DislikedPlaylistName = "Disliked"

// Service holds the wired-up engine. SP is the provider abstraction — Spotify
// or TIDAL, selected by MUSIC_PROVIDER; everything downstream is
// provider-agnostic.
type Service struct {
	Cfg config.Config
	SP  provider.Client
	DB  *store.Store
	LF  *lastfm.Client
	Res *resolve.Resolver
}

// New builds a Service: the provider client (token source from cache or the
// env-seeded refresh token), the store (single writer), Last.fm, and the
// resolver (backed by the store's resolution cache).
func New(cfg config.Config) (*Service, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	var sp provider.Client
	switch cfg.Provider {
	case "", "spotify":
		ts := auth.NewTokenSource(cfg.ClientID, config.TokenURL, cfg.TokenPath(), cfg.RefreshToken, hc)
		sp = spotify.NewClient(ts)
	case "tidal":
		ts := auth.NewTokenSource(cfg.TidalClientID, config.TidalTokenURL, cfg.TokenPath(), cfg.TidalRefreshToken, hc)
		sp = tidal.NewClient(ts, cfg.TidalCountry)
	default:
		return nil, fmt.Errorf("unknown MUSIC_PROVIDER %q (want spotify or tidal)", cfg.Provider)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	// Provider stamp: one data dir belongs to one provider. Track ids, URIs,
	// and the resolver cache are provider-scoped; mixing them would corrupt
	// every signal, so a mismatched open is refused outright.
	ctx := context.Background()
	if prev, ok := db.GetMeta(ctx, "provider"); ok && prev != "" && prev != sp.Name() {
		db.Close()
		return nil, fmt.Errorf("data dir %s belongs to provider %q; set MUSIC_PROVIDER=%s or point MUSICTOOL_DATA_DIR at a fresh directory",
			cfg.DataDir, prev, prev)
	} else if !ok || prev == "" {
		_ = db.SetMeta(ctx, "provider", sp.Name())
	}
	lf := lastfm.New(cfg.LastFMKey)
	res := resolve.New(sp, db, sp.Name())
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
	SavedTracks     int               `json:"saved_tracks"`
	Playlists       int               `json:"playlists"`
	NewPlays        int               `json:"new_plays"`
	Keepers         int               `json:"keepers"`
	PlaylistsDeep   int               `json:"playlists_deep_synced"`
	Skipped         []SkippedPlaylist `json:"skipped,omitempty"`
	Disliked        int               `json:"disliked"`
	NonOwnedSkipped int               `json:"non_owned_skipped,omitempty"`
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

	if s.SP.Capabilities().PlayHistory {
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
	} else {
		logx.Infof("sync: %s exposes no play history; skipping (explicit signals still sync)", s.SP.Name())
	}

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
		// Vote channels: only playlists the USER owns qualify, or a followed
		// stranger's same-named playlist could overwrite them.
		owned := userID == "" || p.OwnerID == userID
		isKeepers := strings.EqualFold(p.Name, KeepersPlaylistName) && owned
		isDisliked := strings.EqualFold(p.Name, DislikedPlaylistName) && owned
		mustHave := isKeepers || isDisliked || batchPlaylistIDs[p.ID]
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
		if isKeepers || isDisliked {
			ids := make([]string, 0, len(tracks))
			for _, t := range tracks {
				ids = append(ids, t.ID)
			}
			if isKeepers {
				if err := s.DB.SyncKeepers(ctx, ids); err != nil {
					return fail(err)
				}
				res.Keepers = len(ids)
				logx.Infof("sync: keepers membership: %d", len(ids))
			} else {
				if err := s.DB.SyncDisliked(ctx, ids); err != nil {
					return fail(err)
				}
				res.Disliked = len(ids)
				logx.Infof("sync: disliked membership: %d", len(ids))
			}
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
