package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeZellij simulates the zellij web server: /command/login accepts the right
// token and sets a session cookie; everything else requires that cookie.
func fakeZellij(t *testing.T, wantToken string) (*httptest.Server, *int) {
	t.Helper()
	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/command/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AuthToken  string `json:"auth_token"`
			RememberMe bool   `json:"remember_me"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AuthToken != wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		logins++
		http.SetCookie(w, &http.Cookie{Name: "web_session_id", Value: "sess-abc", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("web_session_id")
		if err != nil || c.Value != "sess-abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("terminal page: authenticated"))
	})
	return httptest.NewTLSServer(mux), &logins
}

func TestTermProxyInjectsAuth(t *testing.T) {
	upstream, logins := fakeZellij(t, "tok-123")
	defer upstream.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "zellij-web-token.txt")
	if err := os.WriteFile(tokenPath, []byte("tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tp, err := NewTermProxy(upstream.URL, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(tp)
	defer front.Close()

	// First request: proxy must log in and serve the authenticated page.
	res, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	// The upstream session cookie must never reach the browser.
	if len(res.Cookies()) != 0 {
		t.Fatalf("proxy leaked cookies to the client: %v", res.Cookies())
	}

	// Second request reuses the held session: no second login.
	res2, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if *logins != 1 {
		t.Fatalf("logins = %d, want 1 (session should be reused)", *logins)
	}
}

func TestTermProxyBadToken(t *testing.T) {
	upstream, _ := fakeZellij(t, "right-token")
	defer upstream.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "zellij-web-token.txt")
	os.WriteFile(tokenPath, []byte("wrong-token"), 0o600)
	tp, err := NewTermProxy(upstream.URL, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	tp.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 with a clear bootstrap error", rr.Code)
	}
}

func TestTermProxyMissingTokenFile(t *testing.T) {
	upstream, _ := fakeZellij(t, "tok")
	defer upstream.Close()
	tp, err := NewTermProxy(upstream.URL, filepath.Join(t.TempDir(), "nope.txt"))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	tp.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
