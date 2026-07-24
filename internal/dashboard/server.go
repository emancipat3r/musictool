// Package dashboard is the read-only web UI over the SQLite store plus the
// taste-profile viewer/editor. It makes no model calls and never mutates music
// state — the profile editor is the sole write, per the PRD. Styling is
// gruvbox-dark with a coyote-tan accent.
package dashboard

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/emancipat3r/spotifytool/internal/config"
	"github.com/emancipat3r/spotifytool/internal/logx"
	"github.com/emancipat3r/spotifytool/internal/profile"
	"github.com/emancipat3r/spotifytool/internal/store"
)

// Dashboard serves the read-only UI.
type Dashboard struct {
	db   *store.Store
	cfg  config.Config
	tmpl *template.Template
}

// New builds a dashboard over the given store.
func New(db *store.Store, cfg config.Config) *Dashboard {
	return &Dashboard{
		db:   db,
		cfg:  cfg,
		tmpl: template.Must(template.New("page").Parse(pageTemplate)),
	}
}

// Handler returns the dashboard's http.Handler.
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleIndex)
	mux.HandleFunc("/profile", d.handleProfile)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

type pageData struct {
	Title       string
	Stats       store.LibraryStats
	Signals     store.RecentSignals
	Batches     []store.Batch
	PlayHistory []barRow
	Profile     string
	ShowProfile bool
	Saved       bool
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

	data := pageData{
		Title:       "spotifytool",
		Stats:       stats,
		Signals:     sig,
		Batches:     batches,
		PlayHistory: toBars(hist),
	}
	d.render(w, data)
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
		Profile:     content,
		ShowProfile: true,
		Saved:       r.URL.Query().Get("saved") == "1",
	})
}

func (d *Dashboard) render(w http.ResponseWriter, data pageData) {
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
