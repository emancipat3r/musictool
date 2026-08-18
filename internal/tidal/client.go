// Package tidal is a thin, auditable HTTP client over the documented TIDAL
// API v2 (openapi.tidal.com, JSON:API) — the TIDAL implementation of
// provider.Client. Endpoint shapes were taken from TIDAL's published OpenAPI
// spec and live-verified 2026-08-14 (auth, search, ISRC lookup, playlist
// create/add/read-back/delete).
//
// Provider notes vs Spotify:
//   - No listen telemetry or play history: the third-party API has no
//     currently-playing or recently-played endpoints, so those Capabilities
//     are off and the feedback loop runs on explicit signals only.
//   - Popularity is a 0..1 float; scaled to the model's 0..100 int here.
//   - Durations are ISO-8601 ("PT3M35S"); parsed to milliseconds here.
//   - API-created playlists are PUBLIC or UNLISTED — never truly PRIVATE.
package tidal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emancipat3r/musictool/internal/apperr"
	"github.com/emancipat3r/musictool/internal/config"
	"github.com/emancipat3r/musictool/internal/logx"
	"github.com/emancipat3r/musictool/internal/provider"
)

const jsonAPIType = "application/vnd.api+json"

// tokenProvider yields a valid bearer token, refreshing as needed.
type tokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
}

// Client talks to the TIDAL API. It honors Retry-After on 429 and retries
// transient 5xx up to maxTry. Requests are sequential — sufficient at this
// personal scale.
type Client struct {
	http    *http.Client
	tokens  tokenProvider
	base    string
	country string
	maxTry  int
	backoff time.Duration

	userID string // cached current-user id (JSON:API "me" resolution)
}

// Compile-time proof that Client implements the provider surface.
var _ provider.Client = (*Client)(nil)

// NewClient builds a TIDAL client. country is the ISO 3166-1 alpha-2 code most
// endpoints require (catalog availability differs by market).
func NewClient(tokens tokenProvider, country string) *Client {
	if country == "" {
		country = "US"
	}
	return &Client{
		http:    &http.Client{Timeout: 45 * time.Second},
		tokens:  tokens,
		base:    config.TidalAPIBase,
		country: country,
		maxTry:  3,
		backoff: time.Second,
	}
}

// Name is the provider key stamped into the store and resolver cache.
func (c *Client) Name() string { return "tidal" }

// Capabilities: no telemetry channel exists in TIDAL's third-party API, and
// playlists cap out at UNLISTED visibility.
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		ListenTelemetry:  false,
		PlayHistory:      false,
		PrivatePlaylists: false,
	}
}

// PlaylistURI and TrackURI render ids in the tidal URI scheme (internal to
// musictool; TIDAL itself uses bare ids).
func (c *Client) PlaylistURI(id string) string { return "tidal:playlist:" + id }
func (c *Client) TrackURI(id string) string    { return trackURI(id) }

// TrackID extracts the bare id from a tidal:track: URI.
func (c *Client) TrackID(uri string) (string, bool) {
	id := strings.TrimPrefix(uri, "tidal:track:")
	if id == uri || id == "" {
		return "", false
	}
	return id, true
}

func trackURI(id string) string { return "tidal:track:" + id }

// jaError is TIDAL's JSON:API error envelope.
type jaError struct {
	Errors []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

// do performs an authenticated JSON:API request. path is relative to the API
// base unless absolute; query may be nil. body (marshaled as JSON:API) and out
// may be nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out *jaDocument) error {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.base + path
	}
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + query.Encode()
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
		req, err := http.NewRequestWithContext(ctx, method, u, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", jsonAPIType)
		if rawBody != nil {
			req.Header.Set("Content-Type", jsonAPIType)
		}

		res, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !sleepCtx(ctx, c.backoff*time.Duration(attempt+1)) {
				return apperr.API(ctx.Err())
			}
			continue
		}

		if res.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(res)
			res.Body.Close()
			lastErr = fmt.Errorf("rate limited (429, Retry-After %s)", wait)
			logx.Debugf("429 from %s; sleeping %s", u, wait)
			if !sleepCtx(ctx, wait) {
				return apperr.API(ctx.Err())
			}
			continue
		}
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
			return apperr.Auth(fmt.Errorf("401 from TIDAL; token invalid — run `musictool auth` with MUSIC_PROVIDER=tidal"))
		}
		if res.StatusCode >= 400 {
			return apperr.API(decodeAPIError(res))
		}

		if out == nil || res.StatusCode == http.StatusNoContent {
			io.Copy(io.Discard, res.Body)
			return nil
		}
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			return apperr.API(fmt.Errorf("read %s response: %w", path, err))
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return apperr.API(fmt.Errorf("decode %s response: %w", path, err))
		}
		return nil
	}
	return apperr.API(fmt.Errorf("giving up on %s %s after %d attempts: %w", method, path, c.maxTry+1, lastErr))
}

