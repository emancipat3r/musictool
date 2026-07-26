package store

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/emancipat3r/spotifytool/internal/model"
)

// Evidence-weighted affinity model. Design follows the implicit-feedback
// literature rather than naive counting:
//
//   - Explicit beats implicit, always: Disliked and Keepers votes carry the
//     largest weights and never decay (true negative/positive feedback
//     outperforms inferred signals — UMAP'24 "Negative Feedback for Music
//     Personalization").
//   - Repetition is the reliable implicit positive: the same track completed
//     across separate sessions is worth far more than one play (Deezer
//     UMAP'25 treats repeat-consistency as the reliability measure).
//   - Skips are NOT clean negatives: listeners skip most within catalogs
//     they engage with most (RecSys/CIKM skip studies), so single skips are
//     nearly ignored and skip evidence only counts against an artist with
//     corroboration (see thresholds in TasteDeltas).
//   - Implicit evidence decays with a half-life (taste drift); explicit
//     votes and the user's own words in the profile do not.
const (
	wKeeper   = 5.0  // explicit positive vote (no decay)
	wDisliked = -6.0 // explicit negative vote (no decay)
	wSaved    = 3.0  // liked song (no decay while saved)

	wCompleted = 0.5  // one full listen (decayed)
	wRestart   = 1.0  // restarted the track: strong engagement (decayed)
	wSkipEarly = -0.4 // skipped before 30s (weak alone, by design)
	wSkipMid   = -0.2 // bailed before halfway
	wPartial   = 0.0  // late bail: contextual, not taste

	implicitHalfLifeDays = 180.0
)

// decayFactor halves implicit evidence every implicitHalfLifeDays.
func decayFactor(ageDays float64) float64 {
	if ageDays <= 0 {
		return 1
	}
	return math.Pow(0.5, ageDays/implicitHalfLifeDays)
}

// listenWeight maps a listen outcome to its decayed weight.
func listenWeight(outcome string, ageDays float64) float64 {
	var w float64
	switch outcome {
	case "completed":
		w = wCompleted
	case "restart":
		w = wRestart
	case "skip_early":
		w = wSkipEarly
	case "skip_mid":
		w = wSkipMid
	default:
		w = wPartial
	}
	return w * decayFactor(ageDays)
}

// ArtistDelta is one artist's evidence-weighted standing, exposed to Claude
// for taste-profile iteration. Evidence counts let the caller apply the
// corroboration thresholds instead of trusting a bare number.
type ArtistDelta struct {
	Artist   string         `json:"artist"`
	Affinity float64        `json:"affinity"`
	Trend    string         `json:"trend"` // rising | falling | steady
	Evidence map[string]int `json:"evidence"`
}

