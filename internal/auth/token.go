package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emancipat3r/musictool/internal/apperr"
	"github.com/emancipat3r/musictool/internal/logx"
)

// earlyRefresh is how long before nominal expiry we proactively refresh, to
// avoid racing a token that expires mid-request.
const earlyRefresh = 60 * time.Second

// Token is the persisted token cache written to token.json (mode 0600). It is
// never logged and never printed to stdout.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// expired reports whether the access token is at or past its refresh threshold.
func (t *Token) expired() bool {
	return t.AccessToken == "" || time.Now().After(t.ExpiresAt.Add(-earlyRefresh))
}

// LoadToken reads and parses the token cache. A missing file is reported as an
// auth error so callers can tell the user to run `auth`.
func LoadToken(path string) (*Token, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, apperr.Auth(fmt.Errorf("no token cache at %s; run `musictool auth`", path))
		}
		return nil, apperr.Auth(fmt.Errorf("read token cache: %w", err))
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, apperr.Auth(fmt.Errorf("parse token cache %s: %w", path, err))
	}
	return &t, nil
}

// save atomically writes the token cache with 0600 permissions.
func (t *Token) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	// Ensure mode is 0600 even if the file pre-existed with looser perms.
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// applyRefreshResponse folds a /api/token response into the token, preserving
// the old refresh token when the response omits one.
//
// PKCE ROTATES REFRESH TOKENS: Spotify may return a new refresh_token on
// refresh, and the old one stops working. We must persist the new one or auth
// silently dies on the next run. When the field is absent we keep the current
// value.
func (t *Token) applyRefreshResponse(r tokenResponse) {
	t.AccessToken = r.AccessToken
	if r.TokenType != "" {
		t.TokenType = r.TokenType
	}
	if r.Scope != "" {
		t.Scope = r.Scope
	}
	if r.RefreshToken != "" {
		t.RefreshToken = r.RefreshToken
	}
	exp := r.ExpiresIn
	if exp <= 0 {
		exp = 3600
	}
	t.ExpiresAt = time.Now().Add(time.Duration(exp) * time.Second)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// TokenSource loads, caches, and silently refreshes access tokens for
// non-interactive commands. It is safe for concurrent use (serve mode). The
// token endpoint is a parameter so the same machinery serves every provider
// (Spotify, TIDAL — both OAuth2 authorization-code + PKCE).
type TokenSource struct {
	clientID string
	tokenURL string
	path     string
	seed     string // *_REFRESH_TOKEN env bootstrap, if provided
	http     *http.Client

	mu  sync.Mutex
	tok *Token
}

// NewTokenSource builds a token source refreshing against tokenURL. If
// seedRefresh is non-empty (the headless *_REFRESH_TOKEN bootstrap) and no
// cache exists yet, it seeds a token from that refresh token on first use. The
// seed is also kept as a fallback: if the cached refresh token is rejected
// (revoked, or a restored volume holding a stale pre-rotation token), the seed
// gets one retry before giving up.
func NewTokenSource(clientID, tokenURL, path, seedRefresh string, hc *http.Client) *TokenSource {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	ts := &TokenSource{clientID: clientID, tokenURL: tokenURL, path: path, seed: seedRefresh, http: hc}
	if seedRefresh != "" {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			ts.tok = &Token{RefreshToken: seedRefresh, TokenType: "Bearer"}
		}
	}
	return ts
}

// AccessToken returns a valid bearer token, refreshing (and persisting rotation)
// as needed.
func (ts *TokenSource) AccessToken(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.tok == nil {
		t, err := LoadToken(ts.path)
		if err != nil {
			return "", err
		}
		ts.tok = t
	}
	if !ts.tok.expired() {
		return ts.tok.AccessToken, nil
	}
	if ts.tok.RefreshToken == "" {
		return "", apperr.Auth(errors.New("no refresh token; run `musictool auth`"))
	}
	if err := ts.refreshLocked(ctx); err != nil {
		return "", err
	}
	return ts.tok.AccessToken, nil
}

// refreshLocked performs the refresh_token grant and persists the result,
// retrying once with the env seed if the cached token is rejected. The caller
// must hold ts.mu.
func (ts *TokenSource) refreshLocked(ctx context.Context) error {
	err := ts.grantLocked(ctx, ts.tok.RefreshToken)
	if err != nil && ts.seed != "" && ts.seed != ts.tok.RefreshToken {
		logx.Errorf("cached refresh token rejected; retrying with the env-seeded refresh token")
		if seedErr := ts.grantLocked(ctx, ts.seed); seedErr == nil {
			return nil
		}
		return err // report the primary failure, not the fallback's
	}
	return err
}

// grantLocked runs one refresh_token grant with the given token and persists
// the (possibly rotated) result on success.
func (ts *TokenSource) grantLocked(ctx context.Context, refreshToken string) error {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ts.clientID},
	}
	resp, err := ts.postToken(ctx, form)
	if err != nil {
		return err
	}
	prevRefresh := ts.tok.RefreshToken
	ts.tok.RefreshToken = refreshToken // base for applyRefreshResponse's keep-if-omitted rule
	ts.tok.applyRefreshResponse(resp)
	if err := ts.tok.save(ts.path); err != nil {
		return apperr.Auth(fmt.Errorf("persist refreshed token: %w", err))
	}
	if resp.RefreshToken != "" && resp.RefreshToken != prevRefresh {
		logx.Debugf("refresh token rotated and persisted")
	}
	return nil
}

// postToken sends a refresh request via the shared helper.
func (ts *TokenSource) postToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	return doTokenRequest(ctx, ts.http, ts.tokenURL, form)
}

// doTokenRequest sends a form-encoded request to the token endpoint and decodes
// the response, mapping OAuth errors to auth failures. Shared by the refresh
// path and the interactive code-exchange path.
func doTokenRequest(ctx context.Context, hc *http.Client, tokenURL string, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := hc.Do(req)
	if err != nil {
		return tokenResponse{}, apperr.Auth(fmt.Errorf("token endpoint unreachable: %w", err))
	}
	defer res.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return tokenResponse{}, apperr.Auth(fmt.Errorf("decode token response (status %d): %w", res.StatusCode, err))
	}
	if tr.Error != "" || res.StatusCode >= 400 {
		return tokenResponse{}, apperr.Auth(fmt.Errorf("token grant failed (status %d): %s %s; run `musictool auth`",
			res.StatusCode, tr.Error, tr.ErrorDesc))
	}
	return tr, nil
}
