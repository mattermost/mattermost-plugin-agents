// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

func TestServerLifecycle(t *testing.T) {
	server, err := NewServer(":0", testHostOrigin, noopLogger{})
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run()
	}()

	// Wait briefly for Serve to start accepting.
	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + server.Addr() + "/sandbox.html")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "ALLOWED_HOST_ORIGIN")

	require.NoError(t, server.Shutdown())
	select {
	case runErr := <-errCh:
		require.ErrorIs(t, runErr, http.ErrServerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}

	_, err = http.Get("http://" + server.Addr() + "/sandbox.html")
	require.Error(t, err)
}

func TestServerRoutes(t *testing.T) {
	server, err := NewServer(":0", testHostOrigin, noopLogger{})
	require.NoError(t, err)
	defer func() { _ = server.Shutdown() }()

	go func() { _ = server.Run() }()

	base := "http://" + server.Addr()
	// Wait for ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, getErr := http.Get(base + "/sandbox.html")
		if getErr == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "GET sandbox.html", method: http.MethodGet, path: "/sandbox.html", wantStatus: http.StatusOK},
		{name: "GET /", method: http.MethodGet, path: "/", wantStatus: http.StatusNotFound},
		{name: "GET sandbox.html/extra", method: http.MethodGet, path: "/sandbox.html/extra", wantStatus: http.StatusNotFound},
		{name: "GET other.html", method: http.MethodGet, path: "/other.html", wantStatus: http.StatusNotFound},
		{name: "POST sandbox.html", method: http.MethodPost, path: "/sandbox.html", wantStatus: http.StatusNotFound},
		{name: "HEAD sandbox.html", method: http.MethodHead, path: "/sandbox.html", wantStatus: http.StatusNotFound},
	}

	client := &http.Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, base+tt.path, nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestNewServerPortConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	_, err = NewServer(ln.Addr().String(), testHostOrigin, noopLogger{})
	require.Error(t, err)
}

func TestListenSpecFromConfig(t *testing.T) {
	tests := []struct {
		name        string
		apps        config.MCPAppsConfig
		siteURL     string
		wantAddr    string
		wantOrigin  string
		wantEnabled bool
		wantErr     bool
	}{
		{
			name:        "disabled",
			apps:        config.MCPAppsConfig{Enabled: false, SandboxURL: "https://x"},
			siteURL:     "https://mm.example.com",
			wantEnabled: false,
		},
		{
			name:        "enabled, no URL (same-origin mode)",
			apps:        config.MCPAppsConfig{Enabled: true},
			siteURL:     "https://mm.example.com",
			wantEnabled: false,
		},
		{
			name: "enabled + URL, default addr",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://apps.example.com",
			},
			siteURL:     "https://mm.example.com",
			wantAddr:    ":8066",
			wantOrigin:  "https://mm.example.com",
			wantEnabled: true,
		},
		{
			name: "custom listen addr",
			apps: config.MCPAppsConfig{
				Enabled:              true,
				SandboxURL:           "https://apps.example.com",
				SandboxListenAddress: "127.0.0.1:9000",
			},
			siteURL:     "https://mm.example.com",
			wantAddr:    "127.0.0.1:9000",
			wantOrigin:  "https://mm.example.com",
			wantEnabled: true,
		},
		{
			name: "siteURL with subpath",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://apps.example.com",
			},
			siteURL:     "https://mm.example.com/mattermost",
			wantAddr:    ":8066",
			wantOrigin:  "https://mm.example.com",
			wantEnabled: true,
		},
		{
			name: "siteURL with port",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://apps.example.com",
			},
			siteURL:     "http://localhost:8065",
			wantAddr:    ":8066",
			wantOrigin:  "http://localhost:8065",
			wantEnabled: true,
		},
		{
			name: "empty siteURL",
			apps: config.MCPAppsConfig{
				Enabled:    true,
				SandboxURL: "https://apps.example.com",
			},
			siteURL: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, origin, enabled, err := ListenSpecFromConfig(tt.apps, tt.siteURL)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantEnabled, enabled)
			if !tt.wantEnabled {
				require.Empty(t, addr)
				require.Empty(t, origin)
				return
			}
			require.Equal(t, tt.wantAddr, addr)
			require.Equal(t, tt.wantOrigin, origin)
		})
	}
}
