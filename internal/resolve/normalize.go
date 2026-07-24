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
