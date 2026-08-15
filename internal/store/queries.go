package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emancipat3r/musictool/internal/model"
)

// TrackRef is a compact track reference for summary outputs (guardrail: never
// full objects).
type TrackRef struct {
	ID     string `json:"id"`
	URI    string `json:"uri"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

// LibraryStats is the distilled library summary for get_library_stats.
type LibraryStats struct {
	SavedTracks   int     `json:"saved_tracks"`
	Playlists     int     `json:"playlists"`
	Artists       int     `json:"artists"`
	Albums        int     `json:"albums"`
	PlayEvents    int     `json:"play_events"`
	Keepers       int     `json:"keepers"`
	Disliked      int     `json:"disliked"`
	LastSync      string  `json:"last_sync,omitempty"`
	LastBatch     string  `json:"last_batch,omitempty"`
	TopArtists    []Count `json:"top_artists,omitempty"`
	RecentAdds30d int     `json:"recent_adds_30d"`
}

// Count is a name/count pair.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// RepeatRef is a track with its play count in the signal window.
type RepeatRef struct {
	TrackRef
	Plays int `json:"plays"`
}

// RecentSignals is the distilled feedback since the last batch, for
// get_recent_signals. Compact by construction.
type RecentSignals struct {
	Since                string      `json:"since"`
	NewSaves             []TrackRef  `json:"new_saves"`
	Repeats              []RepeatRef `json:"repeats"`
	NewKeepers           []TrackRef  `json:"new_keepers"`
	NewDislikes          []TrackRef  `json:"new_dislikes"`
	NewSkips             []TrackRef  `json:"new_skips"`
	IgnoredFromLastBatch []TrackRef  `json:"ignored_from_last_batch"`
	Summary              string      `json:"summary"`
}

func countRow(ctx context.Context, db *sql.DB, q string, args ...any) int {
	var n int
	_ = db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n
}

// Stats computes the library summary.
func (s *Store) Stats(ctx context.Context) (LibraryStats, error) {
	st := LibraryStats{
		SavedTracks: countRow(ctx, s.db, `SELECT COUNT(*) FROM saved_tracks`),
		Playlists:   countRow(ctx, s.db, `SELECT COUNT(*) FROM playlists`),
		Artists:     countRow(ctx, s.db, `SELECT COUNT(*) FROM artists`),
		Albums:      countRow(ctx, s.db, `SELECT COUNT(*) FROM albums`),
		PlayEvents:  countRow(ctx, s.db, `SELECT COUNT(*) FROM recently_played`),
		Keepers:     countRow(ctx, s.db, `SELECT COUNT(*) FROM keepers`),
		Disliked:    countRow(ctx, s.db, `SELECT COUNT(*) FROM disliked`),
	}
	if v, ok := s.GetMeta(ctx, "last_sync"); ok {
		st.LastSync = v
	}
	if v, ok := s.GetMeta(ctx, "last_batch"); ok {
		st.LastBatch = v
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	st.RecentAdds30d = countRow(ctx, s.db, `SELECT COUNT(*) FROM saved_tracks WHERE saved_at > ?`, cutoff)

	rows, err := s.db.QueryContext(ctx,
		`SELECT primary_artist, COUNT(*) c FROM tracks
		 JOIN saved_tracks ON saved_tracks.track_id = tracks.id
		 WHERE primary_artist <> '' GROUP BY primary_artist ORDER BY c DESC LIMIT 10`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var c Count
			if err := rows.Scan(&c.Name, &c.Count); err == nil {
				st.TopArtists = append(st.TopArtists, c)
			}
		}
	}
	return st, nil
}

// batchSince returns the reference timestamp for signal windows: the last batch
// time, or 30 days ago if there has been no batch yet.
func (s *Store) batchSince(ctx context.Context) string {
	if v, ok := s.GetMeta(ctx, "last_batch"); ok && v != "" {
		return v
	}
	return time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
}

// Signals distills feedback since the last batch. Every list field is always
// a non-nil slice: [] means "nothing new", never "no data".
func (s *Store) Signals(ctx context.Context) (RecentSignals, error) {
	since := s.batchSince(ctx)
	sig := RecentSignals{
		Since:                since,
		NewSaves:             []TrackRef{},
		Repeats:              []RepeatRef{},
		NewKeepers:           []TrackRef{},
		NewDislikes:          []TrackRef{},
		NewSkips:             []TrackRef{},
		IgnoredFromLastBatch: []TrackRef{},
	}

	// New saves since the window.
	saves, err := s.queryTrackRefs(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist FROM saved_tracks s
		 JOIN tracks t ON t.id=s.track_id
		 WHERE s.saved_at > ? ORDER BY s.saved_at DESC LIMIT 50`, since)
	if err != nil {
		return sig, err
	}
	sig.NewSaves = saves

	// Repeats: tracks played 2+ times since the window.
	rrows, err := s.db.QueryContext(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist,COUNT(*) c
		 FROM recently_played r JOIN tracks t ON t.id=r.track_id
		 WHERE r.played_at > ? GROUP BY r.track_id HAVING c>=2
		 ORDER BY c DESC LIMIT 30`, since)
	if err == nil {
		defer rrows.Close()
		for rrows.Next() {
			var rr RepeatRef
			if err := rrows.Scan(&rr.ID, &rr.URI, &rr.Title, &rr.Artist, &rr.Plays); err == nil {
				sig.Repeats = append(sig.Repeats, rr)
			}
		}
	}

	// New keepers since the window.
	keepers, err := s.queryTrackRefs(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist FROM keepers k
		 JOIN tracks t ON t.id=k.track_id
		 WHERE k.first_seen > ? ORDER BY k.first_seen DESC LIMIT 50`, since)
	if err == nil {
		sig.NewKeepers = keepers
	}

	// New dislikes since the window (explicit-negative votes).
	dislikes, err := s.queryTrackRefs(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist FROM disliked d
		 JOIN tracks t ON t.id=d.track_id
		 WHERE d.first_seen > ? ORDER BY d.first_seen DESC LIMIT 50`, since)
	if err == nil {
		sig.NewDislikes = dislikes
	}

	// Early skips from listen telemetry (weak individually; corroboration
	// thresholds live in the affinity model).
	if skips, err := s.RecentSkips(ctx, since); err == nil {
		sig.NewSkips = skips
	}

	// Ignored: tracks from the last batch playlist with zero plays since it
	// shipped — an honest negative signal (real skip data isn't in the API).
	if ignored := s.ignoredFromLastBatch(ctx); ignored != nil {
		sig.IgnoredFromLastBatch = ignored
	}

	sig.Summary = summarize(sig)
	return sig, nil
}

func (s *Store) ignoredFromLastBatch(ctx context.Context) []TrackRef {
	var outcomesJSON, createdAt, playlistID string
	err := s.db.QueryRowContext(ctx,
		`SELECT playlist_id, created_at, COALESCE(outcomes_json,'') FROM batches
		 ORDER BY id DESC LIMIT 1`).Scan(&playlistID, &createdAt, &outcomesJSON)
	if err != nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist
		 FROM playlist_tracks pt JOIN tracks t ON t.id=pt.track_id
		 WHERE pt.playlist_id=?
		   AND NOT EXISTS (
		     SELECT 1 FROM recently_played r
		     WHERE r.track_id=pt.track_id AND r.played_at > ?
		   ) LIMIT 30`, playlistID, createdAt)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TrackRef
	for rows.Next() {
		var r TrackRef
		if err := rows.Scan(&r.ID, &r.URI, &r.Title, &r.Artist); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// queryTrackRefs always returns a non-nil slice: "no rows" must serialize as
// [] so callers can tell empty from missing (the null-vs-empty ambiguity that
// bit read_playlist and get_recent_signals).
func (s *Store) queryTrackRefs(ctx context.Context, q string, args ...any) ([]TrackRef, error) {
	out := make([]TrackRef, 0, 8)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r TrackRef
		if err := rows.Scan(&r.ID, &r.URI, &r.Title, &r.Artist); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LikedSongs returns a compact, paginated slice of liked songs.
func (s *Store) LikedSongs(ctx context.Context, limit, offset int) ([]TrackRef, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.queryTrackRefs(ctx,
		`SELECT t.id,t.uri,t.title,t.primary_artist FROM saved_tracks s
		 JOIN tracks t ON t.id=s.track_id
		 ORDER BY s.saved_at DESC LIMIT ? OFFSET ?`, limit, offset)
}

// Playlists returns compact playlist metadata.
func (s *Store) Playlists(ctx context.Context) ([]model.Playlist, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,COALESCE(description,''),public,COALESCE(owner_id,''),track_count
		 FROM playlists ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Playlist
	for rows.Next() {
		var p model.Playlist
		var pub int
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &pub, &p.OwnerID, &p.TrackCount); err != nil {
			return nil, err
		}
		p.Public = pub == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// (The old PlaylistTracks id-or-name lookup was removed: it had no callers and
// its unscoped WHERE name=? LIMIT 1 was exactly the ambiguity hazard the
// owner-scoped service.findPlaylist exists to prevent. Live playlist reads go
// through service.ReadPlaylist.)

// SyncKeepers snapshots current membership of the Keepers playlist. New members
// get a first_seen stamp; departed members are removed.
func (s *Store) SyncKeepers(ctx context.Context, trackIDs []string) error {
	return s.syncVoteTable(ctx, "keepers", trackIDs)
}

// SyncDisliked snapshots the Disliked playlist (the explicit-negative channel)
// with the same diff semantics as Keepers.
func (s *Store) SyncDisliked(ctx context.Context, trackIDs []string) error {
	return s.syncVoteTable(ctx, "disliked", trackIDs)
}

// syncVoteTable diffs a vote playlist's membership into its snapshot table.
// table is a compile-time constant name, never user input.
func (s *Store) syncVoteTable(ctx context.Context, table string, trackIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := nowRFC3339()
	present := map[string]bool{}
	for _, id := range trackIDs {
		present[id] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+table+`(track_id,first_seen) VALUES(?,?)
			 ON CONFLICT(track_id) DO NOTHING`, id, now); err != nil {
			return err
		}
	}
	// Remove votes no longer in the playlist.
	rows, err := tx.QueryContext(ctx, `SELECT track_id FROM `+table)
	if err != nil {
		return err
	}
	var toDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !present[id] {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()
	for _, id := range toDelete {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE track_id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DislikedKeys returns the disliked track ids and, where known, their ISRCs —
// so a build can refuse a disliked recording even when it resolves to a
// different release of the same song.
func (s *Store) DislikedKeys(ctx context.Context) (ids, isrcs map[string]bool, err error) {
	ids, isrcs = map[string]bool{}, map[string]bool{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.track_id, COALESCE(t.isrc,'') FROM disliked d
		 LEFT JOIN tracks t ON t.id = d.track_id`)
	if err != nil {
		return ids, isrcs, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, isrc string
		if err := rows.Scan(&id, &isrc); err != nil {
			return ids, isrcs, err
		}
		ids[id] = true
		if isrc != "" {
			isrcs[isrc] = true
		}
	}
	return ids, isrcs, rows.Err()
}

// Batch is a shipped discovery batch record.
type Batch struct {
	ID         int64  `json:"id"`
	Label      string `json:"label"`
	PlaylistID string `json:"playlist_id"`
	CreatedAt  string `json:"created_at"`
	TrackCount int    `json:"track_count"`
	Digest     string `json:"digest,omitempty"`
}

// RecordBatch inserts a batch record and marks last_batch.
func (s *Store) RecordBatch(ctx context.Context, b Batch, outcomes any) error {
	oj, _ := json.Marshal(outcomes)
	created := b.CreatedAt
	if created == "" {
		created = nowRFC3339()
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO batches(label,playlist_id,created_at,track_count,digest,outcomes_json)
		 VALUES(?,?,?,?,?,?)`,
		b.Label, b.PlaylistID, created, b.TrackCount, b.Digest, string(oj)); err != nil {
		return err
	}
	return s.SetMeta(ctx, "last_batch", created)
}

// ListBatches returns recent batches for the dashboard.
func (s *Store) ListBatches(ctx context.Context, limit int) ([]Batch, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,COALESCE(label,''),COALESCE(playlist_id,''),COALESCE(created_at,''),
		        COALESCE(track_count,0),COALESCE(digest,'')
		 FROM batches ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Batch
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.Label, &b.PlaylistID, &b.CreatedAt, &b.TrackCount, &b.Digest); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ArtistCard is a dashboard row: artist depth plus a representative cover.
type ArtistCard struct {
	Name     string `json:"name"`
	Count    int    `json:"count"`
	ImageURL string `json:"image_url,omitempty"`
}

// TopArtistCards returns the deepest liked artists with a representative album
// image (dashboard only; MCP outputs stay compact).
func (s *Store) TopArtistCards(ctx context.Context, limit int) ([]ArtistCard, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.primary_artist, COUNT(*) c,
		        COALESCE((SELECT al.image_url FROM tracks t2
		                  JOIN albums al ON al.id = t2.album_id
		                  WHERE t2.primary_artist = t.primary_artist
		                    AND COALESCE(al.image_url,'') <> ''
		                  LIMIT 1), '')
		 FROM tracks t JOIN saved_tracks s ON s.track_id = t.id
		 WHERE t.primary_artist <> ''
		 GROUP BY t.primary_artist ORDER BY c DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ArtistCard, 0, limit)
	for rows.Next() {
		var a ArtistCard
		if err := rows.Scan(&a.Name, &a.Count, &a.ImageURL); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CoverRef is a dashboard tile: a track with its album art and vote state.
type CoverRef struct {
	// ID is what the template embeds (data-track): html/template sanitizes
	// spotify: URIs in *-uri attributes to #ZgotmplZ, so the client rebuilds
	// the URI from the bare id.
	ID       string `json:"id"`
	URI      string `json:"uri"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	ImageURL string `json:"image_url,omitempty"`
	Keeper   bool   `json:"keeper"`
	Disliked bool   `json:"disliked"`
}

const coverSelect = `SELECT t.id, t.uri, t.title, t.primary_artist, COALESCE(al.image_url,''),
	EXISTS(SELECT 1 FROM keepers k WHERE k.track_id=t.id),
	EXISTS(SELECT 1 FROM disliked d WHERE d.track_id=t.id)`

func (s *Store) queryCovers(ctx context.Context, q string, args ...any) ([]CoverRef, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CoverRef, 0, 18)
	for rows.Next() {
		var c CoverRef
		if err := rows.Scan(&c.ID, &c.URI, &c.Title, &c.Artist, &c.ImageURL, &c.Keeper, &c.Disliked); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecentSaveCovers returns the latest liked songs with album art for the
// dashboard's cover grid.
func (s *Store) RecentSaveCovers(ctx context.Context, limit int) ([]CoverRef, error) {
	if limit <= 0 {
		limit = 18
	}
	return s.queryCovers(ctx, coverSelect+`
		 FROM saved_tracks s
		 JOIN tracks t ON t.id = s.track_id
		 LEFT JOIN albums al ON al.id = t.album_id
		 ORDER BY s.saved_at DESC LIMIT ?`, limit)
}

// LatestBatchCovers returns the most recent discovery batch's tracks with art
// and vote state, so the dashboard can collect verdicts on the batch.
func (s *Store) LatestBatchCovers(ctx context.Context) (string, []CoverRef, error) {
	var label, playlistID string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(label,''), COALESCE(playlist_id,'') FROM batches ORDER BY id DESC LIMIT 1`).
		Scan(&label, &playlistID)
	if err != nil {
		return "", nil, nil // no batches yet
	}
	covers, err := s.queryCovers(ctx, coverSelect+`
		 FROM playlist_tracks pt
		 JOIN tracks t ON t.id = pt.track_id
		 LEFT JOIN albums al ON al.id = t.album_id
		 WHERE pt.playlist_id = ? ORDER BY pt.position`, playlistID)
	return label, covers, err
}

// PlayHistoryDaily returns per-day play counts for the last n days (dashboard).
func (s *Store) PlayHistoryDaily(ctx context.Context, days int) ([]Count, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT substr(played_at,1,10) d, COUNT(*) c FROM recently_played
		 WHERE played_at > ? GROUP BY d ORDER BY d`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

func summarize(sig RecentSignals) string {
	return fmt.Sprintf("since %s: %d new saves, %d repeats, %d new keepers, %d new dislikes, %d early skips, %d ignored from last batch",
		sig.Since, len(sig.NewSaves), len(sig.Repeats), len(sig.NewKeepers), len(sig.NewDislikes), len(sig.NewSkips), len(sig.IgnoredFromLastBatch))
}
