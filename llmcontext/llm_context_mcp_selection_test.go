// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
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

func (p *recordingMCPToolProvider) GetToolsWithSelection(_ stdcontext.Context, _ mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
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

// dmOnlyDescribingMCPToolProvider also names the DM-only servers a selection
// leaves out, recording whether it was asked.
type dmOnlyDescribingMCPToolProvider struct {
	recordingMCPToolProvider
	described bool
}

func (p *dmOnlyDescribingMCPToolProvider) DescribeDMOnlyMCPServers(_ stdcontext.Context, _ mcp.CatalogRequest, selection mcp.ToolSelection) []llm.DMOnlyMCPServer {
	p.described = true
	if !selection.ExcludeDMOnlyServers {
		return nil
	}
	return []llm.DMOnlyMCPServer{{Name: "Salesforce"}}
}

// DM-only servers are selected only when the caller marks the request as a DM
// and its channel really is the requester's DM with this agent; everything
// else, including callers that say nothing, excludes them.
func TestBuilderSelectsDMOnlyServersOnlyInDirectMessage(t *testing.T) {
	const botUserID = "bot-id"
	dmWithBot := &model.Channel{Id: "dm", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds("user-id", botUserID)}
	dmWithOther := &model.Channel{Id: "dm-other", Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds("user-id", "someone-else")}
	group := &model.Channel{Id: "gm", Type: model.ChannelTypeGroup, Name: "group"}
	open := &model.Channel{Id: "open", Type: model.ChannelTypePrivate}

	testCases := []struct {
		name          string
		channel       *model.Channel
		directMessage bool
		interactive   bool
		wantExclude   bool
		wantDescribed []llm.DMOnlyMCPServer
	}{
		{name: "marked DM with the agent selects DM-only servers", channel: dmWithBot, directMessage: true, interactive: true},
		{name: "unmarked DM with the agent excludes them", channel: dmWithBot, interactive: true, wantExclude: true, wantDescribed: []llm.DMOnlyMCPServer{{Name: "Salesforce"}}},
		{name: "marked DM with someone else excludes them", channel: dmWithOther, directMessage: true, interactive: true, wantExclude: true, wantDescribed: []llm.DMOnlyMCPServer{{Name: "Salesforce"}}},
		{name: "marked group message excludes them", channel: group, directMessage: true, interactive: true, wantExclude: true, wantDescribed: []llm.DMOnlyMCPServer{{Name: "Salesforce"}}},
		{name: "marked channel excludes them", channel: open, directMessage: true, interactive: true, wantExclude: true, wantDescribed: []llm.DMOnlyMCPServer{{Name: "Salesforce"}}},
		{name: "a request with no channel excludes them", directMessage: true, interactive: true, wantExclude: true, wantDescribed: []llm.DMOnlyMCPServer{{Name: "Salesforce"}}},
		{name: "a request nobody is present for names no servers", channel: open, wantExclude: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mcpProvider := &dmOnlyDescribingMCPToolProvider{}
			builder := newLicenseTestBuilder(t, true, &staticToolProvider{}, mcpProvider)
			bot := newTestBotWithConfig(llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true})

			var opts []llm.ContextOption
			if tc.directMessage {
				opts = append(opts, builder.WithLLMContextDirectMessage())
			}
			if tc.interactive {
				opts = append(opts, builder.WithLLMContextInteractive())
			}
			opts = append(opts, builder.WithLLMContextTools(stdcontext.Background(), bot))
			context := builder.BuildLLMContextUserRequest(bot, testUser(), tc.channel, opts...)

			require.Equal(t, tc.wantExclude, mcpProvider.selection.ExcludeDMOnlyServers)
			require.Equal(t, tc.interactive, mcpProvider.described)
			require.Equal(t, tc.wantDescribed, context.DMOnlyMCPServers)
		})
	}
}
