// Package spotify is a thin, auditable HTTP client over the documented Spotify
// Web API endpoints spotifytool actually uses. No SDK, no algorithmic surfaces
// (audio-features/recommendations/related-artists are deprecated upstream).
package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/emancipat3r/spotifytool/internal/apperr"
	"github.com/emancipat3r/spotifytool/internal/config"
	"github.com/emancipat3r/spotifytool/internal/logx"
)

// tokenProvider yields a valid bearer token, refreshing as needed.
type tokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
}

// Client talks to the Spotify Web API. It honors Retry-After on 429 and retries
// transient 5xx up to maxRetries. Requests are sequential — sufficient at this
// personal scale.
type Client struct {
	http    *http.Client
	tokens  tokenProvider
	base    string
	maxTry  int
	backoff time.Duration
}

// NewClient builds a Spotify client from a token provider.
func NewClient(tokens tokenProvider) *Client {
	return &Client{
		http:    &http.Client{Timeout: 45 * time.Second},
		tokens:  tokens,
		base:    config.APIBase,
		maxTry:  3,
		backoff: time.Second,
	}
}

// apiError is Spotify's error envelope.
type apiError struct {
	Err struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

// do performs an authenticated request against path (absolute URL if it starts
// with http, else base+path), retrying transient failures. body may be nil.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	url := path
	if len(path) == 0 || path[0] == '/' {
		url = c.base + path
	}

	var rawBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		rawBody = b
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxTry; attempt++ {
		tok, err := c.tokens.AccessToken(ctx)
		if err != nil {
			return err // already an apperr.Auth
		}
		var reader io.Reader
		if rawBody != nil {
			reader = bytes.NewReader(rawBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if rawBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		res, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !sleepCtx(ctx, c.backoff*time.Duration(attempt+1)) {
				return apperr.API(ctx.Err())
			}
			continue
		}

		// Handle rate limiting: honor Retry-After and retry.
		if res.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(res)
			res.Body.Close()
			logx.Debugf("429 from %s; sleeping %s", url, wait)
			if !sleepCtx(ctx, wait) {
				return apperr.API(ctx.Err())
			}
			continue
		}

		// Retry transient 5xx.
		if res.StatusCode >= 500 {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			res.Body.Close()
			lastErr = fmt.Errorf("upstream %d: %s", res.StatusCode, snippet)
			if !sleepCtx(ctx, c.backoff*time.Duration(attempt+1)) {
				return apperr.API(ctx.Err())
			}
			continue
		}

		defer res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			return apperr.Auth(fmt.Errorf("401 from Spotify; token invalid — run `spotifytool auth`"))
		}
		if res.StatusCode >= 400 {
			return apperr.API(decodeAPIError(res))
		}

		if out == nil || res.StatusCode == http.StatusNoContent {
			io.Copy(io.Discard, res.Body)
			return nil
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return apperr.API(fmt.Errorf("decode %s response: %w", path, err))
		}
		return nil
	}
	return apperr.API(fmt.Errorf("giving up on %s %s after %d attempts: %w", method, path, c.maxTry+1, lastErr))
}

func decodeAPIError(res *http.Response) error {
	var ae apiError
	b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	_ = json.Unmarshal(b, &ae)
	if ae.Err.Message != "" {
		return fmt.Errorf("spotify API %d: %s", res.StatusCode, ae.Err.Message)
	}
	return fmt.Errorf("spotify API %d: %s", res.StatusCode, bytes.TrimSpace(b))
}

// retryAfter reads the Retry-After header (seconds), defaulting to a sane value.
func retryAfter(res *http.Response) time.Duration {
	if v := res.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs)*time.Second + 500*time.Millisecond
		}
	}
	return 2 * time.Second
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false if ctx
// was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
