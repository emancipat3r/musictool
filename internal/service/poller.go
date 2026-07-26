package service

import (
	"context"
	"strings"
	"time"

	"github.com/emancipat3r/spotifytool/internal/logx"
	"github.com/emancipat3r/spotifytool/internal/model"
)

// Listen telemetry: poll the read-only currently-playing endpoint and classify
// each listen when the track changes or playback stops. recently_played only
// logs completions, so this is the only source of skip/partial signal.
//
// Classification is approximate by nature (poll granularity ~20s); the
// affinity model's weights already treat single skips as weak evidence, so
// the noise floor is priced in.

// classifyListen buckets a finished listen by how far the user got.
// skip_early = bailed before the 30s mark Spotify itself treats as the
// negative-signal boundary; completed = reached 80%+.
func classifyListen(maxProgressMs, durationMs int) string {
	if durationMs > 0 && maxProgressMs >= durationMs*4/5 {
		return "completed"
	}
	switch {
	case maxProgressMs < 30_000:
		return "skip_early"
	case durationMs > 0 && maxProgressMs < durationMs/2:
		return "skip_mid"
	default:
		return "partial"
	}
}

// isRestart detects the strong-engagement signal of scrubbing back to the
// start after substantial listening.
func isRestart(prevProgressMs, newProgressMs int) bool {
	return prevProgressMs > 30_000 && newProgressMs+5_000 < prevProgressMs && newProgressMs < 15_000
}

// listenState tracks the in-flight listen between polls.
type listenState struct {
	track        model.Track
	startedAt    time.Time
	maxProgress  int
	lastProgress int
	duration     int
}

// StartListenPoller runs the telemetry loop until ctx is done. Call in a
// goroutine from serve. Missing scope (403) backs off to a slow retry so a
// pre-rescope token does not spam the API or the logs.
func (s *Service) StartListenPoller(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	logx.Infof("listen poller: every %s (read-only currently-playing)", interval)
	var cur *listenState
	warned := false

	finalize := func() {
		if cur == nil {
			return
		}
		st := cur
		cur = nil
		if st.maxProgress < 5_000 {
			return // queue-jump noise, not a listen
		}
		outcome := classifyListen(st.maxProgress, st.duration)
		if err := s.DB.RecordListen(ctx, st.track, st.startedAt, time.Now(), st.maxProgress, st.duration, outcome); err != nil {
			logx.Errorf("listen poller: record: %v", err)
			return
		}
		logx.Debugf("listen: %s — %s [%s]", st.track.ArtistName(), st.track.Title, outcome)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			finalize()
			return
		case <-t.C:
		}

		np, err := s.SP.CurrentlyPlaying(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "403") {
				if !warned {
					logx.Errorf("listen poller: 403 (token lacks user-read-currently-playing; re-run auth to enable telemetry); retrying hourly")
					warned = true
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Hour):
				}
				continue
			}
			logx.Debugf("listen poller: %v", err)
			continue
		}
		warned = false

		switch {
		case np == nil:
			finalize()
		case cur == nil || np.Track.ID != cur.track.ID:
			finalize()
			cur = &listenState{
				track:        np.Track,
				startedAt:    time.Now(),
				maxProgress:  np.ProgressMs,
				lastProgress: np.ProgressMs,
				duration:     np.Track.DurationMs,
			}
		default:
			if isRestart(cur.lastProgress, np.ProgressMs) {
				// Record the pre-restart listen as its own engagement event.
				_ = s.DB.RecordListen(ctx, cur.track, cur.startedAt, time.Now(), cur.lastProgress, cur.duration, "restart")
				cur.startedAt = time.Now()
				cur.maxProgress = np.ProgressMs
			}
			if np.ProgressMs > cur.maxProgress {
				cur.maxProgress = np.ProgressMs
			}
			cur.lastProgress = np.ProgressMs
		}
	}
}
