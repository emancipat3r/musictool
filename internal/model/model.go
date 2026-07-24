// Package model holds the neutral domain types shared by the Spotify client,
// the SQLite store, the resolver, and the MCP layer. Keeping them provider- and
// storage-agnostic here avoids import cycles and keeps compact fields in one
// place (the token-bloat guardrail lives at the tool layer, but starts here).
package model

import "time"

// Artist is a minimal artist record.
type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Album is a minimal album record.
type Album struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ReleaseDate string `json:"release_date,omitempty"`
}

// Track is the compact track shape used everywhere in spotifytool. It carries
// only what curation and resolution need — never a raw Spotify object.
type Track struct {
	ID         string   `json:"id"`
	URI        string   `json:"uri"`
	Title      string   `json:"title"`
	Artists    []Artist `json:"artists"`
	Album      Album    `json:"album"`
	DurationMs int      `json:"duration_ms,omitempty"`
	Popularity int      `json:"popularity,omitempty"`
	ISRC       string   `json:"isrc,omitempty"`
}

// ArtistName returns the primary artist's name (empty if none).
func (t Track) ArtistName() string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0].Name
}

// AllArtists joins every credited artist name with ", ".
func (t Track) AllArtists() string {
	out := ""
	for i, a := range t.Artists {
		if i > 0 {
			out += ", "
		}
		out += a.Name
	}
	return out
}

// Playlist is a minimal playlist record.
type Playlist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Public      bool   `json:"public"`
	OwnerID     string `json:"owner_id,omitempty"`
	TrackCount  int    `json:"track_count"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
}

// SavedTrack is a liked song with the time it was saved.
type SavedTrack struct {
	Track   Track     `json:"track"`
	SavedAt time.Time `json:"saved_at"`
}

// PlayEvent is one entry from recently-played history (append-only locally).
type PlayEvent struct {
	TrackID  string    `json:"track_id"`
	PlayedAt time.Time `json:"played_at"`
	Title    string    `json:"title"`
	Artist   string    `json:"artist"`
}

// User is the current Spotify user (needed to create playlists under them).
type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// TrackQuery is a curated pick the resolver must turn into a real URI.
// DurationMs is optional; when the caller knows the expected length it becomes
// a scoring tiebreaker between otherwise-equal candidates.
type TrackQuery struct {
	Artist     string `json:"artist"`
	Title      string `json:"title"`
	Album      string `json:"album,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
}
