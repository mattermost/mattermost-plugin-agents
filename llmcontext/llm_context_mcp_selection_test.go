// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/stretchr/testify/require"
)

const (
	jiraOrigin   = "https://jira.example.com"
	githubOrigin = "https://api.githubcopilot.com"
)

// recordingMCPToolProvider captures the selection the builder computed so tests
// can assert on what would have been contacted, not just on what survived the
// tool-level filters.
type recordingMCPToolProvider struct {
	tools     []llm.Tool
	selection mcp.ToolSelection
	calls     int
}

func (p *recordingMCPToolProvider) GetToolsForUser(_ stdcontext.Context, _ string, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	p.calls++
	p.selection = selection

	retained := make([]llm.Tool, 0, len(p.tools))
	for _, tool := range p.tools {
		if selection.Allows(tool.ServerOrigin) {
			retained = append(retained, tool)
		}
	}
	return retained, nil
}

// The builder must resolve server eligibility before tools are requested, so
// an agent, a user preference, or a missing license keeps a server from being
// contacted at all.
func TestBuilderPassesServerEligibilityToMCPProvider(t *testing.T) {
	testCases := []struct {
		name            string
		licensed        bool
		botConfig       llm.BotConfig
		disabledOrigins []string
		wantAllowed     []string
		wantDenied      []string
		wantExcludeRemo bool
		wantToolNames   []string
	}{
		{
			name:      "auto-enabling agent selects every server",
			licensed:  true,
			botConfig: llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true},
			// A nil allowlist means "no restriction".
			wantAllowed:   nil,
			wantToolNames: []string{"builtin", "mattermost__read_channel", "jira__get_issue", "github__search"},
		},
		{
			name:     "agent allowlist narrows the selection to its origins",
			licensed: true,
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty",
				EnabledMCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: jiraOrigin, ToolName: "get_issue"},
					{ServerOrigin: mcp.EmbeddedClientKey, ToolName: llm.MCPServerToolWildcard},
				},
			},
			wantAllowed:   []string{jiraOrigin, mcp.EmbeddedClientKey},
			wantToolNames: []string{"builtin", "mattermost__read_channel", "jira__get_issue"},
		},
		{
			// A wildcard entry names no individual tool but still names its
			// server, so the server stays eligible.
			name:     "a wildcard allowlist entry selects its server",
			licensed: true,
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty",
				EnabledMCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: jiraOrigin, ToolName: llm.MCPServerToolWildcard},
				},
			},
			wantAllowed:   []string{jiraOrigin},
			wantToolNames: []string{"builtin", "jira__get_issue"},
		},
		{
			name:     "an agent that allowlists nothing selects no servers",
			licensed: true,
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty",
			},
			wantAllowed:   []string{},
			wantToolNames: []string{"builtin"},
		},
		{
			name:            "user-disabled servers are denied",
			licensed:        true,
			botConfig:       llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true},
			disabledOrigins: []string{githubOrigin},
			wantDenied:      []string{githubOrigin},
			wantToolNames:   []string{"builtin", "mattermost__read_channel", "jira__get_issue"},
		},
		{
			name:            "unlicensed servers exclude everything but the embedded server",
			licensed:        false,
			botConfig:       llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true},
			wantExcludeRemo: true,
			wantToolNames:   []string{"builtin", "mattermost__read_channel"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mcpProvider := &recordingMCPToolProvider{tools: []llm.Tool{
				testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
				testMCPTool("jira__get_issue", jiraOrigin, "fetch Jira issue details"),
				testMCPTool("github__search", githubOrigin, "search GitHub"),
			}}
			builder := newLicenseTestBuilder(t, tc.licensed,
				&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
				mcpProvider,
			)
			bot := newTestBotWithConfig(tc.botConfig)

			var opts []llm.ContextOption
			if len(tc.disabledOrigins) > 0 {
				opts = append(opts, builder.WithLLMContextDisabledMCPServers(tc.disabledOrigins))
			}
			context := buildToolsContext(builder, bot, opts...)

			require.Equal(t, 1, mcpProvider.calls)
			require.Equal(t, tc.wantAllowed, mcpProvider.selection.AllowedOrigins)
			require.Equal(t, tc.wantDenied, mcpProvider.selection.DeniedOrigins)
			require.Equal(t, tc.wantExcludeRemo, mcpProvider.selection.ExcludeRemoteServers)
			require.ElementsMatch(t, tc.wantToolNames, toolNames(context.Tools))
		})
	}
}
