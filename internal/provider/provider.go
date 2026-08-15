// Package provider defines the music-provider abstraction: the exact surface
// the service layer needs from a streaming backend (Spotify, TIDAL). Each
// backend package implements Client against these signatures and converts its
// raw API objects to model types at the boundary — nothing provider-specific
// leaks into the store, the resolver, or the MCP surface.
package provider

import (
	"context"
	"errors"

	"github.com/emancipat3r/spotifytool/internal/model"
)

// ErrNotSupported is returned by optional endpoints a provider does not offer
// (e.g. listen telemetry on TIDAL). Callers should consult Capabilities before
// calling rather than probing for this error.
var ErrNotSupported = errors.New("not supported by this provider")

// Capabilities declares which optional feature groups a provider supports, so
// the service degrades gracefully instead of failing (PRD: report gaps
// honestly, never pretend).
type Capabilities struct {
	// ListenTelemetry: a read-only currently-playing endpoint exists, so the
	// poller can classify skips/completions/restarts.
	ListenTelemetry bool
	// PlayHistory: a recently-played endpoint exists, so sync can accumulate
	// play history locally.
	PlayHistory bool
	// PrivatePlaylists: playlists can be created truly private. TIDAL's
	// third-party API caps out at UNLISTED.
	PrivatePlaylists bool
}

// NowPlaying is a snapshot of the player state (read-only telemetry).
type NowPlaying struct {
	Track      model.Track
	ProgressMs int
	IsPlaying  bool
}

// Client is the full provider surface the service layer consumes. All track
// and playlist identifiers cross this boundary as provider-scoped URIs
// ("spotify:track:x", "tidal:track:y") so rows from different providers can
// never be confused.
type Client interface {
	// Name is the stable provider key ("spotify", "tidal") stamped into the
	// store's meta table and the resolver cache keys.
	Name() string
	Capabilities() Capabilities

	// SearchTracks runs a raw provider-native query (the MCP search_tracks
	// escape hatch). SearchPick is the resolver's path: each provider builds
	// its own best fielded/filtered query from the structured pick.
	SearchTracks(ctx context.Context, query string, limit int) ([]model.Track, error)
	SearchPick(ctx context.Context, artist, title, album string, limit int) ([]model.Track, error)

	CurrentUser(ctx context.Context) (model.User, error)
	SavedTracks(ctx context.Context) ([]model.SavedTrack, error)
	Playlists(ctx context.Context) ([]model.Playlist, error)
	PlaylistTracks(ctx context.Context, playlistID string) ([]model.Track, error)

	// RecentlyPlayed and CurrentlyPlaying return ErrNotSupported when the
	// matching Capabilities flag is false.
	RecentlyPlayed(ctx context.Context) ([]model.PlayEvent, error)
	CurrentlyPlaying(ctx context.Context) (*NowPlaying, error)

	CreatePlaylist(ctx context.Context, name, description string, public bool) (model.Playlist, error)
	AddTracks(ctx context.Context, playlistID string, uris []string) error
	RemoveTracks(ctx context.Context, playlistID string, uris []string) error
	UnfollowPlaylist(ctx context.Context, playlistID string) error
	ReadbackURIs(ctx context.Context, playlistID string) ([]string, error)

	// PlaylistURI and TrackURI render ids in the provider's URI scheme.
	PlaylistURI(id string) string
	TrackURI(id string) string
	// TrackID extracts the bare track id from one of this provider's track
	// URIs; ok is false when the URI is not a track URI of this provider.
	TrackID(uri string) (id string, ok bool)
}
