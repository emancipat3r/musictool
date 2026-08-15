package tidal

import (
	"context"
	"fmt"
	"net/url"

	"github.com/emancipat3r/spotifytool/internal/model"
)

// itemRefsBatch is the JSON:API payload for playlist item add/remove. The
// spec caps one operation at 50 identifiers; 20 keeps requests small.
const itemBatch = 20

// CreatePlaylist creates a playlist and returns it. TIDAL's third-party API
// only offers PUBLIC and UNLISTED visibility (live-verified: omitting
// accessType also yields UNLISTED), so public=false maps to UNLISTED — anyone
// with the link can view, but it is not browsable or searchable.
func (c *Client) CreatePlaylist(ctx context.Context, name, description string, public bool) (model.Playlist, error) {
	access := "UNLISTED"
	if public {
		access = "PUBLIC"
	}
	body := map[string]any{
		"data": map[string]any{
			"type": "playlists",
			"attributes": map[string]any{
				"name":        name,
				"description": description,
				"accessType":  access,
			},
		},
	}
	var doc jaDocument
	q := url.Values{"countryCode": {c.country}}
	if err := c.do(ctx, "POST", "/playlists", q, body, &doc); err != nil {
		return model.Playlist{}, err
	}
	res := doc.dataResources()
	if len(res) == 0 {
		return model.Playlist{}, fmt.Errorf("tidal: create playlist returned no resource")
	}
	return c.playlistFromResource(res[0]), nil
}

// AddTracks appends track URIs to a playlist in order, batched.
func (c *Client) AddTracks(ctx context.Context, playlistID string, uris []string) error {
	ids := make([]string, 0, len(uris))
	for _, u := range uris {
		id, ok := c.TrackID(u)
		if !ok {
			return fmt.Errorf("tidal: not a tidal track URI: %q", u)
		}
		ids = append(ids, id)
	}
	path := "/playlists/" + url.PathEscape(playlistID) + "/relationships/items"
	q := url.Values{"countryCode": {c.country}}
	for i := 0; i < len(ids); i += itemBatch {
		end := i + itemBatch
		if end > len(ids) {
			end = len(ids)
		}
		data := make([]map[string]any, 0, end-i)
		for _, id := range ids[i:end] {
			data = append(data, map[string]any{"id": id, "type": "tracks"})
		}
		if err := c.do(ctx, "POST", path, q, map[string]any{"data": data}, nil); err != nil {
			return err
		}
	}
	return nil
}

// RemoveTracks deletes the given track URIs from a playlist. Playlist entries
// are keyed by an occurrence-level itemId, so the items relationship is read
// first and every occurrence of each requested track is targeted explicitly.
func (c *Client) RemoveTracks(ctx context.Context, playlistID string, uris []string) error {
	want := map[string]bool{}
	for _, u := range uris {
		id, ok := c.TrackID(u)
		if !ok {
			return fmt.Errorf("tidal: not a tidal track URI: %q", u)
		}
		want[id] = true
	}
	refs, err := c.playlistItemRefs(ctx, playlistID)
	if err != nil {
		return err
	}
	var data []map[string]any
	for _, ref := range refs {
		if !want[ref.ID] {
			continue
		}
		item := map[string]any{"id": ref.ID, "type": "tracks"}
		if m := refMeta(ref); m.ItemID != "" {
			item["meta"] = map[string]any{"itemId": m.ItemID}
		}
		data = append(data, item)
	}
	path := "/playlists/" + url.PathEscape(playlistID) + "/relationships/items"
	q := url.Values{"countryCode": {c.country}}
	for i := 0; i < len(data); i += itemBatch {
		end := i + itemBatch
		if end > len(data) {
			end = len(data)
		}
		if err := c.do(ctx, "DELETE", path, q, map[string]any{"data": data[i:end]}, nil); err != nil {
			return err
		}
	}
	return nil
}

// UnfollowPlaylist deletes the playlist (TIDAL playlists the user owns are
// deleted outright; there is no follow/unfollow distinction for own lists).
func (c *Client) UnfollowPlaylist(ctx context.Context, playlistID string) error {
	return c.do(ctx, "DELETE", "/playlists/"+url.PathEscape(playlistID), nil, nil, nil)
}
