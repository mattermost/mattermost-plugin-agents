// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPToolConfigRetrievalDescriptionOverrideJSON(t *testing.T) {
	toolConfig := MCPToolConfig{
		Name:                         "get_issue",
		Policy:                       MCPToolPolicyAutoRunInDM,
		Enabled:                      true,
		RetrievalDescriptionOverride: "Find Jira issues by key or text",
	}

	data, err := json.Marshal(toolConfig)
	require.NoError(t, err)
	require.Contains(t, string(data), `"retrieval_description_override":"Find Jira issues by key or text"`)

	var decoded MCPToolConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, toolConfig, decoded)
}

func TestMCPToolConfigEmptyRetrievalDescriptionOverrideOmitted(t *testing.T) {
	data, err := json.Marshal(MCPToolConfig{
		Name:    "get_issue",
		Policy:  MCPToolPolicyAsk,
		Enabled: true,
	})
	require.NoError(t, err)
	require.NotContains(t, string(data), "retrieval_description_override")

	var decoded MCPToolConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"get_issue","policy":"ask","enabled":true}`), &decoded))
	require.Empty(t, decoded.RetrievalDescriptionOverride)
}

// headersFromPairs builds a header map from name/value pairs. Padded names are
// passed as arguments because a map literal cannot hold keys that only differ by
// whitespace without tripping the linter.
func headersFromPairs(pairs ...string) map[string]string {
	headers := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		headers[pairs[i]] = pairs[i+1]
	}
	return headers
}

// HasServiceAccountAuth must be true exactly when EffectiveServiceAccountHeaders is non-empty.
func TestMCPServerConfigServiceAccountHeaderFiltering(t *testing.T) {
	tests := []struct {
		name         string
		serverConfig *MCPServerConfig
		wantHeaders  map[string]string
	}{
		{
			name:         "only blank entries",
			serverConfig: &MCPServerConfig{Enabled: true, ServiceAccountHeaders: map[string]string{"": "Bearer pat", "Authorization": "   "}},
		},
		{
			name: "blank entries are dropped and kept entries are trimmed",
			serverConfig: &MCPServerConfig{
				Enabled: true,
				ServiceAccountHeaders: map[string]string{
					"":              "",
					"   ":           "Bearer pat",
					"X-Blank-Value": "  ",
					"Authorization": "Bearer pat",
					"  X-Api-Key ":  " secret-token\n",
				},
			},
			wantHeaders: map[string]string{"Authorization": "Bearer pat", "X-Api-Key": "secret-token"},
		},
		{
			name: "names colliding after trim are all dropped",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: headersFromPairs("Authorization", "Bearer first", " Authorization ", "Bearer second"),
			},
		},
		{
			name: "colliding names do not drop distinct entries",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: headersFromPairs("Authorization", "Bearer first", "Authorization ", "Bearer second", "X-Api-Key", "secret-token"),
			},
			wantHeaders: map[string]string{"X-Api-Key": "secret-token"},
		},
		{
			name: "names colliding only by case are all dropped",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: map[string]string{"Authorization": "Bearer first", "authorization": "Bearer second"},
			},
		},
		{
			name: "case-colliding names do not drop distinct entries",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: map[string]string{"Authorization": "Bearer first", "authorization": "Bearer second", "X-Api-Key": "secret-token"},
			},
			wantHeaders: map[string]string{"X-Api-Key": "secret-token"},
		},
		{
			name: "names are stored in canonical form",
			serverConfig: &MCPServerConfig{
				Enabled:               true,
				ServiceAccountHeaders: map[string]string{"x-api-key": "secret-token"},
			},
			wantHeaders: map[string]string{"X-Api-Key": "secret-token"},
		},
		{
			name: "base headers do not count as service account auth",
			serverConfig: &MCPServerConfig{
				Enabled: true,
				Headers: map[string]string{"Authorization": "Bearer shared"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantHeaders, tt.serverConfig.EffectiveServiceAccountHeaders())
			require.Equal(t, len(tt.wantHeaders) > 0, tt.serverConfig.HasServiceAccountAuth())
		})
	}
}

func TestConfigClonePreservesServiceAccountHeaders(t *testing.T) {
	original := &Config{
		MCP: MCPConfig{
			Servers: []MCPServerConfig{
				{
					Name:                  "Jira",
					Enabled:               true,
					BaseURL:               "https://jira.example.com",
					Headers:               map[string]string{"X-Trace": "on"},
					ServiceAccountHeaders: map[string]string{"Authorization": "Bearer service-pat"},
				},
			},
		},
	}

	clone := original.Clone()
	require.Equal(t, original.MCP.Servers, clone.MCP.Servers)

	clone.MCP.Servers[0].ServiceAccountHeaders["Authorization"] = "Bearer tampered"
	require.Equal(t, "Bearer service-pat", original.MCP.Servers[0].ServiceAccountHeaders["Authorization"])
}

func TestMCPConfigServerIDMaps(t *testing.T) {
	tests := []struct {
		name             string
		cfg              MCPConfig
		expectByOrigin   map[string]string
		expectByServerID map[string]string
	}{
		{
			name:             "empty servers",
			cfg:              MCPConfig{},
			expectByOrigin:   map[string]string{},
			expectByServerID: map[string]string{},
		},
		{
			name: "servers without IDs omitted",
			cfg: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "id-one", Name: "one", BaseURL: "https://one.example.com"},
					{Name: "no-id", BaseURL: "https://two.example.com"},
				},
			},
			expectByOrigin:   map[string]string{"https://one.example.com": "id-one"},
			expectByServerID: map[string]string{"id-one": "https://one.example.com"},
		},
		{
			name: "duplicate BaseURL last entry wins",
			cfg: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "id-first", Name: "first", BaseURL: "https://dup.example.com"},
					{ID: "id-last", Name: "last", BaseURL: "https://dup.example.com"},
				},
			},
			expectByOrigin: map[string]string{"https://dup.example.com": "id-last"},
			expectByServerID: map[string]string{
				"id-first": "https://dup.example.com",
				"id-last":  "https://dup.example.com",
			},
		},
		{
			name: "disabled server included",
			cfg: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "id-disabled", Name: "off", Enabled: false, BaseURL: "https://off.example.com"},
				},
			},
			expectByOrigin:   map[string]string{"https://off.example.com": "id-disabled"},
			expectByServerID: map[string]string{"id-disabled": "https://off.example.com"},
		},
		{
			name: "embedded and plugin origins included",
			cfg: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "id-remote", Name: "remote", BaseURL: "https://remote.example.com"},
				},
				EmbeddedServer: MCPEmbeddedServerConfig{ID: "id-embedded", Enabled: true},
				PluginServers: []PluginServerConfig{
					{ID: "id-plugin", PluginID: "com.example.mcp", Name: "Plugin", Path: "/mcp"},
					{PluginID: "com.example.noid", Name: "No ID", Path: "/mcp"},
				},
			},
			expectByOrigin: map[string]string{
				"https://remote.example.com":          "id-remote",
				MCPEmbeddedServerOrigin:               "id-embedded",
				PluginServerOrigin("com.example.mcp"): "id-plugin",
			},
			expectByServerID: map[string]string{
				"id-remote":   "https://remote.example.com",
				"id-embedded": MCPEmbeddedServerOrigin,
				"id-plugin":   PluginServerOrigin("com.example.mcp"),
			},
		},
		{
			name: "ID-less embedded omitted",
			cfg: MCPConfig{
				EmbeddedServer: MCPEmbeddedServerConfig{Enabled: true},
			},
			expectByOrigin:   map[string]string{},
			expectByServerID: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expectByOrigin, tt.cfg.ServerIDByOrigin())
			require.Equal(t, tt.expectByServerID, tt.cfg.OriginByServerID())
		})
	}
}

func TestReconcileEmbeddedMCPServerID(t *testing.T) {
	tests := []struct {
		name      string
		next      MCPEmbeddedServerConfig
		prev      MCPEmbeddedServerConfig
		expectID  string
		expectErr bool
	}{
		{
			name:     "empty incoming keeps prev ID",
			next:     MCPEmbeddedServerConfig{Enabled: true},
			prev:     MCPEmbeddedServerConfig{ID: "embedded-prev", Enabled: true},
			expectID: "embedded-prev",
		},
		{
			name:     "matching IDs kept",
			next:     MCPEmbeddedServerConfig{ID: "embedded-prev", Enabled: false},
			prev:     MCPEmbeddedServerConfig{ID: "embedded-prev", Enabled: true},
			expectID: "embedded-prev",
		},
		{
			name:      "conflicting IDs rejected",
			next:      MCPEmbeddedServerConfig{ID: "incoming-id", Enabled: true},
			prev:      MCPEmbeddedServerConfig{ID: "embedded-prev", Enabled: true},
			expectErr: true,
		},
		{
			name:     "caller-chosen ID on previously ID-less kept",
			next:     MCPEmbeddedServerConfig{ID: "seeded-id", Enabled: true},
			prev:     MCPEmbeddedServerConfig{Enabled: true},
			expectID: "seeded-id",
		},
		{
			name:     "both empty stays ID-less",
			next:     MCPEmbeddedServerConfig{Enabled: true},
			prev:     MCPEmbeddedServerConfig{},
			expectID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReconcileEmbeddedMCPServerID(tt.next, tt.prev)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrMCPServerIDConflict)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectID, result.ID)
		})
	}
}

func TestReconcileMCPConfigIDs(t *testing.T) {
	const sharedID = "sharedidsharedidsharedidsha"

	prevPlugins := []PluginServerConfig{
		{ID: "plugin-a", PluginID: "com.example.a", Name: "A", Path: "/mcp", Enabled: true},
		{ID: "plugin-b", PluginID: "com.example.b", Name: "B", Path: "/other", Enabled: false},
	}

	tests := []struct {
		name      string
		next      MCPConfig
		prev      MCPConfig
		expectErr bool
		validate  func(t *testing.T, got MCPConfig)
	}{
		{
			name: "happy path keeps explicit remote ID, copies embedded/plugin from prev",
			next: MCPConfig{
				Servers:        []MCPServerConfig{{ID: "remote-id", Name: "Jira", BaseURL: "https://jira.example.com"}},
				EmbeddedServer: MCPEmbeddedServerConfig{Enabled: true},
				PluginServers:  []PluginServerConfig{{PluginID: "com.example.stale", Name: "Stale", Path: "/stale"}},
			},
			prev: MCPConfig{
				Servers:        []MCPServerConfig{{ID: "remote-id", Name: "Jira", BaseURL: "https://jira.example.com"}},
				EmbeddedServer: MCPEmbeddedServerConfig{ID: "embedded-id", Enabled: true},
				PluginServers:  []PluginServerConfig{{ID: "plugin-id", PluginID: "com.example.a", Name: "A", Path: "/mcp"}},
			},
			validate: func(t *testing.T, got MCPConfig) {
				require.Equal(t, "remote-id", got.Servers[0].ID)
				require.Equal(t, "embedded-id", got.EmbeddedServer.ID)
				require.Len(t, got.PluginServers, 1)
				require.Equal(t, "plugin-id", got.PluginServers[0].ID)
				require.Equal(t, "com.example.a", got.PluginServers[0].PluginID)
			},
		},
		{
			name: "nil PluginServers carries prev wholesale",
			next: MCPConfig{
				Servers:        []MCPServerConfig{{ID: "remote-id", Name: "Jira", BaseURL: "https://jira.example.com"}},
				EmbeddedServer: MCPEmbeddedServerConfig{ID: "embedded-id", Enabled: true},
				PluginServers:  nil,
			},
			prev: MCPConfig{PluginServers: prevPlugins},
			validate: func(t *testing.T, got MCPConfig) {
				require.Equal(t, prevPlugins, got.PluginServers)
			},
		},
		{
			name: "empty PluginServers carries prev wholesale",
			next: MCPConfig{
				PluginServers: []PluginServerConfig{},
			},
			prev: MCPConfig{
				PluginServers: []PluginServerConfig{
					{ID: "plugin-a", PluginID: "com.example.a", Name: "A", Path: "/mcp"},
				},
			},
			validate: func(t *testing.T, got MCPConfig) {
				require.Len(t, got.PluginServers, 1)
				require.Equal(t, "plugin-a", got.PluginServers[0].ID)
			},
		},
		{
			name: "non-empty stale PluginServers in next ignored",
			next: MCPConfig{
				PluginServers: []PluginServerConfig{
					{ID: "attacker-id", PluginID: "com.example.a", Name: "Hacked", Path: "/evil", Enabled: false},
				},
			},
			prev: MCPConfig{PluginServers: prevPlugins},
			validate: func(t *testing.T, got MCPConfig) {
				require.Equal(t, prevPlugins, got.PluginServers)
			},
		},
		{
			name: "remote and embedded share ID",
			next: MCPConfig{
				Servers:        []MCPServerConfig{{ID: sharedID, Name: "Remote", BaseURL: "https://r.example.com"}},
				EmbeddedServer: MCPEmbeddedServerConfig{ID: sharedID, Enabled: true},
			},
			expectErr: true,
		},
		{
			name: "remote and carried-forward plugin share ID",
			next: MCPConfig{
				Servers: []MCPServerConfig{{ID: sharedID, Name: "Remote", BaseURL: "https://r.example.com"}},
			},
			prev: MCPConfig{
				PluginServers: []PluginServerConfig{{ID: sharedID, PluginID: "com.example.a", Name: "A", Path: "/mcp"}},
			},
			expectErr: true,
		},
		{
			name: "embedded and carried-forward plugin share ID",
			next: MCPConfig{
				EmbeddedServer: MCPEmbeddedServerConfig{ID: sharedID, Enabled: true},
			},
			prev: MCPConfig{
				PluginServers: []PluginServerConfig{{ID: sharedID, PluginID: "com.example.a", Name: "A", Path: "/mcp"}},
			},
			expectErr: true,
		},
		{
			name: "stale next plugin ID cannot free prev plugin ID for remote reuse",
			next: MCPConfig{
				Servers: []MCPServerConfig{{ID: sharedID, Name: "Remote", BaseURL: "https://r.example.com"}},
				PluginServers: []PluginServerConfig{
					{ID: "other-id", PluginID: "com.example.a", Name: "A", Path: "/mcp"},
				},
			},
			prev: MCPConfig{
				PluginServers: []PluginServerConfig{{ID: sharedID, PluginID: "com.example.a", Name: "A", Path: "/mcp"}},
			},
			expectErr: true,
		},
		{
			name: "duplicate incoming remote IDs rejected",
			next: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: sharedID, Name: "srv", BaseURL: "https://one.example.com"},
					{ID: sharedID, Name: "copy", BaseURL: "https://two.example.com"},
				},
			},
			expectErr: true,
		},
		{
			name: "unique next succeeds even when prev has duplicate stored remote IDs",
			next: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "unique-id", Name: "srv", BaseURL: "https://one.example.com"},
				},
			},
			prev: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: sharedID, Name: "srv", BaseURL: "https://one.example.com"},
					{ID: sharedID, Name: "copy", BaseURL: "https://two.example.com"},
				},
			},
			validate: func(t *testing.T, got MCPConfig) {
				require.Len(t, got.Servers, 1)
				require.Equal(t, "unique-id", got.Servers[0].ID)
			},
		},
		{
			name: "ID-less remotes stay empty for the caller to mint",
			next: MCPConfig{
				Servers: []MCPServerConfig{
					{Name: "srv", BaseURL: "https://one.example.com"},
					{Name: "brand-new", BaseURL: "https://new.example.com"},
				},
			},
			prev: MCPConfig{
				Servers: []MCPServerConfig{
					{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
				},
			},
			validate: func(t *testing.T, got MCPConfig) {
				require.Len(t, got.Servers, 2)
				require.Empty(t, got.Servers[0].ID)
				require.Empty(t, got.Servers[1].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReconcileMCPConfigIDs(tt.next, tt.prev)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrMCPServerIDConflict)
				return
			}
			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}

func TestServerConfigGetToolPolicyIgnoresRetrievalOverride(t *testing.T) {
	serverConfig := &MCPServerConfig{
		Enabled: true,
		ToolConfigs: []MCPToolConfig{
			{
				Name:                         "get_issue",
				Policy:                       MCPToolPolicyAutoRunEverywhere,
				Enabled:                      true,
				RetrievalDescriptionOverride: strings.Repeat("override ", 4),
			},
		},
	}

	policy, enabled := serverConfig.GetToolPolicy("get_issue")
	require.Equal(t, MCPToolPolicyAutoRunEverywhere, policy)
	require.True(t, enabled)
}
