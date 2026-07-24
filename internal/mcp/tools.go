package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/emancipat3r/spotifytool/internal/model"
	"github.com/emancipat3r/spotifytool/internal/service"
	"github.com/emancipat3r/spotifytool/internal/spotify"
)

// obj is a small helper for building JSON-Schema fragments.
func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func intProp(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// trackQuerySchema is the shared shape for a curated pick.
var trackQuerySchema = obj(map[string]any{
	"artist": strProp("primary artist name"),
	"title":  strProp("track title"),
	"album":  strProp("optional album name to disambiguate"),
}, "artist", "title")

// Tools builds the full MCP tool surface bound to svc.
func Tools(svc *service.Service) []Tool {
	return []Tool{
		{
			Name:        "sync_library",
			Description: "Refresh liked songs, playlists, recently-played history (append-only), and Keepers membership from Spotify into local SQLite. Set full=true for a deep sync of every playlist's tracks.",
			InputSchema: obj(map[string]any{"full": boolProp("deep-sync all playlist tracks")}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct{ Full bool `json:"full"` }
				_ = json.Unmarshal(args, &a)
				return svc.Sync(ctx, a.Full)
			},
		},
		{
			Name:        "get_library_stats",
			Description: "Distilled library summary: counts, top artists, last sync/batch times. Compact; use this instead of dumping the library.",
			InputSchema: obj(nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return svc.DB.Stats(ctx)
			},
		},
		{
			Name:        "get_recent_signals",
			Description: "Distilled feedback since the last discovery batch: new saves, repeats, new Keepers votes, and tracks from the last batch that were ignored.",
			InputSchema: obj(nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return svc.DB.Signals(ctx)
			},
		},
		{
			Name:        "get_liked_songs",
			Description: "Paginated liked songs as compact rows (id, uri, title, artist). Use limit/offset; do not fetch the whole library at once.",
			InputSchema: obj(map[string]any{
				"limit":  intProp("max rows (default 50, cap 200)"),
				"offset": intProp("row offset"),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Limit  int `json:"limit"`
					Offset int `json:"offset"`
				}
				_ = json.Unmarshal(args, &a)
				return svc.DB.LikedSongs(ctx, a.Limit, a.Offset)
			},
		},
		{
			Name:        "get_playlists",
			Description: "List playlists (compact metadata) from the local store.",
			InputSchema: obj(nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return svc.DB.Playlists(ctx)
			},
		},
		{
			Name:        "read_playlist",
			Description: "Read a playlist's tracks (compact) by id or exact name.",
			InputSchema: obj(map[string]any{"playlist": strProp("playlist id or exact name")}, "playlist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct{ Playlist string `json:"playlist"` }
				if err := json.Unmarshal(args, &a); err != nil || a.Playlist == "" {
					return nil, errors.New("playlist id or name is required")
				}
				return svc.DB.PlaylistTracks(ctx, a.Playlist)
			},
		},
		{
			Name:        "search_tracks",
			Description: "Search Spotify with field filters (artist/title/album), never free text. Returns compact candidate tracks.",
			InputSchema: obj(map[string]any{
				"artist": strProp("artist filter"),
				"title":  strProp("title filter"),
				"album":  strProp("album filter"),
				"query":  strProp("raw query if you must (discouraged)"),
				"limit":  intProp("max results (default 20)"),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Artist, Title, Album, Query string
					Limit                       int
				}
				_ = json.Unmarshal(args, &a)
				q := a.Query
				if a.Artist != "" || a.Title != "" || a.Album != "" {
					q = spotify.FieldQuery(a.Artist, a.Title, a.Album)
				}
				if strings.TrimSpace(q) == "" {
					return nil, errors.New("provide artist/title (preferred) or query")
				}
				return svc.SP.SearchTracks(ctx, q, a.Limit)
			},
		},
		{
			Name:        "resolve_tracklist",
			Description: "Deterministically resolve curated {artist,title,album?} picks into exact Spotify URIs. Returns per-pick bucket: exact/probable/ambiguous/not_found. Never substitutes.",
			InputSchema: obj(map[string]any{
				"tracks": map[string]any{"type": "array", "items": trackQuerySchema, "description": "curated picks"},
			}, "tracks"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct{ Tracks []model.TrackQuery `json:"tracks"` }
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, err
				}
				if len(a.Tracks) == 0 {
					return nil, errors.New("tracks is required")
				}
				return svc.Res.ResolveList(ctx, a.Tracks)
			},
		},
		{
			Name:        "create_playlist_exact",
			Description: "Resolve a tracklist, create a playlist, add exactly the resolved URIs, then read the playlist back and diff intent vs result. Gaps are reported, never substituted.",
			InputSchema: obj(map[string]any{
				"name":        strProp("playlist name (e.g. 'Discovery W30')"),
				"description": strProp("playlist description / rationale"),
				"public":      boolProp("public playlist (default false)"),
				"tracks":      map[string]any{"type": "array", "items": trackQuerySchema},
				"record_batch_label": strProp("if set, record this as a discovery batch under this label"),
			}, "name", "tracks"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Name             string             `json:"name"`
					Description      string             `json:"description"`
					Public           bool               `json:"public"`
					Tracks           []model.TrackQuery `json:"tracks"`
					RecordBatchLabel string             `json:"record_batch_label"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, err
				}
				if a.Name == "" || len(a.Tracks) == 0 {
					return nil, errors.New("name and tracks are required")
				}
				return svc.BuildExact(ctx, service.BuildOptions{
					Name:             a.Name,
					Description:      a.Description,
					Public:           a.Public,
					Queries:          a.Tracks,
					RecordBatchLabel: a.RecordBatchLabel,
				})
			},
		},
		{
			Name:        "get_artist_tags",
			Description: "Last.fm tags + similar artists for an artist (discovery seed), served from local cache when available. Subjective qualities are tags, not audio-feature math.",
			InputSchema: obj(map[string]any{"artist": strProp("artist name")}, "artist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct{ Artist string `json:"artist"` }
				if err := json.Unmarshal(args, &a); err != nil || a.Artist == "" {
					return nil, errors.New("artist is required")
				}
				return svc.ArtistTags(ctx, a.Artist)
			},
		},
		{
			Name:        "get_similar_artists",
			Description: "Similar-artist names for an artist (Last.fm), a discovery seed replacing Spotify's deprecated related-artists.",
			InputSchema: obj(map[string]any{"artist": strProp("artist name")}, "artist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct{ Artist string `json:"artist"` }
				if err := json.Unmarshal(args, &a); err != nil || a.Artist == "" {
					return nil, errors.New("artist is required")
				}
				similar, err := svc.SimilarArtists(ctx, a.Artist)
				if err != nil {
					return nil, err
				}
				return map[string]any{"artist": a.Artist, "similar": similar}, nil
			},
		},
	}
}
