package spotify

import (
	"context"
	"fmt"
	"net/url"

	"github.com/emancipat3r/spotifytool/internal/model"
)

// CreatePlaylist creates an empty playlist owned by userID and returns it.
func (c *Client) CreatePlaylist(ctx context.Context, userID, name, description string, public bool) (model.Playlist, error) {
	body := map[string]any{
		"name":        name,
		"description": description,
		"public":      public,
	}
	var p apiPlaylist
	path := fmt.Sprintf("/users/%s/playlists", url.PathEscape(userID))
	if err := c.do(ctx, "POST", path, body, &p); err != nil {
		return model.Playlist{}, err
	}
	return p.toModel(), nil
}

// AddTracks appends track URIs to a playlist in batches of 100 (the API max per
// request). Order is preserved.
func (c *Client) AddTracks(ctx context.Context, playlistID string, uris []string) error {
	const batch = 100
	path := fmt.Sprintf("/playlists/%s/tracks", url.PathEscape(playlistID))
	for i := 0; i < len(uris); i += batch {
		end := i + batch
		if end > len(uris) {
			end = len(uris)
		}
		body := map[string]any{"uris": uris[i:end]}
		if err := c.do(ctx, "POST", path, body, nil); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceTracks sets a playlist's contents to exactly uris (first ≤100 via PUT,
// remainder appended). Used when rebuilding a playlist deterministically.
func (c *Client) ReplaceTracks(ctx context.Context, playlistID string, uris []string) error {
	path := fmt.Sprintf("/playlists/%s/tracks", url.PathEscape(playlistID))
	first := uris
	if len(first) > 100 {
		first = uris[:100]
	}
	body := map[string]any{"uris": first}
	if err := c.do(ctx, "PUT", path, body, nil); err != nil {
		return err
	}
	if len(uris) > 100 {
		return c.AddTracks(ctx, playlistID, uris[100:])
	}
	return nil
}

// ReadbackURIs returns the actual track URIs currently in the playlist, in
// order, so the caller can diff intent vs result. Gaps are never hidden.
func (c *Client) ReadbackURIs(ctx context.Context, playlistID string) ([]string, error) {
	tracks, err := c.PlaylistTracks(ctx, playlistID)
	if err != nil {
		return nil, err
	}
	uris := make([]string, 0, len(tracks))
	for _, t := range tracks {
		uris = append(uris, t.URI)
	}
	return uris, nil
}
