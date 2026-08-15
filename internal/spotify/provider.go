package spotify

import (
	"context"
	"strings"

	"github.com/emancipat3r/spotifytool/internal/model"
	"github.com/emancipat3r/spotifytool/internal/provider"
)

// Compile-time proof that Client implements the provider surface.
var _ provider.Client = (*Client)(nil)

// Name is the provider key stamped into the store and resolver cache.
func (c *Client) Name() string { return "spotify" }

// Capabilities: Spotify exposes the full feedback surface.
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		ListenTelemetry:  true,
		PlayHistory:      true,
		PrivatePlaylists: true,
	}
}

// SearchPick is the resolver's search path: Spotify supports field filters
// (track:/artist:/album:), so the pick becomes a tightly-scoped field query.
func (c *Client) SearchPick(ctx context.Context, artist, title, album string, limit int) ([]model.Track, error) {
	return c.SearchTracks(ctx, FieldQuery(artist, title, album), limit)
}

// PlaylistURI and TrackURI render ids in Spotify's URI scheme.
func (c *Client) PlaylistURI(id string) string { return "spotify:playlist:" + id }
func (c *Client) TrackURI(id string) string    { return "spotify:track:" + id }

// TrackID extracts the bare id from a spotify:track: URI.
func (c *Client) TrackID(uri string) (string, bool) {
	id := strings.TrimPrefix(uri, "spotify:track:")
	if id == uri || id == "" {
		return "", false
	}
	return id, true
}
