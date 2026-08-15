package spotify

import (
	"context"

	"github.com/emancipat3r/musictool/internal/provider"
)

// CurrentlyPlaying returns the current player snapshot, or nil when nothing is
// playing (the API answers 204). Requires the user-read-currently-playing
// scope; without it the API returns 403 and the poller backs off.
func (c *Client) CurrentlyPlaying(ctx context.Context) (*provider.NowPlaying, error) {
	var resp struct {
		IsPlaying  bool     `json:"is_playing"`
		ProgressMs int      `json:"progress_ms"`
		Item       apiTrack `json:"item"`
	}
	// A 204 decodes nothing; detect it by the zero item.
	if err := c.do(ctx, "GET", "/me/player/currently-playing", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Item.ID == "" {
		return nil, nil
	}
	return &provider.NowPlaying{
		Track:      resp.Item.toModel(),
		ProgressMs: resp.ProgressMs,
		IsPlaying:  resp.IsPlaying,
	}, nil
}
