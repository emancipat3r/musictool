// Package config centralizes filesystem paths, environment lookups, and the
// fixed Spotify constants (redirect URI, scopes) used across spotifytool.
//
// Path resolution honors XDG and explicit overrides so the same binary works
// both on a developer laptop (auth once) and inside container 2 (shared
// volume). Nothing here reads secrets to stdout or logs them.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// RedirectURI is registered verbatim in the Spotify app. Loopback IP
	// literals are the only permitted HTTP redirect (localhost aliases were
	// prohibited by the Nov 2025 migration), so both the authorize URL and the
	// local listener must use 127.0.0.1.
	RedirectURI = "http://127.0.0.1:8888/callback"

	// LoopbackAddr is the host:port the one-shot auth listener binds. It must
	// match RedirectURI's authority exactly.
	LoopbackAddr = "127.0.0.1:8888"

	// Spotify OAuth + API base URLs.
	AuthorizeURL = "https://accounts.spotify.com/authorize"
	TokenURL     = "https://accounts.spotify.com/api/token"
	APIBase      = "https://api.spotify.com/v1"

	// TIDAL OAuth + API base URLs (openapi.tidal.com, JSON:API). Same
	// Authorization Code + PKCE flow; live-verified 2026-08-14.
	TidalAuthorizeURL = "https://login.tidal.com/authorize"
	TidalTokenURL     = "https://auth.tidal.com/v1/oauth2/token"
	TidalAPIBase      = "https://openapi.tidal.com/v2"
)

// Scopes is the exact set the PRD requires: read the library and recent/top
// signals, and read+modify private/public playlists. No playback scopes.
var Scopes = []string{
	"user-library-read",
	"playlist-read-private",
	"playlist-read-collaborative",
	"playlist-modify-private",
	"playlist-modify-public",
	"user-read-recently-played",
	"user-top-read",
	// Listen telemetry: read-only "what's playing now" polling for skip /
	// completion / repeat detection. Still no playback control.
	"user-read-currently-playing",
}

// TidalScopes is the TIDAL equivalent. These five must be ENABLED on the app
// in the TIDAL developer dashboard or the authorize step fails. TIDAL has no
// telemetry or playback-history scopes — that whole channel does not exist in
// its third-party API.
var TidalScopes = []string{
	"playlists.read",
	"playlists.write",
	"search.read",
	"collection.read",
	"user.read",
}

// Config is the resolved runtime configuration. Secrets (client id, refresh
// token, last.fm key) live only in memory here, never on stdout.
type Config struct {
	// Provider selects the music backend: "spotify" (default) or "tidal"
	// (MUSIC_PROVIDER). One provider per deployment; the store stamps it and
	// refuses to open under the other.
	Provider string

	ClientID     string // SPOTIFY_CLIENT_ID (public, required for spotify)
	RefreshToken string // SPOTIFY_REFRESH_TOKEN (optional headless bootstrap)
	LastFMKey    string // LASTFM_API_KEY (optional, discovery seeds)

	TidalClientID     string // TIDAL_CLIENT_ID (public, required for tidal)
	TidalRefreshToken string // TIDAL_REFRESH_TOKEN (optional headless bootstrap)
	TidalCountry      string // TIDAL_COUNTRY (ISO 3166-1 alpha-2, default US)

	// TerminalURL is the Zellij web client address (SPOTIFYTOOL_TERMINAL_URL),
	// e.g. https://homelab.tailnet-name.ts.net:8082. When set, the dashboard
	// grows a terminal pane (iframe + open-in-tab link) per the PRD's
	// control-room layout. Empty hides the pane.
	TerminalURL string

	// TerminalURLHTTP is the plain-HTTP terminal proxy address served to
	// clients that arrive over HTTP (the companion app), where WebView TLS
	// quirks make wss unreliable (SPOTIFYTOOL_TERMINAL_URL_HTTP).
	TerminalURLHTTP string

	// TriggerURL is the sandbox's command-injection endpoint
	// (SPOTIFYTOOL_TRIGGER_URL, e.g. http://sandbox:8090) used by the
	// dashboard's "Run discovery batch" button. Empty disables the button's
	// backend.
	TriggerURL string

	ConfigDir string // holds token.json
	DataDir   string // holds the SQLite db, taste profile, digests
}

