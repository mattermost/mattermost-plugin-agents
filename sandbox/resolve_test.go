// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

func TestOriginFromURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "https no port", raw: "https://mm.example.com/path", want: "https://mm.example.com"},
		{name: "https default port stripped", raw: "https://mm.example.com:443/x", want: "https://mm.example.com"},
		{name: "http default port stripped", raw: "http://mm.example.com:80", want: "http://mm.example.com"},
		{name: "non-default port kept", raw: "https://mm.example.com:8443/apps", want: "https://mm.example.com:8443"},
		{name: "userinfo rejected", raw: "https://user:pass@mm.example.com", wantErr: true},
		{name: "ftp rejected", raw: "ftp://mm.example.com", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OriginFromURL(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestValidateAppsConfigOriginConstraint(t *testing.T) {
	tests := []struct {
		name    string
		apps    config.MCPAppsConfig
		siteURL string
		wantErr bool
	}{
		{
			name:    "external different origin ok",
			apps:    config.MCPAppsConfig{SandboxURL: "https://apps.example.com"},
			siteURL: "https://mm.example.com",
		},
		{
			name:    "same origin without opt-in rejected",
			apps:    config.MCPAppsConfig{SandboxURL: "https://mm.example.com:443/sandbox-proxy"},
			siteURL: "https://mm.example.com",
			wantErr: true,
		},
		{
			name: "same origin with opt-in still rejected",
			apps: config.MCPAppsConfig{
				SandboxURL:                     "https://mm.example.com/proxy",
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "https://mm.example.com",
			wantErr: true,
		},
		{
			name:    "cleared URL with opt-in ok",
			apps:    config.MCPAppsConfig{AllowInsecureSameOriginSandbox: true},
			siteURL: "https://mm.example.com",
		},
		{
			name:    "userinfo rejected",
			apps:    config.MCPAppsConfig{SandboxURL: "https://u:p@apps.example.com"},
			siteURL: "https://mm.example.com",
			wantErr: true,
		},
		{
			name:    "port 0 rejected",
			apps:    config.MCPAppsConfig{SandboxListenAddress: ":0"},
			siteURL: "https://mm.example.com",
			wantErr: true,
		},
		{
			name:    "port too high rejected",
			apps:    config.MCPAppsConfig{SandboxListenAddress: ":99999"},
			siteURL: "https://mm.example.com",
			wantErr: true,
		},
		{
			name:    "port 1 ok",
			apps:    config.MCPAppsConfig{SandboxListenAddress: "127.0.0.1:1"},
			siteURL: "https://mm.example.com",
		},
		{
			name:    "port 65535 ok",
			apps:    config.MCPAppsConfig{SandboxListenAddress: ":65535"},
			siteURL: "https://mm.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAppsConfig(tt.apps, tt.siteURL)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		apps    config.MCPAppsConfig
		siteURL string
		want    Resolved
	}{
		{
			name: "disabled",
			apps: config.MCPAppsConfig{Enabled: false, SandboxURL: "https://x"},
			want: Resolved{Mode: ModeOff, DisabledReason: DisabledReasonAppsDisabled},
		},
		{
			name:    "external",
			apps:    config.MCPAppsConfig{Enabled: true, SandboxURL: "https://apps.example.com"},
			siteURL: "https://mm.example.com/mattermost",
			want: Resolved{
				Mode:       ModeExternal,
				PageURL:    "https://apps.example.com/sandbox.html",
				HostOrigin: "https://mm.example.com",
				ListenAddr: ":8066",
			},
		},
		{
			name:    "same-origin sandboxURL without opt-in fails closed",
			apps:    config.MCPAppsConfig{Enabled: true, SandboxURL: "https://mm.example.com/apps"},
			siteURL: "https://mm.example.com",
			want:    Resolved{Mode: ModeOff, DisabledReason: DisabledReasonInvalidSandboxURL},
		},
		{
			name: "same-origin sandboxURL with opt-in fails closed (clear URL for fallback)",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				SandboxURL:                     "https://mm.example.com/proxy",
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "https://mm.example.com",
			want:    Resolved{Mode: ModeOff, DisabledReason: DisabledReasonInvalidSandboxURL},
		},
		{
			name:    "cleared URL with opt-in ⇒ ModeSameOrigin",
			apps:    config.MCPAppsConfig{Enabled: true, AllowInsecureSameOriginSandbox: true},
			siteURL: "https://mm.example.com",
			want: Resolved{
				Mode:       ModeSameOrigin,
				PageURL:    "https://mm.example.com/plugins/mattermost-ai/mcp/apps/sandbox",
				HostOrigin: "https://mm.example.com",
			},
		},
		{
			name:    "insecure same-origin with subpath SiteURL",
			apps:    config.MCPAppsConfig{Enabled: true, AllowInsecureSameOriginSandbox: true},
			siteURL: "https://example.com/mattermost",
			want: Resolved{
				Mode:       ModeSameOrigin,
				PageURL:    "https://example.com/mattermost/plugins/mattermost-ai/mcp/apps/sandbox",
				HostOrigin: "https://example.com",
			},
		},
		{
			name:    "enabled nothing configured",
			apps:    config.MCPAppsConfig{Enabled: true},
			siteURL: "https://mm.example.com",
			want:    Resolved{Mode: ModeOff, DisabledReason: DisabledReasonNoSandboxOrigin},
		},
		{
			name: "external wins over insecure",
			apps: config.MCPAppsConfig{
				Enabled:                        true,
				SandboxURL:                     "https://apps.example.com/",
				AllowInsecureSameOriginSandbox: true,
			},
			siteURL: "http://localhost:8065",
			want: Resolved{
				Mode:       ModeExternal,
				PageURL:    "https://apps.example.com/sandbox.html",
				HostOrigin: "http://localhost:8065",
				ListenAddr: ":8066",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Resolve(tt.apps, tt.siteURL))
		})
	}
}
