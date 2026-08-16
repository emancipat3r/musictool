// Command musictool is the self-hosted junction between Claude's curation and
// the streaming provider's catalog (Spotify or TIDAL): PKCE auth, library sync
// into SQLite, the deterministic resolver, exact playlist builds with
// read-back verification, and an MCP + dashboard server.
//
// CLI convention (absolute in serve mode): data (JSON) goes to stdout, all
// progress/diagnostics go to stderr. Exit codes: 0 ok / 1 auth / 2 API /
// 3 partial.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/emancipat3r/musictool/internal/apperr"
	"github.com/emancipat3r/musictool/internal/auth"
	"github.com/emancipat3r/musictool/internal/config"
	"github.com/emancipat3r/musictool/internal/logx"
	"github.com/emancipat3r/musictool/internal/model"
	"github.com/emancipat3r/musictool/internal/profile"
	"github.com/emancipat3r/musictool/internal/service"
)

// version is overridable via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "auth":
		err = cmdAuth(ctx, args)
	case "sync":
		err = cmdSync(ctx, args)
	case "serve":
		err = cmdServe(ctx, args)
	case "build":
		err = cmdBuild(ctx, args)
	case "stats":
		err = cmdStats(ctx, args)
	case "signals":
		err = cmdSignals(ctx, args)
	case "profile":
		err = cmdProfile(ctx, args)
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		logx.Errorf("unknown command %q", cmd)
		usage()
		return 2
	}

	if err != nil {
		logx.Errorf("%v", err)
		return apperr.Code(err)
	}
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `musictool — self-hosted AI music curation junction

usage: musictool <command> [flags]

commands:
  auth       one-time interactive provider authorization (PKCE + loopback/OOB)
  sync       refresh liked songs, playlists, plays, Keepers into SQLite
  serve      run the MCP server (Streamable HTTP) + read-only dashboard
  build      resolve a tracklist and create an exact, verified playlist
  stats      print distilled library stats (JSON)
  signals    print distilled recent feedback since last batch (JSON)
  profile    show | path | edit the taste profile
  version    print version

data goes to stdout; logs to stderr. exit: 0 ok / 1 auth / 2 API / 3 partial
`)
}

// envc returns the MUSICTOOL_/legacy-SPOTIFYTOOL_ env value for suffix, or def
// if neither is set.
func envc(suffix, def string) string {
	if v := config.Env(suffix); v != "" {
		return v
	}
	return def
}

// emit writes a value as pretty JSON to stdout (the data channel).
func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdAuth(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	noListener := fs.Bool("no-listener", false, "OOB flow: print URL, paste the redirected code back")
	noBrowser := fs.Bool("no-browser", false, "do not try to open a browser")
	printRefresh := fs.Bool("print-refresh-token", false, "print the refresh token to stdout for headless bootstrap")
	verbose := fs.Bool("v", false, "verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Verbose = *verbose
	cfg := config.Load()
	ep := authEndpoints(cfg)
	if ep.ClientID == "" {
		return apperr.Auth(fmt.Errorf("%s is not set", ep.ClientIDEnv))
	}
	reader := bufio.NewReader(os.Stdin)
	readCode := func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		line, err := reader.ReadString('\n')
		return strings.TrimSpace(line), err
	}
	tok, err := auth.Run(ctx, ep, auth.Options{
		NoListener: *noListener,
		NoBrowser:  *noBrowser,
	}, readCode)
	if err != nil {
		return err
	}
	if *printRefresh {
		// The only stdout write for auth: the app-scoped refresh token, so it
		// can be injected as SPOTIFY_REFRESH_TOKEN / TIDAL_REFRESH_TOKEN into
		// container 2.
		fmt.Println(tok.RefreshToken)
	}
	return nil
}

