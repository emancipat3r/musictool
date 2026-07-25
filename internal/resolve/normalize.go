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