// Env reads a MUSICTOOL_-prefixed variable, falling back to the legacy
// SPOTIFYTOOL_ prefix so pre-rename deployments keep working unchanged.
func Env(suffix string) string {
	if v := strings.TrimSpace(os.Getenv("MUSICTOOL_" + suffix)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("SPOTIFYTOOL_" + suffix))
}

// TokenPath is the 0600 token cache location, per provider so switching
// MUSIC_PROVIDER never reads the other provider's tokens. Spotify keeps the
// historical token.json name.
func (c Config) TokenPath() string {
	if c.Provider == "tidal" {
		return filepath.Join(c.ConfigDir, "token-tidal.json")
	}
	return filepath.Join(c.ConfigDir, "token.json")
}

// DBPath is the SQLite database file. An existing pre-rename spotifytool.db is
// kept in place; only fresh data dirs get the new name.
func (c Config) DBPath() string {
	legacy := filepath.Join(c.DataDir, "spotifytool.db")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return filepath.Join(c.DataDir, "musictool.db")
}

// ProfilePath is the taste-profile.md location, overridable via
// MUSICTOOL_PROFILE (legacy SPOTIFYTOOL_PROFILE) for the shared-volume
// deployment.
func (c Config) ProfilePath() string {
	if p := Env("PROFILE"); p != "" {
		return p
	}
	return filepath.Join(c.DataDir, "taste-profile.md")
}

// DigestDir is where weekly batch digests are written.
func (c Config) DigestDir() string {
	if p := Env("DIGEST_DIR"); p != "" {
		return p
	}
	return filepath.Join(c.DataDir, "digests")
}

// Load resolves configuration from the environment. It never errors on missing
// secrets — subcommands validate what they actually need (e.g. serve requires a
// usable token, auth requires only the client id).
func Load() Config {
	prov := strings.ToLower(strings.TrimSpace(os.Getenv("MUSIC_PROVIDER")))
	if prov == "" {
		prov = "spotify"
	}
	country := strings.ToUpper(strings.TrimSpace(os.Getenv("TIDAL_COUNTRY")))
	if country == "" {
		country = "US"
	}
	c := Config{
		Provider:          prov,
		ClientID:          strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_ID")),
		RefreshToken:      strings.TrimSpace(os.Getenv("SPOTIFY_REFRESH_TOKEN")),
		LastFMKey:         strings.TrimSpace(os.Getenv("LASTFM_API_KEY")),
		TidalClientID:     strings.TrimSpace(os.Getenv("TIDAL_CLIENT_ID")),
		TidalRefreshToken: strings.TrimSpace(os.Getenv("TIDAL_REFRESH_TOKEN")),
		TidalCountry:      country,
		TerminalURL:       Env("TERMINAL_URL"),
		TerminalURLHTTP:   Env("TERMINAL_URL_HTTP"),
		TriggerURL:        Env("TRIGGER_URL"),
		ConfigDir:         configDir(),
		DataDir:           dataDir(),
	}
	return c
}

// legacyOrNew keeps an existing pre-rename "spotifytool" directory in use and
// only names fresh directories "musictool".
func legacyOrNew(base string) string {
	legacy := filepath.Join(base, "spotifytool")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return filepath.Join(base, "musictool")
}

func configDir() string {
	if d := Env("CONFIG_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return legacyOrNew(xdg)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".musictool")
	}
	return legacyOrNew(filepath.Join(home, ".config"))
}

func dataDir() string {
	if d := Env("DATA_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return legacyOrNew(xdg)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".musictool-data")
	}
	return legacyOrNew(filepath.Join(home, ".local", "share"))
}

// EnsureDirs creates the config and data directories (0700) if absent.
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.ConfigDir, c.DataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
