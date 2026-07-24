// Package lastfm is a minimal client for the free Last.fm API, used only as a
// discovery seed: per-artist tags and similar artists. It replaces Spotify's
// deprecated related-artists/recommendations surfaces. Subjective qualities
// ("atmospheric") are tag queries here, never audio-feature math.
package lastfm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/emancipat3r/spotifytool/internal/apperr"
)

const apiBase = "https://ws.audioscrobbler.com/2.0/"

// Client is a Last.fm API client. A zero key disables it (methods return a
// clear error) so the rest of the tool works without discovery seeds.
type Client struct {
	key  string
	http *http.Client
}

// New builds a client. key may be empty.
func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 20 * time.Second}}
}

// Enabled reports whether an API key is configured.
func (c *Client) Enabled() bool { return c.key != "" }

func (c *Client) get(ctx context.Context, method string, params url.Values, out any) error {
	if c.key == "" {
		return apperr.API(errors.New("LASTFM_API_KEY not set; discovery seeds unavailable"))
	}
	params.Set("method", method)
	params.Set("api_key", c.key)
	params.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return apperr.API(fmt.Errorf("last.fm unreachable: %w", err))
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return apperr.API(fmt.Errorf("last.fm %s: status %d", method, res.StatusCode))
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return apperr.API(fmt.Errorf("decode last.fm %s: %w", method, err))
	}
	return nil
}

// TopTags returns the top tags for an artist, ordered by weight, capped at
// limit.
func (c *Client) TopTags(ctx context.Context, artist string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	var resp struct {
		TopTags struct {
			Tag []struct {
				Name string `json:"name"`
			} `json:"tag"`
		} `json:"toptags"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	p := url.Values{"artist": {artist}, "autocorrect": {"1"}}
	if err := c.get(ctx, "artist.gettoptags", p, &resp); err != nil {
		return nil, err
	}
	if resp.Error != 0 {
		return nil, apperr.API(fmt.Errorf("last.fm: %s", resp.Message))
	}
	out := make([]string, 0, limit)
	for _, t := range resp.TopTags.Tag {
		if t.Name == "" {
			continue
		}
		out = append(out, t.Name)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// SimilarArtists returns artist names similar to the given artist, capped at
// limit — the discovery seed replacing Spotify's dead related-artists endpoint.
func (c *Client) SimilarArtists(ctx context.Context, artist string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	var resp struct {
		SimilarArtists struct {
			Artist []struct {
				Name  string `json:"name"`
				Match string `json:"match"`
			} `json:"artist"`
		} `json:"similarartists"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	p := url.Values{"artist": {artist}, "autocorrect": {"1"}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "artist.getsimilar", p, &resp); err != nil {
		return nil, err
	}
	if resp.Error != 0 {
		return nil, apperr.API(fmt.Errorf("last.fm: %s", resp.Message))
	}
	out := make([]string, 0, limit)
	for _, a := range resp.SimilarArtists.Artist {
		if a.Name == "" {
			continue
		}
		out = append(out, a.Name)
	}
	return out, nil
}
