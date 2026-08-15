package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/emancipat3r/spotifytool/internal/apperr"
	"github.com/emancipat3r/spotifytool/internal/config"
	"github.com/emancipat3r/spotifytool/internal/logx"
)

// Options controls the interactive `auth` subcommand.
type Options struct {
	NoListener bool // OOB: print URL, read the redirected code from stdin
	NoBrowser  bool // print the URL instead of trying to launch a browser
	// PrintRefreshToken, when true, prints the refresh token to stdout so it
	// can be injected into container 2 as SPOTIFY_REFRESH_TOKEN. Off by default.
	PrintRefreshToken bool
}

// codeReader reads the pasted redirect/code in OOB mode. Injectable for tests.
type codeReader func(prompt string) (string, error)

// Endpoints carries one provider's OAuth2 parameters. Both supported providers
// speak Authorization Code + PKCE, so this is the only per-provider surface
// the flow needs.
type Endpoints struct {
	Provider     string // "spotify" | "tidal", for messages
	ClientID     string
	ClientIDEnv  string // env var name to cite when ClientID is empty
	AuthorizeURL string
	TokenURL     string
	Scopes       []string
	TokenPath    string
}

// Run performs the one-time interactive Authorization Code + PKCE flow and
// persists the token cache. It returns the resulting token so the caller can
// optionally surface the refresh token for headless bootstrap.
func Run(ctx context.Context, ep Endpoints, opts Options, readCode codeReader) (*Token, error) {
	if ep.ClientID == "" {
		return nil, apperr.Auth(fmt.Errorf("%s is not set", ep.ClientIDEnv))
	}
	pkce, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	authURL := buildAuthorizeURL(ep, state, pkce.Challenge)

	var code string
	if opts.NoListener {
		code, err = oobFlow(authURL, state, readCode)
	} else {
		code, err = listenerFlow(ctx, authURL, state, opts.NoBrowser)
	}
	if err != nil {
		return nil, err
	}

	tok, err := exchangeCode(ctx, ep, code, pkce.Verifier)
	if err != nil {
		return nil, err
	}
	if err := tok.save(ep.TokenPath); err != nil {
		return nil, apperr.Auth(fmt.Errorf("persist token: %w", err))
	}
	logx.Infof("authorized with %s; token cached at %s", ep.Provider, ep.TokenPath)
	return tok, nil
}

// buildAuthorizeURL constructs the /authorize URL with PKCE parameters.
func buildAuthorizeURL(ep Endpoints, state, challenge string) string {
	q := url.Values{
		"client_id":             {ep.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {config.RedirectURI},
		"scope":                 {strings.Join(ep.Scopes, " ")},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
	}
	return ep.AuthorizeURL + "?" + q.Encode()
}

// listenerFlow runs the one-shot loopback listener on 127.0.0.1:8888, opens the
// browser, and waits for Spotify to redirect back with ?code & ?state.
func listenerFlow(ctx context.Context, authURL, wantState string, noBrowser bool) (string, error) {
	ln, err := net.Listen("tcp", config.LoopbackAddr)
	if err != nil {
		return "", apperr.Auth(fmt.Errorf("bind %s (is auth already running?): %w", config.LoopbackAddr, err))
	}
	defer ln.Close()

	type result struct {
		code string
		err  error
	}
	resc := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writePage(w, "Authorization denied: "+e)
			resc <- result{err: apperr.Auth(fmt.Errorf("authorization denied: %s", e))}
			return
		}
		if q.Get("state") != wantState {
			writePage(w, "State mismatch. Possible CSRF; nothing was saved.")
			resc <- result{err: apperr.Auth(errors.New("state mismatch on callback"))}
			return
		}
		code := q.Get("code")
		if code == "" {
			writePage(w, "No authorization code in callback.")
			resc <- result{err: apperr.Auth(errors.New("no code in callback"))}
			return
		}
		writePage(w, "Authorized. You can close this tab and return to the terminal.")
		resc <- result{code: code}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	logx.Infof("open this URL to authorize (loopback listener on %s):\n\n%s\n", config.LoopbackAddr, authURL)
	if !noBrowser {
		tryOpenBrowser(authURL)
	}

	select {
	case <-ctx.Done():
		return "", apperr.Auth(ctx.Err())
	case <-time.After(5 * time.Minute):
		return "", apperr.Auth(errors.New("timed out waiting for authorization callback"))
	case res := <-resc:
		return res.code, res.err
	}
}

// oobFlow prints the authorize URL and reads back the pasted redirect URL (or
// bare code). Used on headless machines with no loopback browser.
func oobFlow(authURL, wantState string, readCode codeReader) (string, error) {
	if readCode == nil {
		return "", apperr.Auth(errors.New("no input reader for OOB flow"))
	}
	logx.Infof("open this URL anywhere, approve, then paste the FULL redirected URL (or just the code):\n\n%s\n", authURL)
	pasted, err := readCode("paste redirect URL or code: ")
	if err != nil {
		return "", apperr.Auth(fmt.Errorf("read pasted code: %w", err))
	}
	code, state := parsePasted(strings.TrimSpace(pasted))
	if code == "" {
		return "", apperr.Auth(errors.New("could not find a code in the pasted value"))
	}
	// State is only present if the full URL was pasted; validate when we have it.
	if state != "" && state != wantState {
		return "", apperr.Auth(errors.New("state mismatch in pasted redirect"))
	}
	return code, nil
}

// parsePasted extracts (code, state) from either a full redirect URL or a bare
// code string.
func parsePasted(s string) (code, state string) {
	if u, err := url.Parse(s); err == nil && (u.RawQuery != "" || u.Scheme != "") {
		q := u.Query()
		if c := q.Get("code"); c != "" {
			return c, q.Get("state")
		}
	}
	// Might be "code=...&state=..." without a scheme.
	if q, err := url.ParseQuery(s); err == nil {
		if c := q.Get("code"); c != "" {
			return c, q.Get("state")
		}
	}
	// Bare code.
	if s != "" && !strings.ContainsAny(s, " \t") {
		return s, ""
	}
	return "", ""
}

// exchangeCode swaps the authorization code + verifier for a token.
func exchangeCode(ctx context.Context, ep Endpoints, code, verifier string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {config.RedirectURI},
		"client_id":     {ep.ClientID},
		"code_verifier": {verifier},
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	tr, err := doTokenRequest(ctx, hc, ep.TokenURL, form)
	if err != nil {
		return nil, err
	}
	if tr.RefreshToken == "" {
		return nil, apperr.Auth(errors.New("token response contained no refresh_token"))
	}
	t := &Token{}
	t.applyRefreshResponse(tr)
	return t, nil
}

func writePage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:sans-serif;padding:3rem\"><h2>spotifytool</h2><p>%s</p></body></html>", msg)
}

// tryOpenBrowser makes a best-effort attempt to launch a browser; failure is
// non-fatal since the URL is also printed.
func tryOpenBrowser(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return
	}
	_ = exec.Command(cmd, args...).Start()
}
