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

func TestScoreDurationTiebreak(t *testing.T) {
	// Two exact-title/artist candidates: the studio cut (240s) vs a live
	// version (390s) that happens to be more popular. With an expected
	// duration, the studio cut must win.
	q := model.TrackQuery{Artist: "Iration", Title: "Time Bomb", DurationMs: 240_000}
	studio := trk("spotify:track:studio", "Time Bomb", "Iration", 60)
	studio.DurationMs = 241_000
	live := trk("spotify:track:live", "Time Bomb", "Iration", 65)
	live.DurationMs = 390_000
	res := Score(q, []model.Track{live, studio})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s, want exact", res.Bucket)
	}
	if res.Chosen.URI != "spotify:track:studio" {
		t.Fatalf("chose %s; duration tiebreak should prefer the studio cut", res.Chosen.URI)
	}
}

// Live-data regression: "Time Bomb" query vs studio + live + acoustic
// candidates. Version-tag stripping makes all three exact after normalization;
// the verbatim bonus and variant penalty must break the tie toward the studio
// cut. (Search results carry no popularity since Feb 2026.)
func TestScoreVerbatimBeatsVariants(t *testing.T) {
	q := model.TrackQuery{Artist: "Iration", Title: "Time Bomb"}
	studio := trk("spotify:track:studio", "Time Bomb", "Iration", 0)
	live := trk("spotify:track:live", "Time Bomb - Live", "Iration", 0)
	acoustic := trk("spotify:track:acoustic", "Time Bomb - Acoustic", "Iration", 0)
	res := Score(q, []model.Track{live, acoustic, studio})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:studio" {
		t.Fatalf("chose %s, want studio cut", res.Chosen.URI)
	}
}

// Asking for the acoustic version must not penalize acoustic candidates.
func TestScoreWantedVariantNotPenalized(t *testing.T) {
	q := model.TrackQuery{Artist: "Iration", Title: "Time Bomb - Acoustic"}
	studio := trk("spotify:track:studio", "Time Bomb", "Iration", 0)
	acoustic := trk("spotify:track:acoustic", "Time Bomb - Acoustic", "Iration", 0)
	res := Score(q, []model.Track{studio, acoustic})
	if res.Bucket != Exact || res.Chosen.URI != "spotify:track:acoustic" {
		t.Fatalf("bucket=%s chosen=%v, want exact acoustic", res.Bucket, res.Chosen)
	}
}

// Live-data regression: Gang Starr "Mass Appeal" appears on three releases,
// all the same recording (same ISRC). That is one recording, not ambiguity;
// prefer the earliest release deterministically.
func TestScoreSameISRCDedupe(t *testing.T) {
	q := model.TrackQuery{Artist: "Gang Starr", Title: "Mass Appeal"}
	mk := func(uri, album, date string) model.Track {
		tr := trk(uri, "Mass Appeal", "Gang Starr", 0)
		tr.ISRC = "USCH39400036"
		tr.Album = model.Album{Name: album, ReleaseDate: date}
		return tr
	}
	cands := []model.Track{
		mk("spotify:track:comp2017", "Throwback Tunes: Hip Hop", "2017-09-01"),
		mk("spotify:track:fullclip", "Full Clip: A Decade Of Gang Starr", "1999-07-13"),
		mk("spotify:track:la2020", "L.A. Originals", "2020-12-04"),
	}
	res := Score(q, cands)
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact via ISRC dedupe", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:fullclip" {
		t.Fatalf("chose %s, want earliest release", res.Chosen.URI)
	}
	// Distinct ISRCs with no dominant group must still be ambiguous.
	cands[0].ISRC = "DIFFERENT"
	res2 := Score(q, cands)
	if res2.Bucket != Ambiguous {
		t.Fatalf("distinct ISRCs should stay ambiguous, got %s", res2.Bucket)
	}
}

// Live regression (exact candidate set from the real API): 8 compilations of
// the same recording plus one clean edit with its own ISRC. The dominant group
// wins and the earliest release (the original album) is chosen.
func TestScoreDominantISRCOutvotesCleanEdit(t *testing.T) {
	q := model.TrackQuery{Artist: "Gang Starr", Title: "Mass Appeal"}
	mk := func(uri, isrc, album, date string) model.Track {
		tr := trk(uri, "Mass Appeal", "Gang Starr", 0)
		tr.ISRC = isrc
		tr.Album = model.Album{Name: album, ReleaseDate: date}
		return tr
	}
	const main = "USCH39400036"
	cands := []model.Track{
		mk("spotify:track:hte", main, "Hard To Earn", "1994-03-08"),
		mk("spotify:track:clean", "USCH39400035", "Mass Appeal: The Best Of Gang Starr", "2006-12-26"),
		mk("spotify:track:fullclip", main, "Full Clip: A Decade Of Gang Starr", "1999-07-13"),
		mk("spotify:track:bestof", main, "Mass Appeal: The Best Of (Explicit)", "2006-01-01"),
		mk("spotify:track:nyhh", main, "New York Hip Hop", "2021-03-19"),
		mk("spotify:track:tt", main, "Throwback Tunes: Hip Hop", "2017-09-01"),
		mk("spotify:track:thmt", main, "Throwback Hip Hop Mix Tape", "2018-11-09"),
		mk("spotify:track:gmm", main, "Good Morning Music", "2019-11-15"),
		mk("spotify:track:lao", main, "L.A. Originals", "2020-12-04"),
	}
	res := Score(q, cands)
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:hte" {
		t.Fatalf("chose %s, want the 1994 original album", res.Chosen.URI)
	}
}

// stubSearcher lets us exercise Resolve end-to-end without network.
type stubSearcher struct{ tracks []model.Track }

func (s stubSearcher) SearchTracks(_ context.Context, _ string, _ int) ([]model.Track, error) {
	return s.tracks, nil
}

// memCache is an in-memory resolve.Cache for testing bucket passthrough.
type memCache struct{ uri, bucket string }

func (m *memCache) GetResolution(_ context.Context, _ string) (string, string, bool) {
	if m.uri == "" {
		return "", "", false
	}
	return m.uri, m.bucket, true
}
func (m *memCache) PutResolution(_ context.Context, _, uri, bucket string) error {
	m.uri, m.bucket = uri, bucket
	return nil
}

func TestResolveCachePreservesBucket(t *testing.T) {
	// A probable resolution must replay from cache as probable, never as a
	// silently-upgraded exact.
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:cov", "Santeria", "Some Cover Band", 30)}}
	cache := &memCache{}
	r := New(s, cache)
	q := model.TrackQuery{Artist: "Sublime", Title: "Santeria"}

	first, err := r.Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bucket != Probable {
		t.Fatalf("first bucket = %s, want probable", first.Bucket)
	}
	second, err := r.Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if second.Note != "from cache" {
		t.Fatalf("second resolve should hit cache, note = %q", second.Note)
	}
	if second.Bucket != Probable {
		t.Fatalf("cached bucket = %s, want probable", second.Bucket)
	}
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