// TasteDeltas computes per-artist affinity from all channels. Thresholds
// (documented in the MCP tool description and CLAUDE.md):
//   - rising:  affinity >= +2.0 with >= 3 positive events
//   - falling: affinity <= -1.5 with >= 2 negative events, at least one of
//     which is explicit OR a second independent skip — a single skip can
//     never demote an artist (skips correlate with engagement).
func (s *Store) TasteDeltas(ctx context.Context) ([]ArtistDelta, error) {
	now := time.Now().UTC()
	type acc struct {
		affinity float64
		evidence map[string]int
	}
	byArtist := map[string]*acc{}
	get := func(artist string) *acc {
		if artist == "" {
			artist = "(unknown)"
		}
		a, ok := byArtist[artist]
		if !ok {
			a = &acc{evidence: map[string]int{}}
			byArtist[artist] = a
		}
		return a
	}

	// Explicit + saved channels (no decay).
	type simpleQ struct {
		sql    string
		weight float64
		label  string
	}
	for _, q := range []simpleQ{
		{`SELECT t.primary_artist FROM keepers k JOIN tracks t ON t.id=k.track_id`, wKeeper, "keepers"},
		{`SELECT t.primary_artist FROM disliked d JOIN tracks t ON t.id=d.track_id`, wDisliked, "dislikes"},
		{`SELECT t.primary_artist FROM saved_tracks s JOIN tracks t ON t.id=s.track_id`, wSaved, "saves"},
	} {
		rows, err := s.db.QueryContext(ctx, q.sql)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var artist string
			if err := rows.Scan(&artist); err != nil {
				rows.Close()
				return nil, err
			}
			a := get(artist)
			a.affinity += q.weight
			a.evidence[q.label]++
		}
		rows.Close()
	}

	// Listen telemetry (decayed).
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.primary_artist, l.outcome, COALESCE(l.ended_at,'')
		 FROM listen_events l JOIN tracks t ON t.id=l.track_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var artist, outcome, endedAt string
		if err := rows.Scan(&artist, &outcome, &endedAt); err != nil {
			rows.Close()
			return nil, err
		}
		age := 0.0
		if ts, err := time.Parse(time.RFC3339, endedAt); err == nil {
			age = now.Sub(ts).Hours() / 24
		}
		a := get(artist)
		a.affinity += listenWeight(outcome, age)
		a.evidence[outcome]++
	}
	rows.Close()

	out := make([]ArtistDelta, 0, len(byArtist))
	for artist, a := range byArtist {
		out = append(out, ArtistDelta{
			Artist:   artist,
			Affinity: math.Round(a.affinity*100) / 100,
			Trend:    classifyTrend(a.affinity, a.evidence),
			Evidence: a.evidence,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := math.Abs(out[i].Affinity), math.Abs(out[j].Affinity)
		if ai != aj {
			return ai > aj
		}
		return out[i].Artist < out[j].Artist
	})
	return out, nil
}

// classifyTrend applies the corroboration thresholds.
func classifyTrend(affinity float64, ev map[string]int) string {
	pos := ev["keepers"] + ev["saves"] + ev["completed"] + ev["restart"]
	negEvents := ev["dislikes"] + ev["skip_early"] + ev["skip_mid"]
	explicitNeg := ev["dislikes"] > 0
	switch {
	case affinity >= 2.0 && pos >= 3:
		return "rising"
	case affinity <= -1.5 && negEvents >= 2 && (explicitNeg || ev["skip_early"] >= 2):
		return "falling"
	default:
		return "steady"
	}
}

// RecordListen persists one classified listen, upserting a minimal track row
// so telemetry of never-synced tracks still joins.
func (s *Store) RecordListen(ctx context.Context, t model.Track, startedAt, endedAt time.Time, maxProgressMs, durationMs int, outcome string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO tracks(id,uri,title,primary_artist,duration_ms,updated_at)
		 VALUES(?,?,?,?,?,?)`,
		t.ID, t.URI, t.Title, t.ArtistName(), durationMs, nowRFC3339()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO listen_events(track_id,started_at,ended_at,max_progress_ms,duration_ms,outcome)
		 VALUES(?,?,?,?,?,?)`,
		t.ID, startedAt.UTC().Format(time.RFC3339), endedAt.UTC().Format(time.RFC3339),
		maxProgressMs, durationMs, outcome); err != nil {
		return err
	}
	return tx.Commit()
}

// RecentSkips returns early-skipped tracks since the given RFC3339 timestamp
// (for get_recent_signals).
func (s *Store) RecentSkips(ctx context.Context, since string) ([]TrackRef, error) {
	return s.queryTrackRefs(ctx,
		`SELECT DISTINCT t.id,t.uri,t.title,t.primary_artist
		 FROM listen_events l JOIN tracks t ON t.id=l.track_id
		 WHERE l.outcome='skip_early' AND l.ended_at > ?
		 ORDER BY t.primary_artist LIMIT 30`, since)
}

// SetLocalVote reflects a dashboard vote immediately in the local snapshot
// tables (the canonical write goes to the Spotify playlist; the next sync
// reconciles). action is "keeper" or "dislike".
func (s *Store) SetLocalVote(ctx context.Context, action, trackID string) error {
	table, opposite := "keepers", "disliked"
	if action == "dislike" {
		table, opposite = "disliked", "keepers"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+table+`(track_id,first_seen) VALUES(?,?) ON CONFLICT(track_id) DO NOTHING`,
		trackID, nowRFC3339()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+opposite+` WHERE track_id=?`, trackID); err != nil {
		return err
	}
	return tx.Commit()
}
