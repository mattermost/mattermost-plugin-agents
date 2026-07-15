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
		servers          []MCPServerConfig
		expectByOrigin   map[string]string
		expectByServerID map[string]string
	}{
		{
			name:             "empty servers",
			servers:          nil,
			expectByOrigin:   map[string]string{},
			expectByServerID: map[string]string{},
		},
		{
			name: "servers without IDs omitted",
			servers: []MCPServerConfig{
				{ID: "id-one", Name: "one", BaseURL: "https://one.example.com"},
				{Name: "no-id", BaseURL: "https://two.example.com"},
			},
			expectByOrigin:   map[string]string{"https://one.example.com": "id-one"},
			expectByServerID: map[string]string{"id-one": "https://one.example.com"},
		},
		{
			name: "duplicate BaseURL last entry wins",
			servers: []MCPServerConfig{
				{ID: "id-first", Name: "first", BaseURL: "https://dup.example.com"},
				{ID: "id-last", Name: "last", BaseURL: "https://dup.example.com"},
			},
			expectByOrigin: map[string]string{"https://dup.example.com": "id-last"},
			expectByServerID: map[string]string{
				"id-first": "https://dup.example.com",
				"id-last":  "https://dup.example.com",
			},
		},
		{
			name: "disabled server included",
			servers: []MCPServerConfig{
				{ID: "id-disabled", Name: "off", Enabled: false, BaseURL: "https://off.example.com"},
			},
			expectByOrigin:   map[string]string{"https://off.example.com": "id-disabled"},
			expectByServerID: map[string]string{"id-disabled": "https://off.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &MCPConfig{Servers: tt.servers}
			require.Equal(t, tt.expectByOrigin, cfg.ServerIDByOrigin())
			require.Equal(t, tt.expectByServerID, cfg.OriginByServerID())
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
			name: "first write: incoming ID with no stored config rejected",
			next: []MCPServerConfig{
				{ID: "invented-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev:      nil,
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
			name: "fabricated incoming ID rejected",
			next: []MCPServerConfig{
				{ID: "never-issued-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			prev: []MCPServerConfig{
				{ID: "prev-id", Name: "srv", BaseURL: "https://one.example.com"},
			},
			expectErr: true,
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
