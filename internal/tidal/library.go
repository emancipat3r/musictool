package tidal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/emancipat3r/musictool/internal/model"
	"github.com/emancipat3r/musictool/internal/provider"
)

type userAttrs struct {
	Username string `json:"username"`
	Country  string `json:"country"`
}

// CurrentUser returns the authorized user. TIDAL's JSON:API accepts "me" as
// the id for the authenticated user; the JWT's uid claim is the fallback for
// deployments where that path is unavailable.
func (c *Client) CurrentUser(ctx context.Context) (model.User, error) {
	var doc jaDocument
	if err := c.do(ctx, "GET", "/users/me", nil, nil, &doc); err != nil {
		if id := c.uidFromToken(ctx); id != "" {
			c.userID = id
			return model.User{ID: id}, nil
		}
		return model.User{}, err
	}
	res := doc.dataResources()
	if len(res) == 0 {
		return model.User{}, fmt.Errorf("tidal: empty /users/me response")
	}
	var ua userAttrs
	_ = json.Unmarshal(res[0].Attributes, &ua)
	c.userID = res[0].ID
	if ua.Country != "" {
		c.country = ua.Country
	}
	return model.User{ID: res[0].ID, DisplayName: ua.Username}, nil
}

// uidFromToken best-effort decodes the access token's JWT payload "uid" claim.
func (c *Client) uidFromToken(ctx context.Context) string {
	tok, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return ""
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		UID json.Number `json:"uid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.UID.String()
}

// itemMeta is the per-item metadata on collection/playlist relationship refs.
type itemMeta struct {
	AddedAt string `json:"addedAt"`
	ItemID  string `json:"itemId"`
}

func refMeta(ref jaRef) itemMeta {
	var m itemMeta
	if len(ref.Meta) > 0 {
		_ = json.Unmarshal(ref.Meta, &m)
	}
	return m
}

// SavedTracks pages through the user's My Collection tracks ("liked songs"
// equivalent), hydrating full metadata in batches.
func (c *Client) SavedTracks(ctx context.Context) ([]model.SavedTrack, error) {
	type entry struct {
		id      string
		addedAt time.Time
	}
	var entries []entry
	q := url.Values{"countryCode": {c.country}}
	err := c.getPaged(ctx, "/userCollectionTracks/me/relationships/items", q, func(doc *jaDocument) error {
		for _, ref := range decodeRefs(doc.Data) {
			if ref.Type != "tracks" {
				continue
			}
			m := refMeta(ref)
			added, _ := time.Parse(time.RFC3339, m.AddedAt)
			entries = append(entries, entry{id: ref.ID, addedAt: added})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.id)
	}
	tracks, err := c.hydrateTracks(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.Track, len(tracks))
	for _, t := range tracks {
		byID[t.ID] = t
	}
	out := make([]model.SavedTrack, 0, len(entries))
	for _, e := range entries {
		if t, ok := byID[e.id]; ok {
			out = append(out, model.SavedTrack{Track: t, SavedAt: e.addedAt})
		}
	}
	return out, nil
}

type playlistAttrs struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	AccessType    string `json:"accessType"` // PUBLIC | UNLISTED | PRIVATE
	NumberOfItems int    `json:"numberOfItems"`
}

func (c *Client) playlistFromResource(r jaResource) model.Playlist {
	var a playlistAttrs
	_ = json.Unmarshal(r.Attributes, &a)
	ownerID := c.userID
	if refs := r.relRefs("owners"); len(refs) > 0 {
		ownerID = refs[0].ID
	}
	return model.Playlist{
		ID:          r.ID,
		Name:        a.Name,
		Description: a.Description,
		Public:      a.AccessType == "PUBLIC",
		OwnerID:     ownerID,
		TrackCount:  a.NumberOfItems,
	}
}

// Playlists pages through the playlists the user owns. (Followed playlists are
// a separate collection resource on TIDAL and are not curation targets.)
func (c *Client) Playlists(ctx context.Context) ([]model.Playlist, error) {
	if c.userID == "" {
		if _, err := c.CurrentUser(ctx); err != nil {
			return nil, err
		}
	}
	out := []model.Playlist{}
	q := url.Values{
		"filter[owners.id]": {"me"},
		"countryCode":       {c.country},
	}
	err := c.getPaged(ctx, "/playlists", q, func(doc *jaDocument) error {
		for _, r := range doc.dataResources() {
			out = append(out, c.playlistFromResource(r))
		}
		return nil
	})
	return out, err
}

// playlistItemRefs pages a playlist's items relationship (identifiers + meta,
// no hydration), in order.
func (c *Client) playlistItemRefs(ctx context.Context, playlistID string) ([]jaRef, error) {
	var refs []jaRef
	q := url.Values{"countryCode": {c.country}}
	path := "/playlists/" + url.PathEscape(playlistID) + "/relationships/items"
	err := c.getPaged(ctx, path, q, func(doc *jaDocument) error {
		for _, ref := range decodeRefs(doc.Data) {
			if ref.Type == "tracks" { // videos are skipped, like local files on Spotify
				refs = append(refs, ref)
			}
		}
		return nil
	})
	return refs, err
}

// PlaylistTracks returns a playlist's tracks with full metadata, in order.
func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error) {
	refs, err := c.playlistItemRefs(ctx, playlistID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	return c.hydrateTracks(ctx, ids)
}

// ReadbackURIs returns the playlist's track URIs in order, straight from the
// items relationship (no hydration — this is the read-back verification hot
// path).
func (c *Client) ReadbackURIs(ctx context.Context, playlistID string) ([]string, error) {
	refs, err := c.playlistItemRefs(ctx, playlistID)
	if err != nil {
		return nil, err
	}
	uris := make([]string, 0, len(refs))
	for _, r := range refs {
		uris = append(uris, trackURI(r.ID))
	}
	return uris, nil
}

// RecentlyPlayed: TIDAL's third-party API exposes no listening history.
func (c *Client) RecentlyPlayed(ctx context.Context) ([]model.PlayEvent, error) {
	return nil, provider.ErrNotSupported
}

// CurrentlyPlaying: TIDAL's third-party API exposes no player state.
func (c *Client) CurrentlyPlaying(ctx context.Context) (*provider.NowPlaying, error) {
	return nil, provider.ErrNotSupported
}
