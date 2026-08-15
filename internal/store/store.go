// Package store is the single writer to the SQLite database. Claude (via MCP
// tools) and the dashboard both reach the DB only through this engine package —
// nothing else opens the file for writes.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, preserves CGO_ENABLED=0

	"github.com/emancipat3r/spotifytool/internal/model"
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// Open opens (creating if needed) the SQLite database and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc's driver is safe for concurrent readers; keep a single writer.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Migrations for databases created before these columns existed; the
	// error on an already-migrated schema is expected and ignored.
	_, _ = db.ExecContext(context.Background(),
		`ALTER TABLE resolution_cache ADD COLUMN track_json TEXT`)
	_, _ = db.ExecContext(context.Background(),
		`ALTER TABLE albums ADD COLUMN image_url TEXT`)
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for read-only dashboard queries in the same package.
func (s *Store) DB() *sql.DB { return s.db }

// upsertTrackTx writes a track and its album/artists inside a transaction.
func upsertTrackTx(ctx context.Context, tx *sql.Tx, t model.Track) error {
	now := nowRFC3339()
	if t.Album.ID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO albums(id,name,release_date,image_url) VALUES(?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET name=excluded.name,
			   release_date=excluded.release_date,
			   image_url=CASE WHEN excluded.image_url<>'' THEN excluded.image_url ELSE albums.image_url END`,
			t.Album.ID, t.Album.Name, t.Album.ReleaseDate, t.Album.ImageURL); err != nil {
			return err
		}
	}
	var albumID any
	if t.Album.ID != "" {
		albumID = t.Album.ID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tracks(id,uri,title,album_id,primary_artist,duration_ms,popularity,isrc,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   uri=excluded.uri, title=excluded.title, album_id=excluded.album_id,
		   primary_artist=excluded.primary_artist, duration_ms=excluded.duration_ms,
		   popularity=excluded.popularity, isrc=excluded.isrc, updated_at=excluded.updated_at`,
		t.ID, t.URI, t.Title, albumID, t.ArtistName(), t.DurationMs, t.Popularity, t.ISRC, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM track_artists WHERE track_id=?`, t.ID); err != nil {
		return err
	}
	for i, a := range t.Artists {
		if a.ID != "" {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO artists(id,name,updated_at) VALUES(?,?,?)
				 ON CONFLICT(id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
				a.ID, a.Name, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO track_artists(track_id,artist_id,position) VALUES(?,?,?)`,
				t.ID, a.ID, i); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReplaceSavedTracks upserts all tracks and rewrites the saved_tracks set.
func (s *Store) ReplaceSavedTracks(ctx context.Context, saved []model.SavedTrack) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM saved_tracks`); err != nil {
		return err
	}
	for _, st := range saved {
		if err := upsertTrackTx(ctx, tx, st.Track); err != nil {
			return err
		}
		var savedAt any
		if !st.SavedAt.IsZero() {
			savedAt = st.SavedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO saved_tracks(track_id,saved_at) VALUES(?,?)`,
			st.Track.ID, savedAt); err != nil {
			return err
		}
	}
	if err := setMetaTx(ctx, tx, "last_sync", nowRFC3339()); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplacePlaylists refreshes the playlist metadata set by upserting the
