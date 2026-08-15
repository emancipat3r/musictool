package resolve

import (
	"context"
	"strings"
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

// Live regression: Iration "Automatic" hits exactly two releases with the
// same ISRC, same duration, both albums named "Automatic" — one recording.
// A same-ISRC tie of any size must dedupe, not go ambiguous.
func TestScoreTwoCandidatesSameISRC(t *testing.T) {
	q := model.TrackQuery{Artist: "Iration", Title: "Automatic"}
	mk := func(uri, date string) model.Track {
		tr := trk(uri, "Automatic", "Iration", 0)
		tr.ISRC = "USQY51285928"
		tr.Album = model.Album{Name: "Automatic", ReleaseDate: date}
		return tr
	}
	res := Score(q, []model.Track{mk("spotify:track:b", "2013-07-02"), mk("spotify:track:a", "2013-06-04")})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact via same-ISRC dedupe", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:a" {
		t.Fatalf("chose %s, want the earlier release", res.Chosen.URI)
	}
}

// Live regression (stress test F3): the canonical catalog cut is titled
// "X - Remastered" while the soundtrack carries the untagged title, so the
// verbatim bonus rewarded the soundtrack. Remaster tags are version-neutral
// and non-canonical albums are penalized; the 1986 album must win.
func TestScoreCanonicalBeatsSoundtrack(t *testing.T) {
	q := model.TrackQuery{Artist: "Metallica", Title: "Master of Puppets"}
	canonical := trk("spotify:track:album", "Master of Puppets (Remastered)", "Metallica", 0)
	canonical.Album = model.Album{Name: "Master of Puppets (Remastered)", ReleaseDate: "1986-03-03"}
	soundtrack := trk("spotify:track:st", "Master of Puppets", "Metallica", 0)
	soundtrack.Album = model.Album{Name: "Stranger Things: Soundtrack from the Netflix Series, Season 4", ReleaseDate: "2022-07-01"}
	res := Score(q, []model.Track{soundtrack, canonical})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:album" {
		t.Fatalf("chose %s; the canonical album must beat the soundtrack", res.Chosen.URI)
	}
}

