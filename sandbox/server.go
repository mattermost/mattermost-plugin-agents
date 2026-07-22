// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

// Logger is the subset of pluginapi.LogService the sandbox package needs.
type Logger interface {
	Info(message string, keyValuePairs ...any)
	Warn(message string, keyValuePairs ...any)
	Error(message string, keyValuePairs ...any)
}

// Server is the in-process HTTP server that serves the MCP Apps sandbox
// page on its own origin (shape follows metrics/server.go).
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// NewServer binds listenAddr immediately so port conflicts surface
// synchronously (P3: callers log-and-disable instead of failing plugin
// activation). The returned server serves exactly GET /sandbox.html
// (templated for hostOrigin, CSP from ?csp=) and 404s everything else.
func NewServer(listenAddr, hostOrigin string, logger Logger) (*Server, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("sandbox listener failed to bind %s: %w", listenAddr, err)
	}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandbox.html" {
			http.NotFound(w, r)
			return
		}
		ServePage(w, r, hostOrigin, logger)
	})

	return &Server{
		srv: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		ln: ln,
	}, nil
}

// Addr returns the actual bound address (resolves ":0" in tests).
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

// Run serves until Shutdown; returns http.ErrServerClosed on clean stop.
func (s *Server) Run() error {
	return s.srv.Serve(s.ln)
}

// Shutdown gracefully stops the listener (1s grace, then close).
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		_ = s.srv.Close()
		return err
	}
	return nil
}

// ListenSpecFromConfig resolves whether this node should run the sandbox
// listener and with what parameters. enabled is false when apps are off or
// no external SandboxURL is configured (the insecure same-origin mode
// serves from the plugin route and needs no listener, P10).
func ListenSpecFromConfig(apps config.MCPAppsConfig, siteURL string) (addr, hostOrigin string, enabled bool, err error) {
	enabled = apps.Enabled && strings.TrimSpace(apps.SandboxURL) != ""
	if !enabled {
		return "", "", false, nil
	}

	addr = strings.TrimSpace(apps.SandboxListenAddress)
	if addr == "" {
		addr = config.DefaultMCPAppsSandboxListenAddress
	}

	hostOrigin, err = originFromSiteURL(siteURL)
	if err != nil {
		return "", "", false, err
	}
	return addr, hostOrigin, true, nil
}

func originFromSiteURL(siteURL string) (string, error) {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return "", fmt.Errorf("site URL is empty")
	}
	parsed, err := url.Parse(siteURL)
	if err != nil {
		return "", fmt.Errorf("invalid site URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("site URL must include scheme and host")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
