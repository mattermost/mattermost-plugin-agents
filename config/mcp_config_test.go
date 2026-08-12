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

func TestReconcileMCPServerIDs(t *testing.T) {
	tests := []struct {
		name      string
		next      []MCPServerConfig
		prev      []MCPServerConfig
		expectIDs []string
		expectErr bool
	}{
		{
			name: "round-trip payload keeps existing ID",
			next: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "match by name when URL edited",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://new-url.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://old-url.example.com"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "match by BaseURL when renamed",
			next: []MCPServerConfig{
				{Name: "renamed", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "each prev entry consumed at most once",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
				{Name: "other", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"prev-id", ""},
		},
		{
			name: "add flow: genuinely new entry stays ID-less for the caller to mint",
			next: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://old.example.com"},
				{Name: "brand-new", BaseURL: "https://new.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://old.example.com"},
			},
			expectIDs: []string{"prev-id", ""},
		},
		{
			name: "delete flow: fewer entries than prev is not an error",
			next: []MCPServerConfig{
				{ID: "id-one", Name: "keep", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "id-one", Name: "keep", BaseURL: "https://one.example.com"},
				{ID: "id-two", Name: "delete-me", BaseURL: "https://two.example.com"},
			},
			expectIDs: []string{"id-one"},
		},
		{
			name: "prev entries without IDs cannot be claimed",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{""},
		},
		{
			name: "duplicate names reordered resolve by exact (Name, BaseURL)",
			next: []MCPServerConfig{
				{Name: "dup", BaseURL: "https://two.example.com"},
				{Name: "dup", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "id-one", Name: "dup", BaseURL: "https://one.example.com"},
				{ID: "id-two", Name: "dup", BaseURL: "https://two.example.com"},
			},
			expectIDs: []string{"id-two", "id-one"},
		},
		{
			name: "explicit-ID claim and exact match competing across phases rejected",
			next: []MCPServerConfig{
				// The ID-less entry exactly matches the stored server the
				// ID-bearing entry still holds: two entries strongly claiming
				// one identity is a corrupt payload, never a guess.
				{Name: "srv", BaseURL: "https://one.example.com"},
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
		{
			name: "weak match earlier in the payload cannot steal a later exact match",
			next: []MCPServerConfig{
				// Payload order: the unique-Name weak match comes first, but
				// the second entry is an exact (Name, BaseURL) round-trip of
				// the stored server. Exact claims resolve globally before any
				// weak claim, so the first entry stays new.
				{Name: "srv", BaseURL: "https://moved.example.com"},
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"", "prev-id"},
		},
		{
			name: "two identical ID-less entries competing for one stored server rejected",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
		{
			name: "two weak claims resolving to the same stored server rejected",
			next: []MCPServerConfig{
				// First entry matches by unique Name, second by unique
				// BaseURL — both resolve to the single stored server, and
				// which one deserves the identity cannot be decided.
				{Name: "srv", BaseURL: "https://a.example.com"},
				{Name: "other", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
		{
			name: "first write: ID-less entries stay ID-less for the caller to mint",
			next: []MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev:      nil,
			expectIDs: []string{""},
		},
		{
			name: "duplicate incoming IDs rejected",
			next: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
				{ID: "prev-id", Name: "copy", BaseURL: "https://two.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
		{
			name: "unknown ID colliding with a stored server identity rejected",
			next: []MCPServerConfig{
				{ID: "never-issued-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
		{
			name: "unknown ID with unique name and URL is kept (API automation)",
			next: []MCPServerConfig{
				{ID: "seeded-by-automation", Name: "new-srv", BaseURL: "https://new.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectIDs: []string{"seeded-by-automation"},
		},
		{
			name: "caller-chosen IDs on first write are kept",
			next: []MCPServerConfig{
				{ID: "seeded-by-automation", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev:      nil,
			expectIDs: []string{"seeded-by-automation"},
		},
		{
			name: "rename plus URL-change crossing two prev servers rejected",
			next: []MCPServerConfig{
				// Name matches prev B, BaseURL matches prev A: never guess.
				{Name: "beta", BaseURL: "https://alpha.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "id-alpha", Name: "alpha", BaseURL: "https://alpha.example.com"},
				{ID: "id-beta", Name: "beta", BaseURL: "https://beta.example.com"},
			},
			expectErr: true,
		},
		{
			name: "multiple name matches with no exact match rejected",
			next: []MCPServerConfig{
				{Name: "dup", BaseURL: "https://three.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "id-one", Name: "dup", BaseURL: "https://one.example.com"},
				{ID: "id-two", Name: "dup", BaseURL: "https://two.example.com"},
			},
			expectErr: true,
		},
		{
			name: "duplicate non-empty stored IDs rejected",
			next: []MCPServerConfig{
				// One entry claims the ID explicitly, the other by exact
				// (Name, BaseURL) match: with two stored rows sharing the ID,
				// both claims could be satisfied by different rows, leaving
				// two servers guarded by one policy identity.
				{ID: "shared-id", Name: "srv", BaseURL: "https://one.example.com"},
				{Name: "copy", BaseURL: "https://two.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "shared-id", Name: "srv", BaseURL: "https://one.example.com"},
				{ID: "shared-id", Name: "copy", BaseURL: "https://two.example.com"},
			},
			expectErr: true,
		},
		{
			name: "multiple exact (Name, BaseURL) duplicates in prev rejected",
			next: []MCPServerConfig{
				{Name: "dup", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "id-one", Name: "dup", BaseURL: "https://one.example.com"},
				{ID: "id-two", Name: "dup", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReconcileMCPServerIDs(tt.next, tt.prev)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrMCPServerIDConflict)
				return
			}
			require.NoError(t, err)
			require.Len(t, result, len(tt.expectIDs))
			for i, id := range tt.expectIDs {
				require.Equal(t, id, result[i].ID, "server %d", i)
			}
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
			name: "happy path carries remote/embedded IDs and prev plugin rows",
			next: MCPConfig{
				Servers:        []MCPServerConfig{{Name: "Jira", BaseURL: "https://jira.example.com"}},
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