// Stress test F6: one malformed pick costs that row, never the batch.
func TestResolveListSurvivesInvalidPick(t *testing.T) {
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:ok", "Santeria", "Sublime", 0)}}
	r := New(s, nil, "spotify")
	out, err := r.ResolveList(context.Background(), []model.TrackQuery{
		{Artist: "Sublime", Title: "Santeria"},
		{Artist: "", Title: ""},
		{Artist: "Sublime", Title: "Santeria"},
	})
	if err != nil {
		t.Fatalf("batch failed on invalid pick: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("resolutions = %d, want 3", len(out))
	}
	if out[1].Bucket != NotFound || out[1].Note == "" {
		t.Fatalf("invalid pick should be not_found with a note, got %+v", out[1])
	}
	if out[0].Bucket != Exact || out[2].Bucket != Exact {
		t.Fatal("valid picks should still resolve")
	}
}

// Stress test F4/F5: a hollow cached track or an unknown cached bucket is a
// miss (re-resolve), never manufactured confidence.
func TestCacheHollowAndUnknownBucketAreMisses(t *testing.T) {
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:fresh", "Santeria", "Sublime", 0)}}
	q := model.TrackQuery{Artist: "Sublime", Title: "Santeria"}

	hollow := &memCache{track: model.Track{URI: "spotify:track:stale"}, bucket: "exact"}
	res, err := New(s, hollow, "spotify").Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note == "from cache" || res.Chosen.URI != "spotify:track:fresh" {
		t.Fatalf("hollow cache row must be a miss, got %+v", res)
	}

	unknown := &memCache{track: trk("spotify:track:amb", "Santeria", "Sublime", 0), bucket: "ambiguous"}
	res2, err := New(s, unknown, "spotify").Resolve(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Note == "from cache" {
		t.Fatalf("unknown bucket must re-resolve, got %+v", res2)
	}
}

// Stress test F8: among same-recording releases, the least-adorned album wins
// over deluxe/anniversary editions even when their dates are close.
func TestRepresentativePrefersPlainAlbum(t *testing.T) {
	q := model.TrackQuery{Artist: "Nirvana", Title: "About a Girl"}
	mk := func(uri, album, date string) model.Track {
		tr := trk(uri, "About a Girl", "Nirvana", 0)
		tr.ISRC = "USSUB0983403"
		tr.Album = model.Album{Name: album, ReleaseDate: date}
		return tr
	}
	res := Score(q, []model.Track{
		mk("spotify:track:deluxe", "Bleach (Deluxe Edition)", "1989-06-01"),
		mk("spotify:track:plain", "Bleach", "1989-06-15"),
	})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s, want exact", res.Bucket)
	}
	if res.Chosen.URI != "spotify:track:plain" {
		t.Fatalf("chose %s; plain album must beat deluxe", res.Chosen.URI)
	}
	if !strings.Contains(res.Note, "Bleach") {
		t.Fatalf("note should name the chosen release: %q", res.Note)
	}
}

// Live regression: with the album pinned, two masterings of the same album
// (distinct ISRCs) must collapse to the least-adorned edition, not punt.
func TestSameAlbumMasteringsCollapse(t *testing.T) {
	q := model.TrackQuery{Artist: "Metallica", Title: "Master of Puppets", Album: "Master of Puppets"}
	mk := func(uri, album, isrc string) model.Track {
		tr := trk(uri, "Master of Puppets", "Metallica", 0)
		tr.ISRC = isrc
		tr.Album = model.Album{Name: album, ReleaseDate: "1986-03-03"}
		return tr
	}
	res := Score(q, []model.Track{
		mk("spotify:track:box", "Master of Puppets (Remastered Deluxe Box Set)", "QMKHM1600219"),
		mk("spotify:track:rem", "Master of Puppets (Remastered)", "USEV19900002"),
	})
	if res.Bucket != Exact {
		t.Fatalf("bucket = %s (%s), want exact via same-album collapse", res.Bucket, res.Note)
	}
	if res.Chosen.URI != "spotify:track:rem" {
		t.Fatalf("chose %s, want the least-adorned edition", res.Chosen.URI)
	}
}

// Audit finding: duration_ms was absent from the cache key, so a cached
// no-duration answer silently shadowed a duration-pinned re-resolve. Every
// scoring input must produce a distinct key.
func TestCacheKeyIncludesDuration(t *testing.T) {
	base := model.TrackQuery{Artist: "Sublime", Title: "Santeria"}
	pinned := model.TrackQuery{Artist: "Sublime", Title: "Santeria", DurationMs: 999_000}
	if cacheKey("spotify", base) == cacheKey("spotify", pinned) {
		t.Fatal("duration_ms must be part of the cache key")
	}
	albumPinned := model.TrackQuery{Artist: "Sublime", Title: "Santeria", Album: "Sublime"}
	if cacheKey("spotify", base) == cacheKey("spotify", albumPinned) {
		t.Fatal("album must be part of the cache key")
	}
	// Provider must namespace the key: a spotify URI cached for a query can
	// never replay under a tidal deployment.
	if cacheKey("spotify", base) == cacheKey("tidal", base) {
		t.Fatal("provider must be part of the cache key")
	}
}

// stubSearcher lets us exercise Resolve end-to-end without network.
type stubSearcher struct{ tracks []model.Track }

func (s stubSearcher) SearchPick(_ context.Context, _, _, _ string, _ int) ([]model.Track, error) {
	return s.tracks, nil
}

// memCache is an in-memory resolve.Cache for testing bucket passthrough.
type memCache struct {
	track  model.Track
	bucket string
	score  int
}

func (m *memCache) GetResolution(_ context.Context, _ string) (model.Track, string, int, bool) {
	if m.track.URI == "" {
		return model.Track{}, "", 0, false
	}
	return m.track, m.bucket, m.score, true
}
func (m *memCache) PutResolution(_ context.Context, _ string, track model.Track, bucket string, score int) error {
	m.track, m.bucket, m.score = track, bucket, score
	return nil
}

func TestResolveCachePreservesBucket(t *testing.T) {
	// A probable resolution must replay from cache as probable, never as a
	// silently-upgraded exact.
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:cov", "Santeria", "Some Cover Band", 30)}}
	cache := &memCache{}
	r := New(s, cache, "spotify")
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
	// Cache replays must carry metadata, not a hollow URI.
	if second.Chosen.Title == "" || len(second.Chosen.Artists) == 0 {
		t.Fatalf("cached resolution is hollow: %+v", second.Chosen)
	}
}

// Ambiguous options must be spent on distinct recordings: same-ISRC duplicates
// collapse before the top-3 truncation (live regression: two of three Nirvana
// options were the same Bleach recording).
func TestAmbiguousOptionsDedupeByISRC(t *testing.T) {
	q := model.TrackQuery{Artist: "Nirvana", Title: "About a Girl"}
	mk := func(uri, isrc, album string) model.Track {
		tr := trk(uri, "About a Girl", "Nirvana", 0)
		tr.ISRC = isrc
		tr.Album = model.Album{Name: album}
		return tr
	}
	res := Score(q, []model.Track{
		mk("spotify:track:bleach", "USSUB0983403", "Bleach"),
		mk("spotify:track:deluxe", "USSUB0983403", "Bleach (Deluxe Edition)"),
		mk("spotify:track:unplugged", "USGF19960103", "MTV Unplugged in New York"),
		mk("spotify:track:comp", "USGF20020104", "Nirvana"),
	})
	if res.Bucket != Ambiguous {
		t.Fatalf("bucket = %s, want ambiguous (three distinct recordings)", res.Bucket)
	}
	seen := map[string]int{}
	for _, o := range res.Options {
		seen[o.ISRC]++
	}
	for isrc, n := range seen {
		if n > 1 {
			t.Fatalf("ISRC %s appears %d times in options; slots wasted on one recording", isrc, n)
		}
	}
	if len(res.Options) != 3 {
		t.Fatalf("options = %d, want 3 distinct recordings", len(res.Options))
	}
}

func TestResolveUsesCacheDeterministically(t *testing.T) {
	s := stubSearcher{tracks: []model.Track{trk("spotify:track:aaa", "Santeria", "Sublime", 80)}}
	r := New(s, nil, "spotify")
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
