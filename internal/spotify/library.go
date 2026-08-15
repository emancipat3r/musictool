package spotify

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/emancipat3r/musictool/internal/model"
)

// parseTime parses an ISO-8601 timestamp, tolerating both second and
// millisecond precision; zero time on failure.
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// CurrentUser returns the authorized user (needed to create playlists).
func (c *Client) CurrentUser(ctx context.Context) (model.User, error) {
	var u apiUser
	if err := c.do(ctx, "GET", "/me", nil, &u); err != nil {
		return model.User{}, err
	}
	return model.User{ID: u.ID, DisplayName: u.DisplayName}, nil
}

// SavedTracks pages through the entire liked-songs library. Full track objects
// come from this endpoint because the batch get-several endpoints were removed.
func (c *Client) SavedTracks(ctx context.Context) ([]model.SavedTrack, error) {
	out := []model.SavedTrack{}
	next := "/me/tracks?limit=50"
	for next != "" {
		var page apiPaging[apiSavedTrack]
		if err := c.do(ctx, "GET", next, nil, &page); err != nil {
			return out, err
		}
		for _, it := range page.Items {
			if it.Track.ID == "" {
				continue
			}
			out = append(out, model.SavedTrack{
				Track:   it.Track.toModel(),
				SavedAt: parseTime(it.AddedAt),
			})
		}
		next = page.Next
	}
	return out, nil
}

// Playlists pages through the current user's playlists.
func (c *Client) Playlists(ctx context.Context) ([]model.Playlist, error) {
	out := []model.Playlist{}
	next := "/me/playlists?limit=50"
	for next != "" {
		var page apiPaging[apiPlaylist]
		if err := c.do(ctx, "GET", next, nil, &page); err != nil {
			return out, err
		}
		for _, p := range page.Items {
			out = append(out, p.toModel())
		}
		next = page.Next
	}
	return out, nil
}

// PlaylistByID fetches a single playlist's metadata.
func (c *Client) PlaylistByID(ctx context.Context, id string) (model.Playlist, error) {
	var p apiPlaylist
	if err := c.do(ctx, "GET", "/playlists/"+url.PathEscape(id), nil, &p); err != nil {
		return model.Playlist{}, err
	}
	return p.toModel(), nil
}

// PlaylistTracks pages through a playlist's tracks in order. Uses the /items
// endpoint (Feb 2026: /playlists/{id}/tracks returns 403; live-verified).
func (c *Client) PlaylistTracks(ctx context.Context, id string) ([]model.Track, error) {
	out := []model.Track{}
	next := fmt.Sprintf("/playlists/%s/items?limit=100", url.PathEscape(id))
	for next != "" {
		var page apiPaging[apiPlaylistTrackItem]
		if err := c.do(ctx, "GET", next, nil, &page); err != nil {
			return out, err
		}
		for _, it := range page.Items {
			if it.IsLocal || it.Item.ID == "" {
				continue
			}
			out = append(out, it.Item.toModel())
		}
		next = page.Next
	}
	return out, nil
}

// RecentlyPlayed returns up to the last 50 plays Spotify exposes. History beyond
// that must be accumulated locally by repeated sync calls.
func (c *Client) RecentlyPlayed(ctx context.Context) ([]model.PlayEvent, error) {
	var page apiPaging[apiRecentItem]
	if err := c.do(ctx, "GET", "/me/player/recently-played?limit=50", nil, &page); err != nil {
		return nil, err
	}
	out := make([]model.PlayEvent, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, model.PlayEvent{
			TrackID:  it.Track.ID,
			URI:      it.Track.URI,
			PlayedAt: parseTime(it.PlayedAt),
			Title:    it.Track.Name,
			Artist:   it.Track.toModel().ArtistName(),
		})
	}
	return out, nil
}

// TopTracks returns the user's top tracks for the given time range
// (short_term|medium_term|long_term).
func (c *Client) TopTracks(ctx context.Context, timeRange string, limit int) ([]model.Track, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if timeRange == "" {
		timeRange = "medium_term"
	}
	var page apiPaging[apiTrack]
	q := fmt.Sprintf("/me/top/tracks?limit=%d&time_range=%s", limit, url.QueryEscape(timeRange))
	if err := c.do(ctx, "GET", q, nil, &page); err != nil {
		return nil, err
	}
	out := make([]model.Track, 0, len(page.Items))
	for _, t := range page.Items {
		out = append(out, t.toModel())
	}
	return out, nil
}