// authEndpoints maps the configured provider to its OAuth2 endpoints. Both
// providers speak Authorization Code + PKCE with the same loopback redirect.
func authEndpoints(cfg config.Config) auth.Endpoints {
	if cfg.Provider == "tidal" {
		return auth.Endpoints{
			Provider:     "tidal",
			ClientID:     cfg.TidalClientID,
			ClientIDEnv:  "TIDAL_CLIENT_ID",
			AuthorizeURL: config.TidalAuthorizeURL,
			TokenURL:     config.TidalTokenURL,
			Scopes:       config.TidalScopes,
			TokenPath:    cfg.TokenPath(),
		}
	}
	return auth.Endpoints{
		Provider:     "spotify",
		ClientID:     cfg.ClientID,
		ClientIDEnv:  "SPOTIFY_CLIENT_ID",
		AuthorizeURL: config.AuthorizeURL,
		TokenURL:     config.TokenURL,
		Scopes:       config.Scopes,
		TokenPath:    cfg.TokenPath(),
	}
}

func cmdSync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	full := fs.Bool("full", false, "deep-sync every playlist's tracks")
	verbose := fs.Bool("v", false, "verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Verbose = *verbose
	svc, err := service.New(config.Load())
	if err != nil {
		return err
	}
	defer svc.Close()
	res, err := svc.Sync(ctx, *full)
	if err != nil {
		return err
	}
	return emit(res)
}

func cmdStats(ctx context.Context, args []string) error {
	svc, err := service.New(config.Load())
	if err != nil {
		return err
	}
	defer svc.Close()
	st, err := svc.DB.Stats(ctx)
	if err != nil {
		return err
	}
	return emit(st)
}

func cmdSignals(ctx context.Context, args []string) error {
	svc, err := service.New(config.Load())
	if err != nil {
		return err
	}
	defer svc.Close()
	sig, err := svc.DB.Signals(ctx)
	if err != nil {
		return err
	}
	return emit(sig)
}

func cmdBuild(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	name := fs.String("name", "", "playlist name (required)")
	desc := fs.String("desc", "", "playlist description")
	public := fs.Bool("public", false, "make the playlist public")
	file := fs.String("file", "", "JSON file of picks [{artist,title,album?}]; '-' for stdin")
	label := fs.String("batch-label", "", "record as a discovery batch under this label")
	verbose := fs.Bool("v", false, "verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Verbose = *verbose
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	picks, err := readPicks(*file)
	if err != nil {
		return err
	}
	svc, err := service.New(config.Load())
	if err != nil {
		return err
	}
	defer svc.Close()
	res, err := svc.BuildExact(ctx, service.BuildOptions{
		Name:             *name,
		Description:      *desc,
		Public:           *public,
		Queries:          picks,
		RecordBatchLabel: *label,
	})
	if err != nil {
		return err
	}
	if emitErr := emit(res); emitErr != nil {
		return emitErr
	}
	// A build that didn't fully land is a partial success (exit 3) so
	// cron/agents can notice: read-back mismatch, unresolvable picks, or
	// ambiguous picks awaiting a caller decision.
	if res != nil {
		switch {
		case res.ReadbackMatches != nil && !*res.ReadbackMatches:
			return apperr.Partial(fmt.Errorf("read-back did not match resolved URIs"))
		case len(res.NotFound) > 0 || len(res.Ambiguous) > 0:
			return apperr.Partial(fmt.Errorf("%d of %d picks did not land (%d not found, %d ambiguous)",
				res.Requested-res.Added, res.Requested, len(res.NotFound), len(res.Ambiguous)))
		}
	}
	return nil
}

func readPicks(file string) ([]model.TrackQuery, error) {
	var data []byte
	var err error
	switch file {
	case "", "-":
		data, err = io.ReadAll(os.Stdin)
	default:
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return nil, fmt.Errorf("read picks: %w", err)
	}
	var picks []model.TrackQuery
	if err := json.Unmarshal(data, &picks); err != nil {
		return nil, fmt.Errorf("parse picks JSON (expect [{artist,title,album?}]): %w", err)
	}
	if len(picks) == 0 {
		return nil, fmt.Errorf("no picks provided")
	}
	return picks, nil
}

func cmdProfile(ctx context.Context, args []string) error {
	cfg := config.Load()
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path":
		fmt.Println(cfg.ProfilePath())
		return nil
	case "show":
		content, err := profile.Read(cfg.ProfilePath())
		if err != nil {
			return err
		}
		fmt.Print(content)
		return nil
	case "edit":
		return fmt.Errorf("edit the file directly at %s or via the dashboard /profile page", cfg.ProfilePath())
	default:
		return fmt.Errorf("usage: musictool profile [show|path|edit]")
	}
}
