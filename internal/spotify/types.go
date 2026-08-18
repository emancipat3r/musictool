package spotify

import "github.com/emancipat3r/musictool/internal/model"

// The structs below mirror only the fields musictool consumes from Spotify
// Web API responses. They are converted to model types immediately so nothing
// upstream leaks a raw API object into the store or the MCP surface.

type apiArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiAlbum struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ReleaseDate string `json:"release_date"`
	Images      []struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"images"`
}

// imageURL picks a mid-size cover (~300px) when available, falling back to
// whatever exists. Spotify serves 640/300/64 variants.
func (a apiAlbum) imageURL() string {
	best := ""
	for _, img := range a.Images {
		if img.URL == "" {
			continue
		}
		if best == "" || (img.Width >= 250 && img.Width <= 350) {
			best = img.URL
		}
	}
	return best
}

type apiExternalIDs struct {
	ISRC string `json:"isrc"`
}

type apiTrack struct {
	ID          string         `json:"id"`
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Artists     []apiArtist    `json:"artists"`
	Album       apiAlbum       `json:"album"`
	DurationMs  int            `json:"duration_ms"`
	Popularity  int            `json:"popularity"`
	ExternalIDs apiExternalIDs `json:"external_ids"`
	IsLocal     bool           `json:"is_local"`
}

func (t apiTrack) toModel() model.Track {
	artists := make([]model.Artist, 0, len(t.Artists))
	for _, a := range t.Artists {
		artists = append(artists, model.Artist{ID: a.ID, Name: a.Name})
	}
	return model.Track{
		ID:         t.ID,
		URI:        t.URI,
		Title:      t.Name,
		Artists:    artists,
		Album:      model.Album{ID: t.Album.ID, Name: t.Album.Name, ReleaseDate: t.Album.ReleaseDate, ImageURL: t.Album.imageURL()},
		DurationMs: t.DurationMs,
		Popularity: t.Popularity,
		ISRC:       t.ExternalIDs.ISRC,
	}
}

type apiPaging[T any] struct {
	Items  []T    `json:"items"`
	Next   string `json:"next"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type apiSavedTrack struct {
	AddedAt string   `json:"added_at"`
	Track   apiTrack `json:"track"`
}

type apiPlaylist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
	SnapshotID  string `json:"snapshot_id"`
	Owner       struct {
		ID string `json:"id"`
	} `json:"owner"`
	// Feb 2026: the playlist object's track summary moved from "tracks" to
	// "items". Live-verified 2026-07-25.
	Items struct {
		Total int `json:"total"`
	} `json:"items"`
}

func (p apiPlaylist) toModel() model.Playlist {
	return model.Playlist{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Public:      p.Public,
		OwnerID:     p.Owner.ID,
		TrackCount:  p.Items.Total,
		SnapshotID:  p.SnapshotID,
	}
}

// apiPlaylistTrackItem is one entry of GET /playlists/{id}/items. Feb 2026:
// the nested track moved from the "track" key to "item".
type apiPlaylistTrackItem struct {
	AddedAt string   `json:"added_at"`
	IsLocal bool     `json:"is_local"`
	Item    apiTrack `json:"item"`
}

type apiRecentItem struct {
	PlayedAt string   `json:"played_at"`
	Track    apiTrack `json:"track"`
}

type apiUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
