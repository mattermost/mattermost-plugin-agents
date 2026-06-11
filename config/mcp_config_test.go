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
