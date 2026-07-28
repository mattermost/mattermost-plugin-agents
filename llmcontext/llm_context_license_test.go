// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const licenseTestRemoteOrigin = "https://jira.example.com"

// newLicenseTestBuilder mirrors newTestBuilder but lets the test control the
// license state the builder sees.
func newLicenseTestBuilder(t *testing.T, licensed bool, toolProvider ToolProvider, mcpProvider MCPToolProvider) *Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	siteName := "Mattermost"
	siteURL := "https://example.com"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
	if licensed {
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	} else {
		mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
	}
	for i := 1; i <= 10; i++ {
		args := make([]interface{}, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe().Return()
		mockAPI.On("LogWarn", args...).Maybe().Return()
		mockAPI.On("LogError", args...).Maybe().Return()
	}

	return NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		toolProvider,
		mcpProvider,
		&contextTestConfigProvider{},
	)
}

func licenseTestMCPProvider() *staticMCPToolProvider {
	return &staticMCPToolProvider{tools: []llm.Tool{
		testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
		testMCPTool("jira__get_issue", licenseTestRemoteOrigin, "fetch Jira issue details"),
	}}
}

// TestUnlicensedBuilderDropsRemoteMCPTools pins the supply-time license gate:
// without a license, remote MCP tools are never added to the LLM tool store,
// while built-in and embedded Mattermost MCP tools remain available. With a
// license, everything is supplied.
func TestUnlicensedBuilderDropsRemoteMCPTools(t *testing.T) {
	tests := []struct {
		name      string
		licensed  bool
		wantTools []string
	}{
		{
			name:      "unlicensed supplies only builtin and embedded tools",
			licensed:  false,
			wantTools: []string{"builtin", "mattermost__read_channel"},
		},
		{
			name:      "licensed supplies remote tools too",
			licensed:  true,
			wantTools: []string{"builtin", "mattermost__read_channel", "jira__get_issue"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := newLicenseTestBuilder(t, tc.licensed,
				&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
				licenseTestMCPProvider(),
			)
			bot := newTestBotWithConfig(llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
			})

			context := buildToolsContext(builder, bot)

			require.ElementsMatch(t, tc.wantTools, toolNames(context.Tools))
		})
	}
}

// TestUnlicensedBuilderDropsRemoteMCPToolsFromDynamicRegistry pins that the
// dynamic tool loading registry is filtered too: on an unlicensed server the
// model can neither discover a remote tool via search_tools nor load it via
// load_tool.
func TestUnlicensedBuilderDropsRemoteMCPToolsFromDynamicRegistry(t *testing.T) {
	tests := []struct {
		name           string
		licensed       bool
		wantRemoteInfo bool
	}{
		{name: "unlicensed registry excludes remote tools", licensed: false, wantRemoteInfo: false},
		{name: "licensed registry includes remote tools", licensed: true, wantRemoteInfo: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := newLicenseTestBuilder(t, tc.licensed,
				&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
				licenseTestMCPProvider(),
			)
			bot := newTestBotWithConfig(llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			})

			context := buildToolsContext(builder, bot)

			require.Equal(t, tc.wantRemoteInfo, context.Tools.IsUnloadedMCPTool("jira__get_issue"),
				"remote tool availability in the dynamic loading registry")
			require.True(t, context.Tools.IsUnloadedMCPTool("mattermost__read_channel"),
				"embedded tool must stay available in the dynamic loading registry")

			searchResults := searchToolNames(t, context.Tools, "issue")
			if tc.wantRemoteInfo {
				require.Contains(t, searchResults, "jira__get_issue")
			} else {
				require.NotContains(t, searchResults, "jira__get_issue")
			}
		})
	}
}

// TestServiceAccountModeFullyOffWhenUnlicensed pins the settled licensing
// decision: Service Account authentication inherits the remote-MCP enterprise
// gate, so on an unlicensed server an SA-flagged agent behaves exactly like a
// normal agent — per-user catalog (embedded tools as the requesting user),
// remote tools dropped, and no service account attribution.
func TestServiceAccountModeFullyOffWhenUnlicensed(t *testing.T) {
	provider := &staticMCPToolProvider{
		tools: []llm.Tool{
			testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			testMCPTool("jira__get_issue", licenseTestRemoteOrigin, "fetch Jira issue details"),
		},
		saTools: []llm.Tool{testMCPTool("sa_jira__get_issue", licenseTestRemoteOrigin, "service account Jira")},
	}
	builder := newLicenseTestBuilder(t, false,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		provider,
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		UseServiceAccountAuth: true,
	})

	context := buildToolsContext(builder, bot)

	require.Equal(t, []string{"user-id"}, provider.userCalls, "unlicensed SA agents use the per-user catalog")
	require.Empty(t, provider.saCalls, "unlicensed servers must never build a service account catalog")
	require.ElementsMatch(t, []string{"builtin", "mattermost__read_channel"}, toolNames(context.Tools))
	require.Empty(t, context.ToolAuthMode, "unlicensed SA agents are attributed as user mode")
}

// TestUnlicensedBuilderDropsRemoteMCPAuthErrors pins that OAuth prompts for
// remote servers are not surfaced when their tools cannot be used without a
// license.
func TestUnlicensedBuilderDropsRemoteMCPAuthErrors(t *testing.T) {
	tests := []struct {
		name           string
		licensed       bool
		wantAuthErrors int
	}{
		{name: "unlicensed drops remote auth errors", licensed: false, wantAuthErrors: 0},
		{name: "licensed keeps remote auth errors", licensed: true, wantAuthErrors: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mcpProvider := licenseTestMCPProvider()
			mcpProvider.errors = &mcp.Errors{
				ToolAuthErrors: []llm.ToolAuthError{{
					ServerName:   "Jira",
					ServerOrigin: licenseTestRemoteOrigin,
					AuthURL:      "https://jira.example.com/oauth",
				}},
			}
			builder := newLicenseTestBuilder(t, tc.licensed,
				&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
				mcpProvider,
			)
			bot := newTestBotWithConfig(llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
			})

			context := buildToolsContext(builder, bot)

			require.Len(t, context.Tools.GetAuthErrors(), tc.wantAuthErrors)
		})
	}
}
