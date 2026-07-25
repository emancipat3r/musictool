package dashboard

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emancipat3r/spotifytool/internal/logx"
)

// TermProxy is an auth-injecting reverse proxy in front of the Zellij web
// client. Zellij's web auth cannot be disabled, so the proxy performs the
// token login itself (POST /command/login with the token captured on the
// shared volume), holds the resulting session cookie server-side, and attaches
// it to every forwarded request, including the terminal WebSocket upgrade.
// Browsers inside the LAN/tailnet perimeter get a zero-login terminal.
//
// The upstream speaks HTTPS with a self-signed cert on the compose network, so
// certificate verification is intentionally skipped for this hop only.
type TermProxy struct {
	upstream  *url.URL
	tokenPath string
	transport *http.Transport
	proxy     *httputil.ReverseProxy

	mu      sync.Mutex
	cookies []*http.Cookie
}

// NewTermProxy builds the proxy for upstream (e.g. https://sandbox:8082),
// reading the login token from tokenPath at login time.
func NewTermProxy(upstream, tokenPath string) (*TermProxy, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("parse zellij upstream %q: %w", upstream, err)
	}
	tp := &TermProxy{
		upstream:  u,
		tokenPath: tokenPath,
		transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // self-signed internal hop
		},
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = tp.transport
	baseDirector := rp.Director
	rp.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = u.Host
		for _, c := range tp.snapshotCookies() {
			r.AddCookie(c)
		}
	}
	rp.ModifyResponse = tp.onResponse
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logx.Errorf("terminal proxy: upstream error: %v", err)
		http.Error(w, "zellij web upstream unreachable (is the sandbox container up?)", http.StatusBadGateway)
	}
	tp.proxy = rp
	return tp, nil
}

func (tp *TermProxy) snapshotCookies() []*http.Cookie {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return append([]*http.Cookie(nil), tp.cookies...)
}

// onResponse absorbs upstream cookies into the server-side jar (they never
// reach the browser) and resets the session on auth failures so the next
// request re-logs-in.
func (tp *TermProxy) onResponse(res *http.Response) error {
	if sc := res.Cookies(); len(sc) > 0 {
		tp.mu.Lock()
		for _, c := range sc {
			tp.upsertCookieLocked(c)
		}
		tp.mu.Unlock()
		res.Header.Del("Set-Cookie")
	}
	if res.StatusCode == http.StatusUnauthorized {
		logx.Debugf("terminal proxy: upstream 401; resetting session")
		tp.mu.Lock()
		tp.cookies = nil
		tp.mu.Unlock()
	}
	return nil
}

func (tp *TermProxy) upsertCookieLocked(c *http.Cookie) {
	for i, existing := range tp.cookies {
		if existing.Name == c.Name {
			tp.cookies[i] = c
			return
		}
	}
	tp.cookies = append(tp.cookies, c)
}

// ensureLogin performs the token login when no session cookie is held.
func (tp *TermProxy) ensureLogin() error {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if len(tp.cookies) > 0 {
		return nil
	}
	raw, err := os.ReadFile(tp.tokenPath)
	if err != nil {
		return fmt.Errorf("no zellij token at %s (sandbox mints it on boot): %w", tp.tokenPath, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return fmt.Errorf("zellij token file %s is empty", tp.tokenPath)
	}

	body, _ := json.Marshal(map[string]any{"auth_token": token, "remember_me": true})
	loginURL := tp.upstream.ResolveReference(&url.URL{Path: "/command/login"}).String()
	req, err := http.NewRequest(http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Transport: tp.transport, Timeout: 10 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("zellij login: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("zellij rejected the stored token (revoked?); re-mint with `zellij web --create-token` in the sandbox")
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("zellij login failed: status %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		tp.upsertCookieLocked(c)
	}
	if len(tp.cookies) == 0 {
		return fmt.Errorf("zellij login returned no session cookie")
	}
	logx.Infof("terminal proxy: authenticated to zellij web")
	return nil
}

// ServeHTTP logs in lazily, then forwards (WebSocket upgrades included —
// httputil.ReverseProxy handles Connection: Upgrade passthrough).
func (tp *TermProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := tp.ensureLogin(); err != nil {
		logx.Errorf("terminal proxy: %v", err)
		http.Error(w, "terminal auth bootstrap failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	tp.proxy.ServeHTTP(w, r)
}
