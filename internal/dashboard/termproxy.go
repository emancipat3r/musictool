package dashboard

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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
	session   string
	transport *http.Transport
	proxy     *httputil.ReverseProxy

	mu      sync.Mutex
	cookies []*http.Cookie
}

// SetSession names the zellij session the proxy deep-links into: requests to
// "/" redirect to "/{session}" so the web client attaches directly instead of
// showing its session picker (where a stray tap starts a NEW session).
func (tp *TermProxy) SetSession(name string) { tp.session = name }

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
		// Identity encoding so HTML responses can be inspected/injected.
		r.Header.Del("Accept-Encoding")
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
// reach the browser), resets the session on auth failures so the next
// request re-logs-in, and injects the diagnostic overlay into HTML pages.
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
	// Inject the on-screen diagnostics into the terminal page so client-side
	// failures are visible in a screenshot and mirrored to server logs.
	if res.StatusCode == 200 && strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return err
		}
		injected := bytes.Replace(body, []byte("</body>"), []byte(diagScript+"</body>"), 1)
		res.Body = io.NopCloser(bytes.NewReader(injected))
		res.ContentLength = int64(len(injected))
		res.Header.Set("Content-Length", fmt.Sprintf("%d", len(injected)))
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
	if tp.session != "" && r.URL.Path == "/" {
		http.Redirect(w, r, "/"+tp.session, http.StatusFound)
		return
	}
	// Neutralize xterm's WebGL renderer: zellij's terminal.js loads it
	// unconditionally and its context-loss handler just disposes (an upstream
	// TODO), which leaves a black canvas on Android WebView and other
	// environments with flaky WebGL. Serving a no-op stub makes xterm fall
	// back to its DOM renderer, which paints everywhere.
	if r.URL.Path == "/assets/addon-webgl.js" {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(webglStub))
		return
	}
	// Client diagnostics sink: the injected overlay POSTs its snapshots here
	// so `docker logs spotifytool-spotify` shows exactly what the client sees.
	if r.URL.Path == "/termdiag" && r.Method == http.MethodPost {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		logx.Infof("termdiag %s: %s", r.RemoteAddr, strings.ReplaceAll(string(b), "\n", " "))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := tp.ensureLogin(); err != nil {
		logx.Errorf("terminal proxy: %v", err)
		http.Error(w, "terminal auth bootstrap failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	tp.proxy.ServeHTTP(w, r)
}

// webglStub satisfies zellij web's `new WebglAddon.WebglAddon()` +
// loadAddon lifecycle without ever creating a WebGL context.
const webglStub = `// spotifytool stub: WebGL renderer disabled (breaks on Android WebView);
// xterm.js falls back to its DOM renderer.
(function () {
  function WebglAddon() {}
  WebglAddon.prototype.activate = function () {};
  WebglAddon.prototype.dispose = function () {};
  WebglAddon.prototype.onContextLoss = function () { return { dispose: function () {} }; };
  window.WebglAddon = { WebglAddon: WebglAddon };
})();
`

// diagScript is the injected on-screen diagnostic overlay: WebSocket states
// (constructor hooked), xterm DOM metrics, font status, and JS errors.
// Mirrors to POST /termdiag every 5s so `docker logs` captures it too.
const diagScript = `<script>
(function () {
  var errs = [];
  window.addEventListener('error', function (e) { errs.push((e.message||'err') + ' @' + (e.filename||'').split('/').pop() + ':' + e.lineno); });
  var sockets = [];
  var OW = window.WebSocket;
  window.WebSocket = function (url, p) {
    var s = p !== undefined ? new OW(url, p) : new OW(url);
    var rec = { url: String(url).split('?')[0], state: 'connecting' };
    sockets.push(rec);
    s.addEventListener('open', function () { rec.state = 'open'; });
    s.addEventListener('error', function () { rec.state = 'error'; });
    s.addEventListener('close', function (e) { rec.state = 'closed(' + e.code + ')'; });
    return s;
  };
  window.WebSocket.prototype = OW.prototype;
  ['CONNECTING','OPEN','CLOSING','CLOSED'].forEach(function(k){ window.WebSocket[k] = OW[k]; });

  // FIX (found via these diagnostics): zellij's style.css sizes body and
  // #terminal with 100vh, which collapses to 0 on Android WebView — xterm
  // then builds a ONE-ROW terminal. Pin heights in real pixels and dispatch
  // resize so the fit addon rebuilds the grid and zellij redraws.
  function fixHeight() {
    var t = document.getElementById('terminal');
    if (!t) return;
    var h = window.innerHeight;
    if (t.clientHeight < h - 40) {
      document.documentElement.style.height = h + 'px';
      document.body.style.height = h + 'px';
      document.body.style.margin = '0';
      t.style.height = h + 'px';
      t.style.minHeight = h + 'px';
      window.dispatchEvent(new Event('resize'));
    }
  }
  fixHeight();
  setInterval(fixHeight, 2000);
  window.addEventListener('orientationchange', function () { setTimeout(fixHeight, 400); });

  var div = document.createElement('div');
  div.style.cssText = 'position:fixed;top:4px;left:4px;right:4px;z-index:99999;background:rgba(20,20,20,.92);color:#9f9;font:11px/1.5 monospace;padding:8px;border:1px solid #4a4;border-radius:6px;pointer-events:none;white-space:pre-wrap;';
  document.body.appendChild(div);
  // The overlay is a diagnostic aid: fade it out once things settle.
  setTimeout(function () { div.style.display = 'none'; }, 25000);

  function snap() {
    var d = {};
    d.url = location.pathname;
    d.viewport = window.innerWidth + 'x' + window.innerHeight + ' dpr=' + devicePixelRatio;
    d.ws = sockets.map(function (s) { return s.url.split('/').slice(-2).join('/') + '=' + s.state; }).join(' | ') || 'none-yet';
    var termEl = document.getElementById('terminal');
    d.termEl = termEl ? (termEl.clientWidth + 'x' + termEl.clientHeight) : 'MISSING';
    var screen = document.querySelector('.xterm-screen');
    d.screen = screen ? (screen.clientWidth + 'x' + screen.clientHeight) : 'no-screen';
    var rows = document.querySelectorAll('.xterm-rows > div');
    d.rows = rows.length;
    if (rows.length) {
      var r0 = rows[0];
      d.row0 = 'h=' + r0.offsetHeight + ' chars=' + (r0.textContent||'').length + ' txt=' + JSON.stringify((r0.textContent||'').slice(0,40));
      var mid = rows[Math.floor(rows.length/2)];
      d.rowMid = 'chars=' + (mid.textContent||'').length;
    }
    var xt = document.querySelector('.xterm');
    if (xt) { var cs = getComputedStyle(xt); d.font = cs.fontFamily.slice(0,40) + ' ' + cs.fontSize; }
    d.fonts = document.fonts ? document.fonts.status : 'n/a';
    d.canvases = document.querySelectorAll('canvas').length;
    d.errs = errs.slice(-4).join(' ;; ') || 'none';
    return d;
  }

  function tick() {
    try {
      var d = snap();
      div.textContent = 'DIAG ' + new Date().toISOString().slice(11,19) + '\n' +
        Object.keys(d).map(function (k) { return k + ': ' + d[k]; }).join('\n');
      fetch('/termdiag', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify(d) }).catch(function(){});
    } catch (e) { div.textContent = 'DIAG error: ' + e.message; }
  }
  try { tick(); } catch (e) {}
  setInterval(tick, 4000);
})();
</script>`
