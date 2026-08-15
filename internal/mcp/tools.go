package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/emancipat3r/musictool/internal/model"
	"github.com/emancipat3r/musictool/internal/service"
	"github.com/emancipat3r/musictool/internal/store"
)

// obj is a small helper for building JSON-Schema fragments. A nil props map
// must become {} — marshaling nil to "properties": null makes strict MCP
// clients (Claude Code included) reject the entire tools/list.
func obj(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// trackQuerySchema is the shared shape for a curated pick.
var trackQuerySchema = obj(map[string]any{
	"artist":      strProp("primary artist name"),
	"title":       strProp("track title"),
	"album":       strProp("optional album name to disambiguate"),
	"duration_ms": intProp("optional expected duration; ±3s candidates get a scoring tiebreak"),
}, "artist", "title")

// Tools builds the full MCP tool surface bound to svc.
func Tools(svc *service.Service) []Tool {
	return []Tool{
		{
			Name:        "sync_library",
			Description: "Refresh liked songs, playlists, recently-played history (append-only), and Keepers membership from Spotify into local SQLite. Set full=true for a deep sync of every playlist's tracks.",
			InputSchema: obj(map[string]any{"full": boolProp("deep-sync all playlist tracks")}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Full bool `json:"full"`
				}
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
			Description: "Distilled feedback since the last discovery batch: new saves, repeats, new Keepers votes (explicit positive), new Disliked votes (explicit negative), and tracks from the last batch that were ignored.",
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
			Description: "List playlists (compact metadata) from the local store. Defaults to playlists the user OWNS; set owned_only=false to include followed playlists. Descriptions are truncated.",
			InputSchema: obj(map[string]any{
				"owned_only": boolProp("only playlists owned by the user (default true)"),
				"limit":      intProp("max rows (default 50)"),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				a := struct {
					OwnedOnly *bool `json:"owned_only"`
					Limit     int   `json:"limit"`
				}{}
				_ = json.Unmarshal(args, &a)
				ownedOnly := a.OwnedOnly == nil || *a.OwnedOnly
				limit := a.Limit
				if limit <= 0 || limit > 200 {
					limit = 50
				}
				pls, err := svc.DB.Playlists(ctx)
				if err != nil {
					return nil, err
				}
				userID, _ := svc.DB.GetMeta(ctx, "user_id")
				out := pls[:0:0]
				for _, p := range pls {
					if ownedOnly && userID != "" && p.OwnerID != userID {
						continue
					}
					if r := []rune(p.Description); len(r) > 100 {
						p.Description = string(r[:100]) + "…"
					}
					out = append(out, p)
					if len(out) >= limit {
						break
					}
				}
				return out, nil
			},
		},
		{
			Name:        "read_playlist",
			Description: "Read a playlist's tracks LIVE from Spotify, by id or exact name (name matching is scoped to playlists the user owns). An unknown playlist is an error; an empty playlist returns an empty list — never null.",
			InputSchema: obj(map[string]any{"playlist": strProp("playlist id or exact name")}, "playlist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Playlist string `json:"playlist"`
				}
				if err := json.Unmarshal(args, &a); err != nil || a.Playlist == "" {
					return nil, errors.New("playlist id or name is required")
				}
				return svc.ReadPlaylist(ctx, a.Playlist)
			},
		},
		{
			Name:        "search_tracks",
			Description: "Search the music provider's catalog with structured fields (artist/title/album; preferred — each provider builds its best native query) or a raw query. Returns compact candidate tracks.",
			InputSchema: obj(map[string]any{
				"artist": strProp("artist filter"),
				"title":  strProp("title filter"),
				"album":  strProp("album filter"),
				"query":  strProp("raw query if you must (discouraged)"),
				"limit":  intProp("max results (hard API cap of 10)"),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Artist, Title, Album, Query string
					Limit                       int
				}
				_ = json.Unmarshal(args, &a)
				if a.Artist != "" || a.Title != "" || a.Album != "" {
					return svc.SP.SearchPick(ctx, a.Artist, a.Title, a.Album, a.Limit)
				}
				if strings.TrimSpace(a.Query) == "" {
					return nil, errors.New("provide artist/title (preferred) or query")
				}
				return svc.SP.SearchTracks(ctx, a.Query, a.Limit)
			},
		},
		{
			Name:        "resolve_tracklist",
			Description: "Deterministically resolve curated {artist,title,album?,duration_ms?} picks into exact Spotify URIs. Never substitutes. Buckets: exact = title AND artist match after normalization, unambiguously (ties collapse only when candidates share one ISRC, i.e. the same recording); probable = one side matched exactly, the other partially (accepted, flagged); ambiguous = multiple distinct recordings tied, top-3 returned (deduped by ISRC) for the caller to pick; not_found = nothing matched the title. Scores: title/artist exact are worth 100 each, +25 verbatim title (remaster tags exempt), +8 album match, +6 duration within 3s, -15 unrequested live/acoustic/remix variant, -12 non-canonical album (soundtrack/hits/karaoke/tribute) unless that album was pinned. Providing album (and duration_ms) is the reliable way to break ties.",
			InputSchema: obj(map[string]any{
				"tracks": map[string]any{"type": "array", "items": trackQuerySchema, "description": "curated picks"},
			}, "tracks"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Tracks []model.TrackQuery `json:"tracks"`
				}
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
			Description: "Resolve a tracklist, create a playlist, add exactly the resolved URIs, then read the playlist back and diff intent vs result. Gaps are reported, never substituted. Idempotent by name: if a same-named playlist exists, nothing is created and the response says so (set allow_duplicate to force). Picks matching the user's Disliked playlist are refused and reported under disliked_skipped.",
			InputSchema: obj(map[string]any{
				"name":               strProp("playlist name (e.g. 'Discovery W30')"),
				"description":        strProp("playlist description / rationale"),
				"public":             boolProp("public playlist (default false)"),
				"tracks":             map[string]any{"type": "array", "items": trackQuerySchema},
				"allow_duplicate":    boolProp("create even if a same-named playlist exists (default false)"),
				"record_batch_label": strProp("if set, record this as a discovery batch under this label"),
			}, "name", "tracks"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Name             string             `json:"name"`
					Description      string             `json:"description"`
					Public           bool               `json:"public"`
					Tracks           []model.TrackQuery `json:"tracks"`
					AllowDuplicate   bool               `json:"allow_duplicate"`
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
					AllowDuplicate:   a.AllowDuplicate,
					RecordBatchLabel: a.RecordBatchLabel,
				})
			},
		},
		{
			Name:        "remove_from_playlist_exact",
			Description: "Remove tracks from an existing playlist (by id or exact name). Picks are matched against the playlist's actual contents by normalized title+artist (no search); explicit URIs also accepted. Read-back verifies the removals.",
			InputSchema: obj(map[string]any{
				"playlist": strProp("playlist id or exact name"),
				"tracks":   map[string]any{"type": "array", "items": trackQuerySchema, "description": "picks to match against playlist contents"},
				"uris":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "explicit track URIs to remove (provider-scoped, e.g. spotify:track:… / tidal:track:…)"},
			}, "playlist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Playlist string             `json:"playlist"`
					Tracks   []model.TrackQuery `json:"tracks"`
					URIs     []string           `json:"uris"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, err
				}
				if a.Playlist == "" || (len(a.Tracks) == 0 && len(a.URIs) == 0) {
					return nil, errors.New("playlist and at least one of tracks/uris are required")
				}
				return svc.RemoveExact(ctx, a.Playlist, a.Tracks, a.URIs)
			},
		},
		{
			Name:        "delete_playlist",
			Description: "Delete (unfollow) a playlist from the library, by id or exact name. Use for scratch/test playlists and retired batches.",
			InputSchema: obj(map[string]any{"playlist": strProp("playlist id or exact name")}, "playlist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Playlist string `json:"playlist"`
				}
				if err := json.Unmarshal(args, &a); err != nil || a.Playlist == "" {
					return nil, errors.New("playlist is required")
				}
				return svc.DeletePlaylist(ctx, a.Playlist)
			},
		},
		{
			Name:        "add_to_playlist_exact",
			Description: "Resolve picks and append them to an EXISTING playlist (by id or exact name), with read-back verification of the appended tail. Duplicates already present are skipped, and picks matching the user's Disliked playlist are refused (disliked_skipped). Use this to settle ambiguous picks after a build instead of rebuilding.",
			InputSchema: obj(map[string]any{
				"playlist": strProp("playlist id or exact name"),
				"tracks":   map[string]any{"type": "array", "items": trackQuerySchema},
			}, "playlist", "tracks"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Playlist string             `json:"playlist"`
					Tracks   []model.TrackQuery `json:"tracks"`
				}
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, err
				}
				if a.Playlist == "" || len(a.Tracks) == 0 {
					return nil, errors.New("playlist and tracks are required")
				}
				return svc.AppendExact(ctx, a.Playlist, a.Tracks)
			},
		},
		{
			Name:        "get_taste_deltas",
			Description: "Evidence-weighted per-artist affinity from all feedback channels (Keeper/Disliked votes, saves, listen telemetry: completions, restarts, skips). Explicit votes never decay; implicit evidence has a 180-day half-life. Trend thresholds (already applied): rising = affinity >= 2 with >= 3 positive events; falling = affinity <= -1.5 with >= 2 negative events, at least one explicit OR a second independent early skip — a single skip NEVER demotes (skips correlate with engagement). Use this to update the taste profile's signals:auto section; evidence counts are included so claims can be justified.",
			InputSchema: obj(map[string]any{"limit": intProp("max artists (default 30)")}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Limit int `json:"limit"`
				}
				_ = json.Unmarshal(args, &a)
				limit := a.Limit
				if limit <= 0 || limit > 100 {
					limit = 30
				}
				deltas, err := svc.DB.TasteDeltas(ctx)
				if err != nil {
					return nil, err
				}
				if len(deltas) > limit {
					deltas = deltas[:limit]
				}
				return map[string]any{
					"artists": deltas,
					"note":    "affinity = weighted, time-decayed evidence; see tool description for thresholds",
				}, nil
			},
		},
		{
			Name:        "get_batches",
			Description: "Recent discovery batches (label, playlist id, when, track count, digest) so a new batch can avoid repeating what already shipped.",
			InputSchema: obj(map[string]any{"limit": intProp("max rows (default 20)")}),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Limit int `json:"limit"`
				}
				_ = json.Unmarshal(args, &a)
				batches, err := svc.DB.ListBatches(ctx, a.Limit)
				if err != nil {
					return nil, err
				}
				if batches == nil {
					batches = []store.Batch{}
				}
				return batches, nil
			},
		},
		{
			Name:        "get_artist_tags",
			Description: "Last.fm tags + similar artists for an artist (discovery seed), served from local cache when available. Subjective qualities are tags, not audio-feature math.",
			InputSchema: obj(map[string]any{"artist": strProp("artist name")}, "artist"),
			Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Artist string `json:"artist"`
				}
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
				var a struct {
					Artist string `json:"artist"`
				}
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
