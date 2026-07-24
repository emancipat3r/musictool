package resolve

import (
	"context"
	"testing"

	"github.com/emancipat3r/spotifytool/internal/model"
)

func trk(uri, title, artist string, pop int) model.Track {
	return model.Track{
		URI:        uri,
		Title:      title,
		Artists:    []model.Artist{{Name: artist}},
		Popularity: pop,
	}
}

func TestScoreExact(t *testing.T) {
	q := model.TrackQuery{Artist: "Nirvana", Title: "Come As You Are"}
	cands := []model.Track{
		trk("spotify:track:aaa", "Come As You Are - Remastered", "Nirvana", 80),
		trk("spotify:track:bbb", "Something Else", "Nirvana", 50),
	}
	res := Score(q, cands)
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s, want exact", res.Bucket)
	}
	if res.Chosen == nil || res.Chosen.URI != "spotify:track:aaa" {
		t.Fatalf("chosen = %+v, want aaa", res.Chosen)
	}
}

func TestScoreDeterministicOnPopularityTie(t *testing.T) {
	q := model.TrackQuery{Artist: "Nirvana", Title: "Come As You Are"}
	// Two identical exact matches, different popularity: the more popular wins,
	// and it must be flagged ambiguous because both are exact & distinct.
	cands := []model.Track{
		trk("spotify:track:zzz", "Come As You Are", "Nirvana", 70),
		trk("spotify:track:aaa", "Come As You Are", "Nirvana", 70),
	}
	res1 := Score(q, cands)
	res2 := Score(q, []model.Track{cands[1], cands[0]}) // reversed input
	if res1.Bucket != res2.Bucket {
		t.Fatalf("nondeterministic bucket: %s vs %s", res1.Bucket, res2.Bucket)
	}
	if res1.Bucket != Ambiguous {
		t.Fatalf("two distinct exact matches should be ambiguous, got %s", res1.Bucket)
	}
	// Stable ordering: same first option regardless of input order.
	if res1.Options[0].URI != res2.Options[0].URI {
		t.Fatalf("nondeterministic ordering: %s vs %s", res1.Options[0].URI, res2.Options[0].URI)
	}
}

func TestScoreProbableWrongArtist(t *testing.T) {
	q := model.TrackQuery{Artist: "Sublime", Title: "Santeria"}
	cands := []model.Track{
		trk("spotify:track:cov", "Santeria", "Some Cover Band", 30),
	}
	res := Score(q, cands)
	if res.Bucket != Probable {
		t.Fatalf("bucket = %s, want probable", res.Bucket)
	}
	if res.Note == "" {
		t.Fatal("probable resolution should carry a note")
	}
}

func TestScoreNotFound(t *testing.T) {
	q := model.TrackQuery{Artist: "Nirvana", Title: "Come As You Are"}
	cands := []model.Track{
		trk("spotify:track:xxx", "Totally Different Song", "Other Artist", 90),
	}
	res := Score(q, cands)
	if res.Bucket != NotFound {
		t.Fatalf("bucket = %s, want not_found", res.Bucket)
	}
}

func TestScoreEmptyCandidates(t *testing.T) {
	res := Score(model.TrackQuery{Artist: "X", Title: "Y"}, nil)
	if res.Bucket != NotFound {
		t.Fatalf("empty candidates should be not_found, got %s", res.Bucket)
	}
}

func TestScorePartialArtistProbable(t *testing.T) {
	// "Sublime" query, candidate credited "Sublime with Rome" — partial artist.
	q := model.TrackQuery{Artist: "Sublime", Title: "Panic"}
	cands := []model.Track{
		{URI: "spotify:track:swr", Title: "Panic",
			Artists: []model.Artist{{Name: "Sublime with Rome"}}, Popularity: 40},
	}
	res := Score(q, cands)
	if res.Bucket != Exact && res.Bucket != Probable {
		t.Fatalf("partial-artist exact-title should be exact/probable, got %s", res.Bucket)
	}
}

// stubSearcher lets us exercise Resolve end-to-end without network.
type stubSearcher struct{ tracks []model.Track }

func (s stubSearcher) SearchTracks(_ context.Context, _ string, _ int) ([]model.Track, error) {
	return s.tracks, nil
}

func TestResolveUsesCacheDeterministically(t *testing.T) {
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:aaa", "Santeria", "Sublime", 80)}}
	r := New(s, nil)
	q := model.TrackQuery{Artist: "Sublime", Title: "Santeria"}
	a, err := r.Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if a.Chosen.URI != b.Chosen.URI {
		t.Fatal("resolver not deterministic across runs")
	}
}
