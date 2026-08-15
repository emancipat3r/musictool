// Package dashboard is the read-only web UI over the SQLite store plus the
// taste-profile viewer/editor. It makes no model calls and never mutates music
// state — the profile editor is the sole write, per the PRD. Styling is
// gruvbox-dark with a coyote-tan accent.
package dashboard

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emancipat3r/musictool/internal/config"
	"github.com/emancipat3r/musictool/internal/logx"
	"github.com/emancipat3r/musictool/internal/profile"
	"github.com/emancipat3r/musictool/internal/service"
	"github.com/emancipat3r/musictool/internal/store"
)

// Dashboard serves the read-only UI.
type Dashboard struct {
	svc  *service.Service
	db   *store.Store
	cfg  config.Config
	tmpl *template.Template
}

// New builds a dashboard over the service (queries via its store; votes via
// its Spotify write path).
func New(svc *service.Service, cfg config.Config) *Dashboard {
	return &Dashboard{
		svc: svc,
		db:  svc.DB,
		cfg: cfg,
		tmpl: template.Must(template.New("page").Funcs(template.FuncMap{
			"pct": func(n, max int) int {
				if max <= 0 {
					return 0
				}
				return n * 100 / max
			},
		}).Parse(pageTemplate)),
	}
}

// Handler returns the dashboard's http.Handler.
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleIndex)
	mux.HandleFunc("/profile", d.handleProfile)
	mux.HandleFunc("/terminal", d.handleTerminal)
	mux.HandleFunc("/vote", d.handleVote)
	mux.HandleFunc("/batch", d.handleBatch)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

// Pane names for pageData.Page (which pane the template renders).
const (
	pageDashboard = "dashboard"
	pageProfile   = "profile"
	pageTerminal  = "terminal"
)

type pageData struct {
	Title       string
	Page        string
	Stats       store.LibraryStats
	Signals     store.RecentSignals
	Batches     []store.Batch
	PlayHistory []barRow
	TopArtists  []store.ArtistCard
	RecentSaves []store.CoverRef
	BatchLabel  string
	BatchCovers []store.CoverRef
	SigPos      int
	SigNeg      int
	SigPosPct   int
	SigNegPct   int
	Profile     string
	Saved       bool
	TerminalURL string
	// TrackURIPrefix is the provider's track URI scheme prefix (e.g.
	// "spotify:track:"). The vote JS rebuilds full URIs from bare ids with it
	// (html/template mangles scheme URIs in *-uri attributes to #ZgotmplZ, so
	// tiles carry ids only).
	TrackURIPrefix string
}

type barRow struct {
	Label string
	Count int
	Pct   int
}

func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	stats, _ := d.db.Stats(ctx)
	sig, _ := d.db.Signals(ctx)
	batches, _ := d.db.ListBatches(ctx, 15)
	hist, _ := d.db.PlayHistoryDaily(ctx, 30)
	artists, _ := d.db.TopArtistCards(ctx, 10)
	saves, _ := d.db.RecentSaveCovers(ctx, 18)
	batchLabel, batchCovers, _ := d.db.LatestBatchCovers(ctx)

	// Signal balance: positive vs negative event counts for the diverging bar.
	sigPos := len(sig.NewSaves) + len(sig.Repeats) + len(sig.NewKeepers)
	sigNeg := len(sig.NewDislikes) + len(sig.NewSkips) + len(sig.IgnoredFromLastBatch)
	posPct, negPct := 0, 0
	if total := sigPos + sigNeg; total > 0 {
		negPct = sigNeg * 100 / total
		posPct = 100 - negPct
	}

	data := pageData{
		Title:       "dashboard",
		Page:        pageDashboard,
		Stats:       stats,
		Signals:     sig,
		Batches:     batches,
		PlayHistory: toBars(hist),
		TopArtists:  artists,
		RecentSaves: saves,
		BatchLabel:  batchLabel,
		BatchCovers: batchCovers,
		SigPos:      sigPos,
		SigNeg:      sigNeg,
		SigPosPct:   posPct,
		SigNegPct:   negPct,
		TerminalURL: d.terminalURLFor(r),
	}
	d.render(w, data)
}

// handleVote records an explicit vote: the canonical write goes to the real
// Keepers/Disliked playlist via the service, keeping Spotify the source of
// truth. This is the one music-state mutation the dashboard performs, at the
// user's explicit request.
func (d *Dashboard) handleVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		URI    string `json:"uri"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := d.svc.Vote(ctx, body.URI, body.Action)
	if err != nil {
		logx.Errorf("vote: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// handleBatch forwards a manual discovery-batch request to the sandbox's
// trigger endpoint, which types /discovery-batch into the live claude session
// (interactive, subscription-billed; no API usage anywhere).
func (d *Dashboard) handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if d.cfg.TriggerURL == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "trigger not configured (SPOTIFYTOOL_TRIGGER_URL)"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TriggerURL+"/run",
		strings.NewReader(`{"command":"/discovery-batch"}`))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		logx.Errorf("batch trigger: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "sandbox trigger unreachable"})
		return
	}
	defer res.Body.Close()
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

// terminalURLFor picks the terminal proxy address matching how the client
// reached us: TLS clients (browsers) get the TLS proxy; plain-HTTP clients
// (the companion app) get the plain proxy, keeping ws:// end to end so
// WebView TLS quirks never touch the terminal.
func (d *Dashboard) terminalURLFor(r *http.Request) string {
	if r.TLS == nil && d.cfg.TerminalURLHTTP != "" {
		return d.cfg.TerminalURLHTTP
	}
	return d.cfg.TerminalURL
}

// handleTerminal renders the service-hatch pane: the terminal proxy embedded,
// with an open-in-tab link.
func (d *Dashboard) handleTerminal(w http.ResponseWriter, r *http.Request) {
	d.render(w, pageData{
		Title:       "terminal",
		Page:        pageTerminal,
		TerminalURL: d.terminalURLFor(r),
	})
}

func (d *Dashboard) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if err := profile.Write(d.cfg.ProfilePath(), r.FormValue("content")); err != nil {
			http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		logx.Infof("taste profile updated via dashboard")
		http.Redirect(w, r, "/profile?saved=1", http.StatusSeeOther)
		return
	}
	content, _ := profile.Read(d.cfg.ProfilePath())
	d.render(w, pageData{
		Title:       "taste profile",
		Page:        pageProfile,
		Profile:     content,
		Saved:       r.URL.Query().Get("saved") == "1",
		TerminalURL: d.terminalURLFor(r),
	})
}

func (d *Dashboard) render(w http.ResponseWriter, data pageData) {
	data.TrackURIPrefix = d.svc.SP.TrackURI("")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.tmpl.Execute(w, data); err != nil {
		logx.Errorf("render dashboard: %v", err)
	}
}

func toBars(counts []store.Count) []barRow {
	max := 1
	for _, c := range counts {
		if c.Count > max {
			max = c.Count
		}
	}
	out := make([]barRow, 0, len(counts))
	for _, c := range counts {
		out = append(out, barRow{Label: c.Name, Count: c.Count, Pct: c.Count * 100 / max})
	}
	return out
}
