package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/emancipat3r/spotifytool/internal/model"
)

func TestDecayHalfLife(t *testing.T) {
	if decayFactor(0) != 1 {
		t.Fatal("fresh evidence should not decay")
	}
	if d := decayFactor(implicitHalfLifeDays); math.Abs(d-0.5) > 0.001 {
		t.Fatalf("half-life decay = %f, want 0.5", d)
	}
}

// A single early skip must never make an artist "falling" (skips correlate
// with engagement); corroboration flips it.
func TestTrendCorroborationThresholds(t *testing.T) {
	oneSkip := map[string]int{"skip_early": 1}
	if got := classifyTrend(-1.6, oneSkip); got == "falling" {
		t.Fatal("a single skip must never demote an artist")
	}
	twoSkips := map[string]int{"skip_early": 2}
	if got := classifyTrend(-1.6, twoSkips); got != "falling" {
		t.Fatalf("two early skips with low affinity should be falling, got %s", got)
	}
	explicit := map[string]int{"dislikes": 1, "skip_mid": 1}
	if got := classifyTrend(-6.2, explicit); got != "falling" {
		t.Fatalf("explicit dislike + skip should be falling, got %s", got)
	}
	rising := map[string]int{"saves": 2, "completed": 4}
	if got := classifyTrend(4.0, rising); got != "rising" {
		t.Fatalf("saves + repeats should be rising, got %s", got)
	}
}

func TestTasteDeltasEndToEnd(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	mk := func(id, artist string) model.SavedTrack {
		return model.SavedTrack{Track: model.Track{
			ID: id, URI: "spotify:track:" + id, Title: "T-" + id,
			Artists: []model.Artist{{ID: "a-" + artist, Name: artist}},
		}, SavedAt: time.Now()}
	}
	// Two saved tracks by Riser, one by Faller.
	if err := s.ReplaceSavedTracks(ctx, []model.SavedTrack{
		mk("r1", "Riser"), mk("r2", "Riser"), mk("f1", "Faller"),
	}); err != nil {
		t.Fatal(err)
	}
	// Riser: keeper vote + completions. Faller: dislike vote + early skip.
	if err := s.SyncKeepers(ctx, []string{"r1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncDisliked(ctx, []string{"f1"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tr := model.Track{ID: "r1", URI: "spotify:track:r1", Title: "T-r1", Artists: []model.Artist{{Name: "Riser"}}}
	for i := 0; i < 3; i++ {
		if err := s.RecordListen(ctx, tr, now.Add(-time.Hour), now, 200_000, 210_000, "completed"); err != nil {
			t.Fatal(err)
		}
	}
	fr := model.Track{ID: "f1", URI: "spotify:track:f1", Title: "T-f1", Artists: []model.Artist{{Name: "Faller"}}}
	if err := s.RecordListen(ctx, fr, now.Add(-time.Hour), now, 10_000, 210_000, "skip_early"); err != nil {
		t.Fatal(err)
	}

	deltas, err := s.TasteDeltas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ArtistDelta{}
	for _, d := range deltas {
		byName[d.Artist] = d
	}
	r := byName["Riser"]
	if r.Trend != "rising" {
		t.Fatalf("Riser trend = %s (affinity %f, ev %v), want rising", r.Trend, r.Affinity, r.Evidence)
	}
	f := byName["Faller"]
	if f.Trend != "falling" {
		t.Fatalf("Faller trend = %s (affinity %f, ev %v), want falling", f.Trend, f.Affinity, f.Evidence)
	}
	if f.Evidence["dislikes"] != 1 || f.Evidence["skip_early"] != 1 {
		t.Fatalf("Faller evidence wrong: %v", f.Evidence)
	}
}

func TestSetLocalVoteFlipsChannels(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	_ = s.ReplaceSavedTracks(ctx, []model.SavedTrack{
		{Track: model.Track{ID: "v1", URI: "u", Title: "V", Artists: []model.Artist{{ID: "a", Name: "X"}}}},
	})
	if err := s.SetLocalVote(ctx, "keeper", "v1"); err != nil {
		t.Fatal(err)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM keepers WHERE track_id='v1'`); n != 1 {
		t.Fatal("keeper vote not recorded")
	}
	// Flip to dislike: must leave keepers.
	if err := s.SetLocalVote(ctx, "dislike", "v1"); err != nil {
		t.Fatal(err)
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM keepers WHERE track_id='v1'`); n != 0 {
		t.Fatal("flip did not remove keeper vote")
	}
	if n := countRow(ctx, s.db, `SELECT COUNT(*) FROM disliked WHERE track_id='v1'`); n != 1 {
		t.Fatal("dislike vote not recorded")
	}
}
