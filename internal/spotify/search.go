package spotify

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/emancipat3r/musictool/internal/model"
)

// SearchTracks searches with Spotify field filters, never free text. The
// resolver builds queries like `track:"..." artist:"..."` so scoring runs
// against tightly-scoped candidates.
//
// Feb 2026: dev-mode apps get a hard cap of limit=10 on /search (11+ returns
// 400 Invalid limit; live-verified 2026-07-25), so the cap is enforced here.
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]model.Track, error) {
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	q := url.Values{
		"q":     {query},
		"type":  {"track"},
		"limit": {fmt.Sprintf("%d", limit)},
	}
	var resp struct {
		Tracks apiPaging[apiTrack] `json:"tracks"`
	}
	if err := c.do(ctx, "GET", "/search?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]model.Track, 0, len(resp.Tracks.Items))
	for _, t := range resp.Tracks.Items {
		if t.ID == "" {
			continue
		}
		out = append(out, t.toModel())
	}
	return out, nil
}

// FieldQuery builds a filtered search string from a curated pick. Quoting the
// field values keeps multi-word titles/artists intact.
func FieldQuery(artist, title, album string) string {
	var b strings.Builder
	if title != "" {
		fmt.Fprintf(&b, `track:"%s" `, escapeQuotes(title))
	}
	if artist != "" {
		fmt.Fprintf(&b, `artist:"%s" `, escapeQuotes(artist))
	}
	if album != "" {
		fmt.Fprintf(&b, `album:"%s" `, escapeQuotes(album))
	}
	return strings.TrimSpace(b.String())
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}
