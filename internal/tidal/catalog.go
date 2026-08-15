package tidal

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/emancipat3r/musictool/internal/model"
)

// The attrs structs mirror only the fields spotifytool consumes from TIDAL
// JSON:API resources. They are converted to model types immediately so nothing
// upstream leaks a raw API object into the store or the MCP surface.

type trackAttrs struct {
	Title      string  `json:"title"`
	ISRC       string  `json:"isrc"`
	Duration   string  `json:"duration"`   // ISO-8601, e.g. "PT3M35S"
	Popularity float64 `json:"popularity"` // 0..1
	Version    string  `json:"version"`    // variant label, e.g. "Live"
}

type artistAttrs struct {
	Name string `json:"name"`
}

type albumAttrs struct {
	Title       string `json:"title"`
	ReleaseDate string `json:"releaseDate"`
}

// parseISODurationMs converts an ISO-8601 duration (PT1H2M3.5S) to
// milliseconds. Zero on parse failure — a missing duration only weakens a
// resolver tiebreak, it must never fail a sync.
func parseISODurationMs(s string) int {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "PT") {
		return 0
	}
	s = strings.TrimPrefix(s, "PT")
	total := 0.0
	num := ""
	for _, r := range s {
		switch {
		case (r >= '0' && r <= '9') || r == '.':
			num += string(r)
		case r == 'H' || r == 'M' || r == 'S':
			v, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0
			}
			switch r {
			case 'H':
				total += v * 3600
			case 'M':
				total += v * 60
			case 'S':
				total += v
			}
			num = ""
		default:
			return 0
		}
	}
	if num != "" {
		return 0 // trailing digits without a unit
	}
	return int(math.Round(total * 1000))
}

// popularity100 scales TIDAL's 0..1 float to the model's 0..100 int.
func popularity100(p float64) int {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 100
	}
	return int(math.Round(p * 100))
}

// buildTrack converts a tracks resource plus the included artists/albums into
// a model.Track. TIDAL keeps performance variants in a separate "version"
// attribute; it is folded into the title in parentheses so the resolver's
// variant detection (which works on titles) behaves identically across
// providers.
func buildTrack(r jaResource, included map[string]jaResource) model.Track {
	var a trackAttrs
	_ = json.Unmarshal(r.Attributes, &a)

	title := a.Title
	if a.Version != "" {
		title += " (" + a.Version + ")"
	}
	t := model.Track{
		ID:         r.ID,
		URI:        trackURI(r.ID),
		Title:      title,
		DurationMs: parseISODurationMs(a.Duration),
		Popularity: popularity100(a.Popularity),
		ISRC:       a.ISRC,
	}
	for _, ref := range r.relRefs("artists") {
		name := ""
		if inc, ok := included["artists/"+ref.ID]; ok {
			var aa artistAttrs
			_ = json.Unmarshal(inc.Attributes, &aa)
			name = aa.Name
		}
		t.Artists = append(t.Artists, model.Artist{ID: ref.ID, Name: name})
	}
	if refs := r.relRefs("albums"); len(refs) > 0 {
		t.Album = model.Album{ID: refs[0].ID}
		if inc, ok := included["albums/"+refs[0].ID]; ok {
			var al albumAttrs
			_ = json.Unmarshal(inc.Attributes, &al)
			t.Album.Name = al.Title
			t.Album.ReleaseDate = al.ReleaseDate
		}
	}
	return t
}

// includedIndex maps included resources by "type/id" for buildTrack lookups.
func includedIndex(doc *jaDocument) map[string]jaResource {
	idx := make(map[string]jaResource, len(doc.Included))
	for _, r := range doc.Included {
		idx[r.Type+"/"+r.ID] = r
	}
	return idx
}

// hydrateTracks fetches full track resources (with artists and albums) for the
// given ids, preserving input order. Unknown ids are dropped, not errors.
func (c *Client) hydrateTracks(ctx context.Context, ids []string) ([]model.Track, error) {
	const chunk = 20
	byID := map[string]model.Track{}
	for i := 0; i < len(ids); i += chunk {
		end := i + chunk
		if end > len(ids) {
			end = len(ids)
		}
		q := url.Values{
			"countryCode": {c.country},
			"include":     {"artists,albums"},
		}
		for _, id := range ids[i:end] {
			q.Add("filter[id]", id)
		}
		var doc jaDocument
		if err := c.do(ctx, "GET", "/tracks", q, nil, &doc); err != nil {
			return nil, err
		}
		idx := includedIndex(&doc)
		for _, r := range doc.dataResources() {
			byID[r.ID] = buildTrack(r, idx)
		}
	}
	out := make([]model.Track, 0, len(ids))
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// SearchTracks runs a raw text search and returns hydrated candidate tracks.
// (TIDAL has no field-filter query syntax; scoping happens in the resolver's
// scoring, which sees full metadata.)
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]model.Track, error) {
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	q := url.Values{
		"filter[query]": {query},
		"countryCode":   {c.country},
		"include":       {"tracks"},
	}
	var doc jaDocument
	if err := c.do(ctx, "GET", "/searchResults", q, nil, &doc); err != nil {
		return nil, err
	}
	var ids []string
	for _, sr := range doc.dataResources() {
		for _, ref := range sr.relRefs("tracks") {
			ids = append(ids, ref.ID)
			if len(ids) >= limit {
				break
			}
		}
	}
	// Fall back to included track resources when the relationship list is
	// absent (response shape varies with include handling).
	if len(ids) == 0 {
		for _, inc := range doc.Included {
			if inc.Type == "tracks" {
				ids = append(ids, inc.ID)
				if len(ids) >= limit {
					break
				}
			}
		}
	}
	return c.hydrateTracks(ctx, ids)
}

// SearchPick is the resolver's search path. TIDAL search is free-text, so the
// pick's fields are joined; the resolver's normalized scoring does the
// disambiguation the query string cannot.
func (c *Client) SearchPick(ctx context.Context, artist, title, album string, limit int) ([]model.Track, error) {
	parts := make([]string, 0, 3)
	for _, p := range []string{artist, title, album} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	return c.SearchTracks(ctx, strings.Join(parts, " "), limit)
}
