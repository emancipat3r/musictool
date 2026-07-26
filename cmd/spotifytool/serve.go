package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
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
	tlsCert := fs.String("tls-cert", envOr("SPOTIFYTOOL_TLS_CERT", ""), "TLS cert for dashboard + terminal proxy (empty = plain HTTP)")
	tlsKey := fs.String("tls-key", envOr("SPOTIFYTOOL_TLS_KEY", ""), "TLS key for dashboard + terminal proxy")
	noDash := fs.Bool("no-dashboard", false, "do not serve the dashboard")
	noMCP := fs.Bool("no-mcp", false, "do not serve MCP")
	verbose := fs.Bool("v", false, "verbose")
	if err := fs.Parse(args); err != nil {
		return err
	}
	logx.Verbose = *verbose

	// TLS matters beyond hygiene: browsers only expose the clipboard API in
	// secure contexts, so the zellij web client's copy (Ctrl+Shift+C, OSC52)
	// silently breaks over plain HTTP on a LAN address.
	if *tlsCert != "" {
		if _, err := os.Stat(*tlsCert); err != nil {
			return fmt.Errorf("TLS cert configured but unreadable: %w", err)
		}
		if _, err := os.Stat(*tlsKey); err != nil {
			return fmt.Errorf("TLS key configured but unreadable: %w", err)
		}
	}

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
		// MCP stays plain HTTP: it lives on the compose network only and the
		// Claude Code client is configured for http://spotify:8080/mcp.
		m := mcp.NewServer("spotifytool", version, mcp.Tools(svc))
		srv := &http.Server{Addr: *mcpAddr, Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go serveHTTP(srv, "mcp", *mcpAddr+"/mcp", "", "")
	}
	// Listen telemetry: read-only currently-playing polling for skip and
	// repeat detection. Backs off gracefully if the token lacks the scope.
	go svc.StartListenPoller(ctx, 20*time.Second)

	if !*noDash {
		d := dashboard.New(svc, cfg)
		srv := &http.Server{Addr: *dashAddr, Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go serveHTTP(srv, "dashboard", *dashAddr, *tlsCert, *tlsKey)
		// Companion-app lane: plain HTTP twin of the dashboard. Android
		// WebView JS WebSockets do not consult the app's SSL error handler,
		// so self-signed TLS breaks the terminal inside the app; the app uses
		// this listener instead (no TLS anywhere in its path). Browsers keep
		// the TLS listener for clipboard (secure-context) support.
		if *tlsCert != "" {
			httpAddr := envOr("SPOTIFYTOOL_DASH_HTTP_ADDR", ":8085")
			if httpAddr != "off" {
				srv2 := &http.Server{Addr: httpAddr, Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
				servers = append(servers, srv2)
				go serveHTTP(srv2, "dashboard (app/http)", httpAddr, "", "")
			}
		}
	}
	if *zellijUpstream != "" {
		tokenPath := filepath.Join(cfg.DataDir, "zellij-web-token.txt")
		tp, err := dashboard.NewTermProxy(*zellijUpstream, tokenPath)
		if err != nil {
			return err
		}
		// Deep-link into the claude session; "/" never shows the picker.
		tp.SetSession(envOr("SPOTIFYTOOL_ZELLIJ_SESSION", "claude"))
		// No ReadHeaderTimeout here: long-lived terminal WebSockets.
		srv := &http.Server{Addr: *termAddr, Handler: tp}
		servers = append(servers, srv)
		go serveHTTP(srv, "terminal proxy", *termAddr, *tlsCert, *tlsKey)
		if *tlsCert != "" {
			httpAddr := envOr("SPOTIFYTOOL_TERM_HTTP_ADDR", ":8084")
			if httpAddr != "off" {
				srv2 := &http.Server{Addr: httpAddr, Handler: tp}
				servers = append(servers, srv2)
				go serveHTTP(srv2, "terminal proxy (app/http)", httpAddr, "", "")
			}
		}
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

func serveHTTP(srv *http.Server, label, shown, tlsCert, tlsKey string) {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logx.Errorf("%s listen %s: %v", label, srv.Addr, err)
		return
	}
	scheme := "http"
	if tlsCert != "" && tlsKey != "" {
		scheme = "https"
	}
	logx.Infof("%s listening on %s://%s", label, scheme, shown)
	if scheme == "https" {
		err = srv.ServeTLS(ln, tlsCert, tlsKey)
	} else {
		err = srv.Serve(ln)
	}
	if err != nil && err != http.ErrServerClosed {
		logx.Errorf("%s server: %v", label, err)
	}
}
