package resolve

import (
	"context"
	"sort"
	"strings"

	"github.com/emancipat3r/spotifytool/internal/model"
)

// Bucket classifies a resolution outcome.
type Bucket string

const (
	Exact     Bucket = "exact"     // auto-accept
	Probable  Bucket = "probable"  // accept, note
	Ambiguous Bucket = "ambiguous" // return top-3 for caller
	NotFound  Bucket = "not_found"
)

// Resolution is the deterministic result for one curated pick.
type Resolution struct {
	Query   model.TrackQuery `json:"query"`
	Bucket  Bucket           `json:"bucket"`
	Chosen  *model.Track     `json:"chosen,omitempty"`  // exact/probable
	Options []model.Track    `json:"options,omitempty"` // ambiguous (top 3)
	Note    string           `json:"note,omitempty"`
	Score   int              `json:"score,omitempty"`
}

// scored pairs a candidate with its computed score and reasons.
type scored struct {
	track  model.Track
	score  int
	title  bool // title matched exactly (after normalization)
	artist bool // artist matched exactly
}

// Searcher is the minimal Spotify surface the resolver needs. The real client
// satisfies it; tests use a stub.
type Searcher interface {
	SearchTracks(ctx context.Context, query string, limit int) ([]model.Track, error)
}

// Cache stores prior resolutions so identical inputs are free and stable. The
// bucket is cached alongside the URI so a probable hit replays as probable, not
// as a false exact.
type Cache interface {
	GetResolution(ctx context.Context, key string) (uri, bucket string, ok bool)
	PutResolution(ctx context.Context, key, uri string, bucket string) error
}

// Resolver turns curated picks into exact URIs.
type Resolver struct {
	search Searcher
	cache  Cache
}

// New builds a resolver. cache may be nil (no caching).
func New(search Searcher, cache Cache) *Resolver {
	return &Resolver{search: search, cache: cache}
}

// cacheKey is the stable key for a query: normalized artist|title|album.
func cacheKey(q model.TrackQuery) string {
	return NormalizeArtist(q.Artist) + "\x1f" + NormalizeTitle(q.Title) + "\x1f" + NormalizeTitle(q.Album)
}

// Resolve resolves a single pick, consulting the cache first.
func (r *Resolver) Resolve(ctx context.Context, q model.TrackQuery) (Resolution, error) {
	key := cacheKey(q)
	if r.cache != nil {
		if uri, bucket, ok := r.cache.GetResolution(ctx, key); ok {
			b := Bucket(bucket)
			if b != Exact && b != Probable {
				b = Exact // legacy rows cached before buckets were stored
			}
			return Resolution{Query: q, Bucket: b, Chosen: &model.Track{URI: uri}, Note: "from cache"}, nil
		}
	}

	// Field-filtered search, never free text. 10 is the dev-mode search cap.
	query := fieldQuery(q.Artist, q.Title, q.Album)
	cands, err := r.search.SearchTracks(ctx, query, 10)
	if err != nil {
		return Resolution{}, err
	}
	// Retry once without album if the album filter over-constrained.
	if len(cands) == 0 && q.Album != "" {
		cands, err = r.search.SearchTracks(ctx, fieldQuery(q.Artist, q.Title, ""), 10)
		if err != nil {
			return Resolution{}, err
		}
	}

	res := Score(q, cands)
	if r.cache != nil && (res.Bucket == Exact || res.Bucket == Probable) && res.Chosen != nil {
		_ = r.cache.PutResolution(ctx, key, res.Chosen.URI, string(res.Bucket))
	}
	return res, nil
}