func decodeAPIError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	var je jaError
	_ = json.Unmarshal(b, &je)
	if len(je.Errors) > 0 {
		return fmt.Errorf("tidal API %d: %s %s", res.StatusCode, je.Errors[0].Code, je.Errors[0].Detail)
	}
	return fmt.Errorf("tidal API %d: %s", res.StatusCode, bytes.TrimSpace(b))
}

// retryAfter reads the Retry-After header (seconds), defaulting to a sane value.
func retryAfter(res *http.Response) time.Duration {
	if v := res.Header.Get("Retry-After"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil && d >= 0 {
			return d + 500*time.Millisecond
		}
	}
	return 2 * time.Second
}

// sleepCtx sleeps for d unless ctx is cancelled first.
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

// ---- JSON:API document shapes -------------------------------------------

// jaDocument is a generic JSON:API response envelope. Data may be one resource
// or an array; Included carries side-loaded resources.
type jaDocument struct {
	Data     json.RawMessage `json:"data"`
	Included []jaResource    `json:"included"`
	Links    struct {
		Next string `json:"next"`
	} `json:"links"`
}

type jaResource struct {
	ID            string                    `json:"id"`
	Type          string                    `json:"type"`
	Attributes    json.RawMessage           `json:"attributes"`
	Relationships map[string]jaRelationship `json:"relationships"`
	Meta          json.RawMessage           `json:"meta"`
}

type jaRelationship struct {
	Data json.RawMessage `json:"data"`
}

// jaRef is a resource identifier ({id, type, meta?}).
type jaRef struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

// dataResources decodes a document's data as a resource list (accepting a
// single resource too).
func (d *jaDocument) dataResources() []jaResource {
	return decodeResources(d.Data)
}

func decodeResources(raw json.RawMessage) []jaResource {
	if len(raw) == 0 {
		return nil
	}
	var many []jaResource
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	var one jaResource
	if err := json.Unmarshal(raw, &one); err == nil && one.ID != "" {
		return []jaResource{one}
	}
	return nil
}

// decodeRefs decodes a relationship's data as identifier list (or single).
func decodeRefs(raw json.RawMessage) []jaRef {
	if len(raw) == 0 {
		return nil
	}
	var many []jaRef
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	var one jaRef
	if err := json.Unmarshal(raw, &one); err == nil && one.ID != "" {
		return []jaRef{one}
	}
	return nil
}

// relRefs returns a resource's relationship identifiers by name.
func (r jaResource) relRefs(name string) []jaRef {
	rel, ok := r.Relationships[name]
	if !ok {
		return nil
	}
	return decodeRefs(rel.Data)
}

// getPaged walks a paginated collection, calling visit for each page's
// document until there is no next link. TIDAL's next links are
// root-relative (e.g. "/v2/playlists?page[cursor]=…") or full URLs.
func (c *Client) getPaged(ctx context.Context, path string, query url.Values, visit func(*jaDocument) error) error {
	next := path
	q := query
	for next != "" {
		var doc jaDocument
		if err := c.do(ctx, "GET", next, q, nil, &doc); err != nil {
			return err
		}
		if err := visit(&doc); err != nil {
			return err
		}
		next = c.resolveNext(doc.Links.Next)
		q = nil // the next link carries its own query
	}
	return nil
}

// resolveNext normalizes a JSON:API next link against the API base.
func (c *Client) resolveNext(next string) string {
	if next == "" || strings.HasPrefix(next, "http") {
		return next
	}
	// Links come back root-relative including the /v2 prefix; the base
	// already ends in /v2, so strip a duplicate.
	if strings.HasPrefix(next, "/v2/") {
		return c.base + strings.TrimPrefix(next, "/v2")
	}
	if !strings.HasPrefix(next, "/") {
		next = "/" + next
	}
	return c.base + next
}
