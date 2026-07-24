package auth

import (
	"regexp"
	"testing"
)

// Known-answer test from RFC 7636 Appendix B: the verifier
// "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" produces the challenge
// "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM".
func TestS256ChallengeRFC7636(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := S256Challenge(verifier); got != want {
		t.Fatalf("S256Challenge = %q, want %q", got, want)
	}
}

var unreserved = regexp.MustCompile(`^[A-Za-z0-9\-._~]+$`)

func TestNewPKCE(t *testing.T) {
	for i := 0; i < 100; i++ {
		p, err := NewPKCE()
		if err != nil {
			t.Fatalf("NewPKCE: %v", err)
		}
		if p.Method != "S256" {
			t.Fatalf("method = %q, want S256", p.Method)
		}
		if l := len(p.Verifier); l < 43 || l > 128 {
			t.Fatalf("verifier length %d out of RFC range 43-128", l)
		}
		if !unreserved.MatchString(p.Verifier) {
			t.Fatalf("verifier has non-unreserved chars: %q", p.Verifier)
		}
		if p.Challenge != S256Challenge(p.Verifier) {
			t.Fatalf("challenge does not match verifier")
		}
	}
}

func TestVerifiersAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := NewPKCE()
		if err != nil {
			t.Fatal(err)
		}
		if seen[p.Verifier] {
			t.Fatal("duplicate verifier generated")
		}
		seen[p.Verifier] = true
	}
}

func TestRandomStateNonEmptyUnique(t *testing.T) {
	a, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || b == "" {
		t.Fatal("empty state")
	}
	if a == b {
		t.Fatal("state not unique")
	}
}