// ResolveList resolves many picks in order.
func (r *Resolver) ResolveList(ctx context.Context, qs []model.TrackQuery) ([]Resolution, error) {
	out := make([]Resolution, 0, len(qs))
	for _, q := range qs {
		res, err := r.Resolve(ctx, q)
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

// Score is the pure, deterministic scoring/bucketing over a candidate set. It is
// separated from I/O so it can be unit-tested exhaustively offline.
func Score(q model.TrackQuery, cands []model.Track) Resolution {
	nqTitle := NormalizeTitle(q.Title)
	nqArtist := NormalizeArtist(q.Artist)
	nqAlbum := NormalizeTitle(q.Album)
	nqVerbatim := NormalizeVerbatim(q.Title)

	scoredCands := make([]scored, 0, len(cands))
	for _, c := range cands {
		ncTitle := NormalizeTitle(c.Title)
		titleExact := ncTitle == nqTitle && nqTitle != ""
		titlePart := !titleExact && nqTitle != "" && (strings.Contains(ncTitle, nqTitle) || strings.Contains(nqTitle, ncTitle))

		artistExact, artistPart := artistMatch(nqArtist, c)

		s := 0
		switch {
		case titleExact:
			s += 100
		case titlePart:
			s += 45
		}
		// Verbatim bonus: the candidate's raw title matches the query with no
		// tag-stripping needed ("Time Bomb" beats "Time Bomb - Live", which
		// only matches after normalization).
		if titleExact && NormalizeVerbatim(c.Title) == nqVerbatim {
			s += 25
		}
		// Variant penalty: candidate carries a performance-variant marker
		// (live/acoustic/instrumental/…) the query did not ask for.
		if HasUnwantedVariant(q.Title, c.Title) {
			s -= 15
		}
		switch {
		case artistExact:
			s += 100
		case artistPart:
			s += 45
		}
		if nqAlbum != "" && NormalizeTitle(c.Album.Name) == nqAlbum {
			s += 8
		}
		// Duration tiebreaker: when the caller supplied an expected length, a
		// candidate within ±3s of it is very likely the same recording (and a
		// live/extended version is not).
		if q.DurationMs > 0 && c.DurationMs > 0 {
			diff := q.DurationMs - c.DurationMs
			if diff < 0 {
				diff = -diff
			}
			if diff <= 3000 {
				s += 6
			}
		}
		// Popularity as a small, stable tiebreak (0-100 -> 0-10). NOTE: as of
		// the Feb 2026 dev-mode changes, /search results omit popularity, so
		// this is usually 0; it still helps when candidates come from library
		// data.
		s += c.Popularity / 10

		scoredCands = append(scoredCands, scored{track: c, score: s, title: titleExact, artist: artistExact})
	}

	// Deterministic ordering: score desc, then popularity desc, then URI asc.
	sort.SliceStable(scoredCands, func(i, j int) bool {
		if scoredCands[i].score != scoredCands[j].score {
			return scoredCands[i].score > scoredCands[j].score
		}
		if scoredCands[i].track.Popularity != scoredCands[j].track.Popularity {
			return scoredCands[i].track.Popularity > scoredCands[j].track.Popularity
		}
		return scoredCands[i].track.URI < scoredCands[j].track.URI
	})

	return classify(q, scoredCands)
}

// classify turns the ranked candidates into a bucketed resolution.
func classify(q model.TrackQuery, ranked []scored) Resolution {
	res := Resolution{Query: q, Bucket: NotFound}
	if len(ranked) == 0 {
		return res
	}
	best := ranked[0]
	res.Score = best.score

	// No title overlap at all → genuinely not found.
	if !best.title && !titleOverlaps(q.Title, best.track.Title) {
		res.Bucket = NotFound
		res.Note = "no candidate matched the title"
		return res
	}

	t := best.track
	switch {
	case best.title && best.artist:
		// Gather every equally-scored exact match and decide whether the tie
		// is real ambiguity or the same recording republished across releases.
		tied := tiedExact(ranked)
		if len(tied) > 1 {
			if group, isrc := dominantISRCGroup(tied); group != nil {
				// One recording, many releases (singles, compilations,
				// deluxe editions; stray clean edits carry their own ISRC and
				// are outvoted). Prefer the earliest release, then URI.
				chosen := earliestRelease(group)
				res.Bucket = Exact
				res.Chosen = &chosen
				res.Note = "same recording across releases (ISRC " + isrc + "); chose earliest"
				return res
			}
			res.Bucket = Ambiguous
			res.Options = topTracks(ranked, 3)
			res.Note = "multiple exact matches; caller picks"
			return res
		}
		res.Bucket = Exact
		res.Chosen = &t
		if popularTie(ranked) {
			res.Note = "chose most-popular among tied exact matches"
		}
	case best.title && !best.artist:
		// Right title, artist not exact — accept but flag.
		res.Bucket = Probable
		res.Chosen = &t
		res.Note = "title matched; artist differs from query, verify"
	case !best.title && best.artist:
		res.Bucket = Probable
		res.Chosen = &t
		res.Note = "artist matched; title is a near (not exact) match"
	default:
		res.Bucket = Ambiguous
		res.Options = topTracks(ranked, 3)
		res.Note = "no exact match; top candidates returned"
	}
	return res
}

// artistMatch reports whether any candidate artist matches the query artist
// exactly, or partially (substring either direction — covers "Sublime" vs
// "Sublime with Rome" style differences).
func artistMatch(nqArtist string, c model.Track) (exact, partial bool) {
	if nqArtist == "" {
		return false, false
	}
	for _, a := range c.Artists {
		na := NormalizeArtist(a.Name)
		if na == nqArtist {
			return true, false
		}
	}
	joined := NormalizeArtist(c.AllArtists())
	if strings.Contains(joined, nqArtist) || strings.Contains(nqArtist, joined) {
		return false, true
	}
	return false, false
}

func titleOverlaps(a, b string) bool {
	na, nb := NormalizeTitle(a), NormalizeTitle(b)
	if na == "" || nb == "" {
		return false
	}
	return strings.Contains(na, nb) || strings.Contains(nb, na)
}

// popularTie reports whether the top two candidates tie on score (so the more
// popular was chosen and should be flagged).
func popularTie(ranked []scored) bool {
	return len(ranked) > 1 && ranked[1].score == ranked[0].score
}

// tiedExact returns every candidate that ties the top score with exact
// title+artist matches.
func tiedExact(ranked []scored) []model.Track {
	if len(ranked) == 0 || !ranked[0].title || !ranked[0].artist {
		return nil
	}
	best := ranked[0].score
	var out []model.Track
	for _, r := range ranked {
		if r.score == best && r.title && r.artist {
			out = append(out, r.track)
		}
	}
	return out
}

// dominantISRCGroup groups tied candidates by ISRC and returns the group that
// represents "the recording", if any:
//
//   - All tied candidates share one ISRC → same recording, any group size.
//     (Live regression: Iration "Automatic" appears on two releases with the
//     same ISRC and only release dates differing — that is one recording,
//     not ambiguity.)
//   - Otherwise a dominant group of at least 3 members holding at least 75%
//     of the tie wins. (Live regression: "Mass Appeal", 8 compilations of one
//     recording plus a lone clean edit with its own ISRC.)
//
// A genuine multi-recording split with no dominant group returns nil so the
// caller is asked to pick.
func dominantISRCGroup(tracks []model.Track) ([]model.Track, string) {
	groups := map[string][]model.Track{}
	for _, t := range tracks {
		key := t.ISRC
		if key == "" {
			key = "\x00" + t.URI // no ISRC: a singleton group of its own
		}
		groups[key] = append(groups[key], t)
	}
	for isrc, g := range groups {
		if isrc[0] == '\x00' {
			continue
		}
		if len(g) == len(tracks) || (len(g) >= 3 && len(g)*4 >= len(tracks)*3) {
			return g, isrc
		}
	}
	return nil, ""
}

// earliestRelease picks the track with the earliest album release date
// (lexicographic on ISO dates), breaking ties by URI for determinism.
func earliestRelease(tracks []model.Track) model.Track {
	best := tracks[0]
	for _, t := range tracks[1:] {
		bd, td := best.Album.ReleaseDate, t.Album.ReleaseDate
		switch {
		case td != "" && (bd == "" || td < bd):
			best = t
		case td == bd && t.URI < best.URI:
			best = t
		}
	}
	return best
}

func topTracks(ranked []scored, n int) []model.Track {
	if n > len(ranked) {
		n = len(ranked)
	}
	out := make([]model.Track, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ranked[i].track)
	}
	return out
}

// fieldQuery mirrors spotify.FieldQuery here to avoid an import cycle
// (spotify imports nothing from resolve, and resolve must not import spotify's
// client just for a string builder).
func fieldQuery(artist, title, album string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString(`track:"`)
		b.WriteString(strings.ReplaceAll(title, `"`, ""))
		b.WriteString(`" `)
	}
	if artist != "" {
		b.WriteString(`artist:"`)
		b.WriteString(strings.ReplaceAll(artist, `"`, ""))
		b.WriteString(`" `)
	}
	if album != "" {
		b.WriteString(`album:"`)
		b.WriteString(strings.ReplaceAll(album, `"`, ""))
		b.WriteString(`" `)
	}
	return strings.TrimSpace(b.String())
}
