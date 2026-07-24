package spotify

import "github.com/emancipat3r/spotifytool/internal/model"

// The structs below mirror only the fields spotifytool consumes from Spotify
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
		Album:      model.Album{ID: t.Album.ID, Name: t.Album.Name, ReleaseDate: t.Album.ReleaseDate},
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
	Tracks struct {
		Total int `json:"total"`
	} `json:"tracks"`
}

func (p apiPlaylist) toModel() model.Playlist {
	return model.Playlist{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Public:      p.Public,
		OwnerID:     p.Owner.ID,
		TrackCount:  p.Tracks.Total,
		SnapshotID:  p.SnapshotID,
	}
}

type apiPlaylistTrackItem struct {
	AddedAt string   `json:"added_at"`
	IsLocal bool     `json:"is_local"`
	Track   apiTrack `json:"track"`
}

type apiRecentItem struct {
	PlayedAt string   `json:"played_at"`
	Track    apiTrack `json:"track"`
}

type apiUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
