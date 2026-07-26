// Package resolve is the deterministic translation of curated {artist,title}
// picks into exact Spotify track URIs — the core differentiator. Given the same
// inputs and cache it produces the same outputs.
package resolve

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Version/edition noise stripped from titles before comparison. These are the
// tags that make "Song - Remastered 2011" and "Song" the same recording for
// curation purposes.
var (
	// Parenthetical or bracketed feat credits: (feat. X), [featuring X], (with X).
	reFeatParen = regexp.MustCompile(`(?i)[\(\[]\s*(feat\.?|featuring|ft\.?|with)\b[^)\]]*[\)\]]`)
	// Trailing "feat. X" without brackets, to end of string.
	reFeatTrail = regexp.MustCompile(`(?i)\s*[-–]?\s*(feat\.?|featuring|ft\.?)\b.*$`)
	// " - <edition/version>" dash-suffix noise.
	reDashTag = regexp.MustCompile(`(?i)\s+[-–]\s+.*(remaster(ed)?|re-?master|mono|stereo|radio edit|single version|album version|album mix|bonus track|deluxe|expanded|anniversary|edit|version|mix|live|acoustic|instrumental|demo|re-?recorded|taylor'?s version)\b.*$`)
	// Parenthetical edition noise: (Remastered), (2011 Remaster), (Deluxe Edition)...
	reParenTag = regexp.MustCompile(`(?i)[\(\[]\s*[^)\]]*(remaster(ed)?|mono|stereo|radio edit|single version|album version|bonus track|deluxe|expanded|anniversary|re-?recorded|taylor'?s version)[^)\]]*[\)\]]`)
	rePunct    = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)
	reSpace    = regexp.MustCompile(`\s+`)
)

// NormalizeTitle lowercases, strips feat credits and edition/version tags,
// removes punctuation and diacritics, and collapses whitespace.
func NormalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reFeatParen.ReplaceAllString(s, " ")
	s = reParenTag.ReplaceAllString(s, " ")
	s = reDashTag.ReplaceAllString(s, " ")
	s = reFeatTrail.ReplaceAllString(s, " ")
	return finishNormalize(s)
}

// NormalizeArtist lowercases, strips diacritics/punctuation, and collapses
// whitespace. It intentionally does not strip version tags.
func NormalizeArtist(s string) string {
	return finishNormalize(strings.ToLower(strings.TrimSpace(s)))
}

// NormalizeVerbatim lowercases and strips punctuation/diacritics but keeps
// feat credits and version tags. Used to tell a verbatim title match ("Time
// Bomb") apart from a tag-stripped one ("Time Bomb - Live") after both
// normalize equal under NormalizeTitle.
func NormalizeVerbatim(s string) string {
	return finishNormalize(strings.ToLower(strings.TrimSpace(s)))
}

// Remaster tags are version-neutral: the canonical catalog cut of an older
// record is almost always titled "X - Remastered YYYY", while soundtrack and
// compilation releases carry untagged titles. Stripping ONLY remaster tags
// before the verbatim comparison keeps the verbatim bonus from systematically
// rewarding the non-canonical release (live regression: "Master of Puppets"
// resolved to the Stranger Things soundtrack over the 1986 album).
var (
	reRemasterParen = regexp.MustCompile(`(?i)[\(\[][^)\]]*re-?master[^)\]]*[\)\]]`)
	reRemasterDash  = regexp.MustCompile(`(?i)\s+[-–]\s+[^-–]*re-?master.*$`)
)

// VerbatimKey is NormalizeVerbatim modulo remaster tags.
func VerbatimKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reRemasterParen.ReplaceAllString(s, " ")
	s = reRemasterDash.ReplaceAllString(s, " ")
	return finishNormalize(s)
}

// albumNonCanonicalMarkers flag releases that are almost never the canonical
// home of a recording: soundtracks, hits compilations, karaoke/tribute fodder.
var albumNonCanonicalMarkers = []string{
	"soundtrack", "motion picture", "music from the", "greatest hits",
	"best of", "very best", "anthology", "now that s what", "the singles",
	"karaoke", "tribute", "tv series", "compilation",
}

// AlbumNonCanonical reports whether the album name carries a marker of a
// non-canonical release.
func AlbumNonCanonical(album string) bool {
	a := NormalizeVerbatim(album)
	for _, m := range albumNonCanonicalMarkers {
		if strings.Contains(a, m) {
			return true
		}
	}
	return false
}

// editionMarkers adorn re-releases of a canonical album (same recording,
// noisier packaging).
var editionMarkers = []string{
	"deluxe", "anniversary", "expanded", "remaster", "edition", "bonus",
	"legacy", "reissue", "super", "redux",
}

// EditionAdornment counts edition markers in an album name; the canonical
// release is the least adorned.
func EditionAdornment(album string) int {
	a := NormalizeVerbatim(album)
	n := 0
	for _, m := range editionMarkers {
		if strings.Contains(a, m) {
			n++
		}
	}
	return n
}

// variantWords are performance-variant markers: a candidate carrying one the
// query didn't ask for is almost certainly a different recording of the song.
// Remaster tags are deliberately absent (same recording).
var variantWords = []string{
	"live", "acoustic", "instrumental", "unplugged", "demo", "remix",
	"karaoke", "cover", "reprise", "sped up", "slowed", "medley",
}

// HasUnwantedVariant reports whether the candidate title carries a
// performance-variant marker that the query title does not.
func HasUnwantedVariant(queryTitle, candidateTitle string) bool {
	q := NormalizeVerbatim(queryTitle)
	c := NormalizeVerbatim(candidateTitle)
	for _, w := range variantWords {
		if containsWord(c, w) && !containsWord(q, w) {
			return true
		}
	}
	return false
}

// containsWord reports whether s contains w as a whole word (both already
// space-normalized).
func containsWord(s, w string) bool {
	return s == w || strings.HasPrefix(s, w+" ") || strings.HasSuffix(s, " "+w) ||
		strings.Contains(s, " "+w+" ")
}

func finishNormalize(s string) string {
	s = stripDiacritics(s)
	s = rePunct.ReplaceAllString(s, " ")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// stripDiacritics decomposes to NFD and drops nonspacing combining marks, so
// "Motörhead" → "motorhead", "Sigur Rós" → "sigur ros".
func stripDiacritics(s string) string {
	d := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(d))
	for _, r := range d {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
