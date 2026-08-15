package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emancipat3r/musictool/internal/model"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSchemaAndSavedRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	saved := []model.SavedTrack{
		{
			Track: model.Track{
				ID: "t1", URI: "spotify:track:t1", Title: "Santeria",
				Artists: []model.Artist{{ID: "a1", Name: "Sublime"}},
				Album:   model.Album{ID: "al1", Name: "Sublime"},
			},
			SavedAt: time.Now().Add(-time.Hour),
		},
		{
			Track: model.Track{
				ID: "t2", URI: "spotify:track:t2", Title: "What I Got",
				Artists: []model.Artist{{ID: "a1", Name: "Sublime"}},
			},
			SavedAt: time.Now(),
		},
	}
	if err := s.ReplaceSavedTracks(ctx, saved); err != nil {
		t.Fatalf("ReplaceSavedTracks: %v", err)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.SavedTracks != 2 {
		t.Fatalf("saved = %d, want 2", st.SavedTracks)
	}
	if st.Artists != 1 {
		t.Fatalf("artists = %d, want 1", st.Artists)
	}
	if len(st.TopArtists) == 0 || st.TopArtists[0].Name != "Sublime" || st.TopArtists[0].Count != 2 {
		t.Fatalf("top artists wrong: %+v", st.TopArtists)
	}

	liked, err := s.LikedSongs(ctx, 10, 0)
	if err != nil || len(liked) != 2 {
		t.Fatalf("LikedSongs = %d (%v)", len(liked), err)
	}
}

func TestResolutionCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if _, _, _, ok := s.GetResolution(ctx, "k"); ok {
		t.Fatal("empty cache returned a hit")
	}
	stored := model.Track{
		URI: "spotify:track:x", ID: "x", Title: "Santeria",
		Artists: []model.Artist{{Name: "Sublime"}},
		Album:   model.Album{Name: "Sublime"},
	}
	if err := s.PutResolution(ctx, "k", stored, "probable", 145); err != nil {
		t.Fatal(err)
	}
	got, bucket, score, ok := s.GetResolution(ctx, "k")
	if !ok || got.URI != "spotify:track:x" {
		t.Fatalf("cache miss after put: %+v %v", got, ok)
	}
	if bucket != "probable" {
		t.Fatalf("bucket = %q, want probable (cache must not upgrade confidence)", bucket)
	}
	if score != 145 {
		t.Fatalf("score = %d, want 145 (replays must be explainable)", score)
	}
	// Cache hits must carry metadata, not hollow URI-only tracks.
	if got.Title != "Santeria" || got.ArtistName() != "Sublime" {
		t.Fatalf("cache returned hollow track: %+v", got)
	}
}

func TestAppendPlaysCreatesMinimalTrackRow(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	added, err := s.AppendPlays(ctx, []model.PlayEvent{
		{TrackID: "unsaved1", PlayedAt: time.Now(), Title: "New Find", Artist: "Fresh Band"},
		{TrackID: "unsaved1", PlayedAt: time.Now().Add(time.Minute), Title: "New Find", Artist: "Fresh Band"},
	})
	if err != nil || added != 2 {
		t.Fatalf("AppendPlays = %d, %v", added, err)
	}
	// The play of a never-synced track must still be joinable.
	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM tracks WHERE id='unsaved1'`).Scan(&title); err != nil {
		t.Fatalf("minimal track row missing: %v", err)
	}
	if title != "New Find" {
		t.Fatalf("title = %q", title)
	}
}

// Audit finding: the hourly ReplacePlaylists used DELETE FROM playlists, and
// playlist_tracks cascades on playlist deletion — so every hourly sync wiped
// the daily deep sync. Upserting must preserve playlist_tracks for surviving
// playlists and cascade only for genuinely removed ones.
func TestReplacePlaylistsPreservesPlaylistTracks(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	pls := []model.Playlist{
		{ID: "pl1", Name: "Deck", TrackCount: 1},
		{ID: "pl2", Name: "Old", TrackCount: 1},
	}
	if err := s.ReplacePlaylists(ctx, pls); err != nil {
		t.Fatal(err)
	}
	tr := model.Track{ID: "t1", URI: "u1", Title: "A", Artists: []model.Artist{{ID: "a", Name: "X"}}}
	if err := s.ReplacePlaylistTracks(ctx, "pl1", []model.Track{tr}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePlaylistTracks(ctx, "pl2", []model.Track{tr}); err != nil {
		t.Fatal(err)
	}

	// Hourly refresh with pl2 gone: pl1's tracks survive, pl2's cascade away.
	if err := s.ReplacePlaylists(ctx, pls[:1]); err != nil {
		t.Fatal(err)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id='pl1'`); n != 1 {
		t.Fatalf("pl1 tracks = %d, want 1 (hourly sync must not wipe deep sync)", n)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id='pl2'`); n != 0 {
		t.Fatalf("pl2 tracks = %d, want 0 (removed playlist should cascade)", n)
	}
}

// Audit finding: signal lists must serialize as [], never null — "nothing new"
// and "no data" are different claims.
func TestSignalsEmptyListsAreNotNull(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	sig, err := s.Signals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(sig)
	if strings.Contains(string(b), "null") {
		t.Fatalf("signals contain null lists: %s", b)
	}
}

// The Disliked channel mirrors Keepers: membership diffs in and out, keys are
// retrievable by id and ISRC for build-time refusal.
func TestDislikedSyncAndKeys(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	_ = s.ReplaceSavedTracks(ctx, []model.SavedTrack{
		{Track: model.Track{ID: "d1", URI: "u1", Title: "Meh", ISRC: "ISRC001",
			Artists: []model.Artist{{ID: "ar", Name: "X"}}}},
	})
	if err := s.SyncDisliked(ctx, []string{"d1"}); err != nil {
		t.Fatal(err)
	}
	ids, isrcs, err := s.DislikedKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ids["d1"] || !isrcs["ISRC001"] {
		t.Fatalf("keys missing: ids=%v isrcs=%v", ids, isrcs)
	}
	// Un-dislike prunes.
	if err := s.SyncDisliked(ctx, nil); err != nil {
		t.Fatal(err)
	}
	ids, _, _ = s.DislikedKeys(ctx)
	if len(ids) != 0 {
		t.Fatalf("dislike not pruned: %v", ids)
	}
}

func TestKeepersDiff(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	// Seed tracks so joins resolve.
	_ = s.ReplaceSavedTracks(ctx, []model.SavedTrack{
		{Track: model.Track{ID: "k1", URI: "u1", Title: "A", Artists: []model.Artist{{ID: "ar", Name: "X"}}}},
		{Track: model.Track{ID: "k2", URI: "u2", Title: "B", Artists: []model.Artist{{ID: "ar", Name: "X"}}}},
	})
	if err := s.SyncKeepers(ctx, []string{"k1", "k2"}); err != nil {
		t.Fatal(err)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM keepers`); n != 2 {
		t.Fatalf("keepers = %d, want 2", n)
	}
	// Remove one from the playlist; it should be pruned.
	if err := s.SyncKeepers(ctx, []string{"k1"}); err != nil {
		t.Fatal(err)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM keepers`); n != 1 {
		t.Fatalf("keepers after prune = %d, want 1", n)
	}
}
