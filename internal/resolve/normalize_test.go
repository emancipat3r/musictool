package resolve

import "testing"

func TestNormalizeTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Smells Like Teen Spirit", "smells like teen spirit"},
		{"Bohemian Rhapsody - Remastered 2011", "bohemian rhapsody"},
		{"Come As You Are (Remastered)", "come as you are"},
		{"No Woman No Cry (feat. The Wailers)", "no woman no cry"},
		{"Redemption Song - Band Version", "redemption song"},
		{"Song ft. Someone", "song"},
		{"Café del Mar", "cafe del mar"},
		{"Motörhead", "motorhead"},
		{"Sigur Rós - Untitled #1", "sigur ros untitled 1"},
		{"  Mixed   Up   Spaces ", "mixed up spaces"},
		{"Track (2019 Remaster)", "track"},
		{"Hello (with Adele)", "hello"},
	}
	for _, c := range cases {
		if got := NormalizeTitle(c.in); got != c.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeArtist(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Beyoncé", "beyonce"},
		{"AC/DC", "ac dc"},
		{"Sigur Rós", "sigur ros"},
		{"  Tribal   Seeds ", "tribal seeds"},
	}
	for _, c := range cases {
		if got := NormalizeArtist(c.in); got != c.want {
			t.Errorf("NormalizeArtist(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Artist normalization must NOT strip version-like words (a band could be named
// "Live" or "Mono").
func TestNormalizeArtistKeepsWords(t *testing.T) {
	if got := NormalizeArtist("Live"); got != "live" {
		t.Errorf("artist 'Live' normalized to %q", got)
	}
}
