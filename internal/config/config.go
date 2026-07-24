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
}

// Config is the resolved runtime configuration. Secrets (client id, refresh
// token, last.fm key) live only in memory here, never on stdout.
type Config struct {
	ClientID     string // SPOTIFY_CLIENT_ID (public, required)
	RefreshToken string // SPOTIFY_REFRESH_TOKEN (optional headless bootstrap)
	LastFMKey    string // LASTFM_API_KEY (optional, discovery seeds)

	ConfigDir string // holds token.json
	DataDir   string // holds the SQLite db, taste profile, digests
}

// TokenPath is the 0600 token cache location.
func (c Config) TokenPath() string { return filepath.Join(c.ConfigDir, "token.json") }

// DBPath is the SQLite database file.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "spotifytool.db") }

// ProfilePath is the taste-profile.md location, overridable via
// SPOTIFYTOOL_PROFILE for the shared-volume deployment.
func (c Config) ProfilePath() string {
	if p := os.Getenv("SPOTIFYTOOL_PROFILE"); p != "" {
		return p
	}
	return filepath.Join(c.DataDir, "taste-profile.md")
}

// DigestDir is where weekly batch digests are written.
func (c Config) DigestDir() string {
	if p := os.Getenv("SPOTIFYTOOL_DIGEST_DIR"); p != "" {
		return p
	}
	return filepath.Join(c.DataDir, "digests")
}

// Load resolves configuration from the environment. It never errors on missing
// secrets — subcommands validate what they actually need (e.g. serve requires a
// usable token, auth requires only the client id).
func Load() Config {
	c := Config{
		ClientID:     strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_ID")),
		RefreshToken: strings.TrimSpace(os.Getenv("SPOTIFY_REFRESH_TOKEN")),
		LastFMKey:    strings.TrimSpace(os.Getenv("LASTFM_API_KEY")),
		ConfigDir:    configDir(),
		DataDir:      dataDir(),
	}
	return c
}

func configDir() string {
	if d := os.Getenv("SPOTIFYTOOL_CONFIG_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "spotifytool")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".spotifytool")
	}
	return filepath.Join(home, ".config", "spotifytool")
}

func dataDir() string {
	if d := os.Getenv("SPOTIFYTOOL_DATA_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "spotifytool")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".spotifytool-data")
	}
	return filepath.Join(home, ".local", "share", "spotifytool")
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
