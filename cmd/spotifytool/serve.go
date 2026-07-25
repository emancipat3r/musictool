package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/emancipat3r/spotifytool/internal/config"
	"github.com/emancipat3r/spotifytool/internal/dashboard"
	"github.com/emancipat3r/spotifytool/internal/logx"
	"github.com/emancipat3r/spotifytool/internal/mcp"
	"github.com/emancipat3r/spotifytool/internal/profile"
	"github.com/emancipat3r/spotifytool/internal/service"
)

// cmdServe runs the MCP server (Streamable HTTP) and the read-only dashboard,
// sharing one store handle (single writer). Both bind to the given addresses;
// the network perimeter (compose network + Tailscale) is enforced outside this
// process — nothing here reaches the public internet.
//
// Absolute rule in serve: no writes to stdout. All diagnostics go to stderr.
func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	mcpAddr := fs.String("mcp-addr", envOr("SPOTIFYTOOL_MCP_ADDR", ":8080"), "MCP listen address")
	dashAddr := fs.String("dash-addr", envOr("SPOTIFYTOOL_DASH_ADDR", ":8081"), "dashboard listen address")
	termAddr := fs.String("term-addr", envOr("SPOTIFYTOOL_TERMPROXY_ADDR", ":8083"), "terminal proxy listen address")
	zellijUpstream := fs.String("zellij-upstream", envOr("SPOTIFYTOOL_ZELLIJ_UPSTREAM", ""),
		"zellij web URL to auth-proxy (e.g. https://sandbox:8082); empty disables the terminal proxy")
	noDash := fs.Bool("no-dashboard", false, "do not serve the dashboard")
	noMCP := fs.Bool("no-mcp", false, "do not serve MCP")
	verbose := fs.Bool("v", false, "verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Verbose = *verbose

	cfg := config.Load()
	svc, err := service.New(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	// Ensure the taste profile exists so sessions/batches can load it.
	if _, err := profile.Read(cfg.ProfilePath()); err != nil {
		logx.Errorf("taste profile init: %v", err)
	}

	var servers []*http.Server

	if !*noMCP {
		m := mcp.NewServer("spotifytool", version, mcp.Tools(svc))
		srv := &http.Server{Addr: *mcpAddr, Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go serveHTTP(srv, "mcp", *mcpAddr+"/mcp")
	}
	if !*noDash {
		d := dashboard.New(svc.DB, cfg)
		srv := &http.Server{Addr: *dashAddr, Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go serveHTTP(srv, "dashboard", *dashAddr)
	}
	if *zellijUpstream != "" {
		tokenPath := filepath.Join(cfg.DataDir, "zellij-web-token.txt")
		tp, err := dashboard.NewTermProxy(*zellijUpstream, tokenPath)
		if err != nil {
			return err
		}
		// No ReadHeaderTimeout here: long-lived terminal WebSockets.
		srv := &http.Server{Addr: *termAddr, Handler: tp}
		servers = append(servers, srv)
		go serveHTTP(srv, "terminal proxy", *termAddr)
	}
	if len(servers) == 0 {
		return errors.New("nothing to serve: both --no-mcp and --no-dashboard set")
	}

	<-ctx.Done()
	logx.Infof("shutting down…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shutCtx)
	}
	return nil
}

func serveHTTP(srv *http.Server, label, shown string) {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logx.Errorf("%s listen %s: %v", label, srv.Addr, err)
		return
	}
	logx.Infof("%s listening on http://%s", label, shown)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logx.Errorf("%s server: %v", label, err)
	}
}
