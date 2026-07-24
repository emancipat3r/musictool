package auth

import (
	"net/url"
	"testing"
	"time"
)

// PKCE rotates refresh tokens. A refresh response that carries a new
// refresh_token must replace the old one; a response that omits it must keep the
// current one. Getting this wrong silently kills auth on a later run.
func TestApplyRefreshRotation(t *testing.T) {
	tok := &Token{RefreshToken: "old-refresh", AccessToken: "old-access"}

	// Rotation: new refresh token present -> replace.
	tok.applyRefreshResponse(tokenResponse{
		AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600, TokenType: "Bearer",
	})
	if tok.RefreshToken != "new-refresh" {
		t.Fatalf("refresh not rotated: %q", tok.RefreshToken)
	}
	if tok.AccessToken != "new-access" {
		t.Fatalf("access not updated: %q", tok.AccessToken)
	}
	if tok.expired() {
		t.Fatal("freshly refreshed token reports expired")
	}

	// No refresh token in response -> keep the current one.
	tok.applyRefreshResponse(tokenResponse{AccessToken: "another-access", ExpiresIn: 3600})
	if tok.RefreshToken != "new-refresh" {
		t.Fatalf("refresh should be preserved when omitted, got %q", tok.RefreshToken)
	}
}

func TestTokenExpiry(t *testing.T) {
	tok := &Token{AccessToken: "a", ExpiresAt: time.Now().Add(2 * time.Hour)}
	if tok.expired() {
		t.Fatal("token valid for 2h should not be expired")
	}
	tok.ExpiresAt = time.Now().Add(10 * time.Second) // inside earlyRefresh window
	if !tok.expired() {
		t.Fatal("token within early-refresh window should be treated as expired")
	}
}

func TestParsePasted(t *testing.T) {
	cases := []struct {
		in         string
		wantCode   string
		wantState  string
	}{
		{"http://127.0.0.1:8888/callback?code=ABC&state=XYZ", "ABC", "XYZ"},
		{"code=DEF&state=QRS", "DEF", "QRS"},
		{"BARECODE123", "BARECODE123", ""},
		{"   ", "", ""},
	}
	for _, c := range cases {
		code, state := parsePasted(c.in)
		if code != c.wantCode || state != c.wantState {
			t.Errorf("parsePasted(%q) = (%q,%q), want (%q,%q)", c.in, code, state, c.wantCode, c.wantState)
		}
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	got := buildAuthorizeURL("client123", "state456", "challenge789")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("client_id") != "client123" || q.Get("state") != "state456" || q.Get("code_challenge") != "challenge789" {
		t.Fatalf("params wrong: %v", q)
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:8888/callback" {
		t.Fatalf("redirect_uri = %q (must be the 127.0.0.1 loopback literal)", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", q.Get("response_type"))
	}
}
