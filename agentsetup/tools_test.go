// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentsetup

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisableToolsLeavesTheStoredAgentUntouched(t *testing.T) {
	stored := llm.BotConfig{
		EnabledNativeTools: []string{"web_search", "code_interpreter", "image_generation"},
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: "https://a", ToolName: "one"},
			{ServerOrigin: "https://a", ToolName: "two"},
		},
	}
	storedNative := append([]string(nil), stored.EnabledNativeTools...)
	storedMCP := append([]llm.EnabledMCPTool(nil), stored.EnabledMCPTools...)

	// agentconfig hands Apply a shallow copy, so the slices share backing arrays.
	proposed := stored
	args, err := normalizeDisableArgs(disableMyToolsArgs{
		NativeTools: []string{"web_search"},
		MCPTools:    []llm.EnabledMCPTool{{ServerOrigin: "https://a", ToolName: "one"}},
	})
	require.NoError(t, err)
	require.NoError(t, disableTools(&proposed, args))

	assert.Equal(t, []string{"code_interpreter", "image_generation"}, proposed.EnabledNativeTools)
	assert.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: "https://a", ToolName: "two"}}, proposed.EnabledMCPTools)
	assert.Equal(t, storedNative, stored.EnabledNativeTools, "the stored agent's native tools must not change")
	assert.Equal(t, storedMCP, stored.EnabledMCPTools, "the stored agent's MCP tools must not change")
}

func TestNormalizeDisableArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    disableMyToolsArgs
		want    disableMyToolsArgs
		wantErr string
	}{
		{
			name: "trims and deduplicates names",
			args: disableMyToolsArgs{
				NativeTools: []string{" web_search", "web_search ", "code_interpreter"},
				MCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: "https://a", ToolName: "one"},
					{ServerOrigin: " https://a ", ToolName: "one "},
				},
			},
			want: disableMyToolsArgs{
				NativeTools: []string{"web_search", "code_interpreter"},
				MCPTools:    []llm.EnabledMCPTool{{ServerOrigin: "https://a", ToolName: "one"}},
			},
		},
		{name: "all on its own", args: disableMyToolsArgs{All: true}, want: disableMyToolsArgs{All: true}},
		{name: "an empty native tool name", args: disableMyToolsArgs{NativeTools: []string{"web_search", "  "}}, wantErr: "empty name"},
		{name: "an MCP tool without a name", args: disableMyToolsArgs{MCPTools: []llm.EnabledMCPTool{{ServerOrigin: "https://a"}}}, wantErr: "tool_name"},
		{name: "an MCP tool without a server", args: disableMyToolsArgs{MCPTools: []llm.EnabledMCPTool{{ToolName: "one"}}}, wantErr: "server_origin"},
		{name: "nothing named", args: disableMyToolsArgs{}, wantErr: "name the tools"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDisableArgs(tt.args)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
