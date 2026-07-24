package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/emancipat3r/spotifytool/internal/model"
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
	if _, _, ok := s.GetResolution(ctx, "k"); ok {
		t.Fatal("empty cache returned a hit")
	}
	if err := s.PutResolution(ctx, "k", "spotify:track:x", "probable"); err != nil {
		t.Fatal(err)
	}
	uri, bucket, ok := s.GetResolution(ctx, "k")
	if !ok || uri != "spotify:track:x" {
		t.Fatalf("cache miss after put: %q %v", uri, ok)
	}
	if bucket != "probable" {
		t.Fatalf("bucket = %q, want probable (cache must not upgrade confidence)", bucket)
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