// current playlists and pruning only the ones that vanished. It must NOT
// delete-and-reinsert: playlist_tracks cascades on playlist deletion, so a
// wholesale rewrite would wipe the deep sync's work on every hourly run.
func (s *Store) ReplacePlaylists(ctx context.Context, pls []model.Playlist) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := nowRFC3339()
	present := map[string]bool{}
	for _, p := range pls {
		present[p.ID] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO playlists(id,name,description,public,owner_id,snapshot_id,track_count,updated_at)
			 VALUES(?,?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET
			   name=excluded.name, description=excluded.description,
			   public=excluded.public, owner_id=excluded.owner_id,
			   snapshot_id=excluded.snapshot_id, track_count=excluded.track_count,
			   updated_at=excluded.updated_at`,
			p.ID, p.Name, p.Description, boolInt(p.Public), p.OwnerID, p.SnapshotID, p.TrackCount, now); err != nil {
			return err
		}
	}
	// Prune playlists no longer in the account (cascade removes their tracks).
	rows, err := tx.QueryContext(ctx, `SELECT id FROM playlists`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !present[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplacePlaylistTracks rewrites one playlist's ordered track membership.
func (s *Store) ReplacePlaylistTracks(ctx context.Context, playlistID string, tracks []model.Track) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_tracks WHERE playlist_id=?`, playlistID); err != nil {
		return err
	}
	for i, t := range tracks {
		if err := upsertTrackTx(ctx, tx, t); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO playlist_tracks(playlist_id,track_id,position) VALUES(?,?,?)`,
			playlistID, t.ID, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AppendPlays inserts recently-played events, ignoring duplicates. Returns the
// number of genuinely new rows.
func (s *Store) AppendPlays(ctx context.Context, plays []model.PlayEvent) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	added := 0
	now := nowRFC3339()
	for _, p := range plays {
		if p.TrackID == "" || p.PlayedAt.IsZero() {
			continue
		}
		// Minimal track row so plays of not-yet-saved tracks still join in
		// signals (repeats of a discovery pick matter before it's ever liked).
		// INSERT OR IGNORE: never clobbers a full row from a library sync.
		uri := p.URI
		if uri == "" {
			uri = p.TrackID // provider omitted the URI; keep the row joinable
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO tracks(id,uri,title,primary_artist,updated_at)
			 VALUES(?,?,?,?,?)`,
			p.TrackID, uri, p.Title, p.Artist, now); err != nil {
			return added, err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO recently_played(track_id,played_at) VALUES(?,?)`,
			p.TrackID, p.PlayedAt.UTC().Format(time.RFC3339))
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return added, err
	}
	return added, nil
}

// --- resolve.Cache implementation ---

// cacheEntry is the persisted payload of a resolution: the full chosen track
// plus the score it earned, so replays are verifiable and explainable.
type cacheEntry struct {
	Track model.Track `json:"track"`
	Score int         `json:"score"`
}

// GetResolution returns the cached chosen track (full metadata when stored,
// URI-only for legacy rows — the resolver treats those as misses), the
// original bucket, and the stored score.
func (s *Store) GetResolution(ctx context.Context, key string) (model.Track, string, int, bool) {
	var uri string
	var bucket, trackJSON sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT uri, bucket, track_json FROM resolution_cache WHERE query_key=?`, key).
		Scan(&uri, &bucket, &trackJSON)
	if err != nil {
		return model.Track{}, "", 0, false
	}
	var e cacheEntry
	if trackJSON.String != "" {
		if err := json.Unmarshal([]byte(trackJSON.String), &e); err != nil || e.Track.URI == "" {
			// Legacy format: a bare track object.
			_ = json.Unmarshal([]byte(trackJSON.String), &e.Track)
		}
	}
	e.Track.URI = uri // uri column stays authoritative
	return e.Track, bucket.String, e.Score, true
}

// PutResolution stores a resolver decision with the full chosen track and its
// score, so cache replays carry title/artist/album instead of a hollow URI.
func (s *Store) PutResolution(ctx context.Context, key string, track model.Track, bucket string, score int) error {
	tj, _ := json.Marshal(cacheEntry{Track: track, Score: score})
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO resolution_cache(query_key,uri,bucket,track_json,created_at) VALUES(?,?,?,?,?)`,
		key, track.URI, bucket, string(tj), nowRFC3339())
	return err
}

// --- artist tags ---

// GetArtistTags returns cached tags and similar-artist names for an artist.
func (s *Store) GetArtistTags(ctx context.Context, artist string) (tags, similar []string, ok bool) {
	var tj, sj string
	err := s.db.QueryRowContext(ctx,
		`SELECT tags_json, similar_json FROM artist_tags WHERE artist_name=?`, artist).Scan(&tj, &sj)
	if err != nil {
		return nil, nil, false
	}
	_ = json.Unmarshal([]byte(tj), &tags)
	_ = json.Unmarshal([]byte(sj), &similar)
	return tags, similar, true
}

// PutArtistTags caches tags and similar artists for an artist.
func (s *Store) PutArtistTags(ctx context.Context, artist string, tags, similar []string) error {
	tj, _ := json.Marshal(tags)
	sj, _ := json.Marshal(similar)
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO artist_tags(artist_name,tags_json,similar_json,fetched_at) VALUES(?,?,?,?)`,
		artist, string(tj), string(sj), nowRFC3339())
	return err
}

// --- meta ---

func setMetaTx(ctx context.Context, tx *sql.Tx, key, val string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta(key,value) VALUES(?,?)`, key, val)
	return err
}

// SetMeta upserts a bookkeeping value.
func (s *Store) SetMeta(ctx context.Context, key, val string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO meta(key,value) VALUES(?,?)`, key, val)
	return err
}

// GetMeta reads a bookkeeping value.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool) {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
