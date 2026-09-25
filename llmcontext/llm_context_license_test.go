// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const licenseTestRemoteOrigin = "https://jira.example.com"
const licenseTestPluginOrigin = "plugin://com.mattermost.plugin-playbooks"

// newLicenseTestBuilder mirrors newTestBuilder but lets the test control the
// license state the builder sees.
func newLicenseTestBuilder(t *testing.T, licensed bool, toolProvider ToolProvider, mcpProvider MCPToolProvider) *Builder {
	t.Helper()
	level := enterprise.LevelUnlicensed
	if licensed {
		level = enterprise.LevelEnterprise
	}
	return newLicenseTestBuilderAt(t, level, toolProvider, mcpProvider)
}

func newLicenseTestBuilderAt(t *testing.T, level enterprise.Level, toolProvider ToolProvider, mcpProvider MCPToolProvider) *Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	siteName := "Mattermost"
	siteURL := "https://example.com"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
	mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(level)).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
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

// selectionHonoringMCPProvider mimics the real client manager's contract:
// servers outside the selection are never contacted, so their tools and auth
// errors never appear in the result. The license gate is enforced solely
// through that selection, which is what these tests pin end to end.
type selectionHonoringMCPProvider struct {
	tools  []llm.Tool
	errors *mcp.Errors
}

func (p *selectionHonoringMCPProvider) GetToolsWithSelection(_ stdcontext.Context, _ mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	var tools []llm.Tool
	for _, tool := range p.tools {
		if selection.Allows(tool.ServerOrigin) {
			tools = append(tools, tool)
		}
	}

	if p.errors == nil {
		return tools, nil
	}
	mcpErrors := &mcp.Errors{Errors: p.errors.Errors}
	for _, authErr := range p.errors.ToolAuthErrors {
		if selection.Allows(authErr.ServerOrigin) {
			mcpErrors.ToolAuthErrors = append(mcpErrors.ToolAuthErrors, authErr)
		}
	}
	return tools, mcpErrors
}

func licenseTestMCPProvider() *selectionHonoringMCPProvider {
	return &selectionHonoringMCPProvider{tools: []llm.Tool{
		testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
		testMCPTool("jira__get_issue", licenseTestRemoteOrigin, "fetch Jira issue details"),
		testMCPTool("playbooks__run", licenseTestPluginOrigin, "start a playbook run"),
	}}
}

// TestUnlicensedBuilderDropsRemoteMCPTools pins the supply-time license gate:
// remote and plugin MCP tools are available at Enterprise and above, while
// built-in and embedded Mattermost MCP tools are available at every level.
func TestUnlicensedBuilderDropsRemoteMCPTools(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			builder := newLicenseTestBuilderAt(t, level,
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

			want := []string{"builtin", "mattermost__read_channel"}
			if level >= enterprise.LevelEnterprise {
				want = append(want, "jira__get_issue", "playbooks__run")
			}
			require.ElementsMatch(t, want, toolNames(context.Tools))
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		builder := newLicenseTestBuilderAt(t, enterprise.LevelEnterpriseAdvanced,
			&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
			licenseTestMCPProvider(),
		)
		builder.licenseChecker = nil
		bot := newTestBotWithConfig(llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
		})

		context := buildToolsContext(builder, bot)
		require.ElementsMatch(t, []string{"builtin", "mattermost__read_channel"}, toolNames(context.Tools))
	})
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

// Service-account catalogs are available at Enterprise and above. Below that,
// SA-flagged agents use the per-user catalog.
func TestServiceAccountCatalogLicenseGate(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			provider := &staticMCPToolProvider{
				tools: []llm.Tool{
					testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
					testMCPTool("jira__get_issue", licenseTestRemoteOrigin, "fetch Jira issue details"),
				},
				saTools: []llm.Tool{
					testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
					testMCPTool("sa_jira__get_issue", licenseTestRemoteOrigin, "service account Jira"),
				},
			}
			builder := newLicenseTestBuilderAt(t, level,
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

			if level >= enterprise.LevelEnterprise {
				require.Empty(t, provider.userCalls)
				require.NotEmpty(t, provider.saCalls)
				require.ElementsMatch(t, []string{"builtin", "mattermost__read_channel", "sa_jira__get_issue"}, toolNames(context.Tools))
				require.Equal(t, llm.ToolAuthModeServiceAccount, context.ToolAuthMode)
				return
			}

			require.Equal(t, []string{"user-id"}, provider.userCalls)
			require.Empty(t, provider.saCalls)
			require.ElementsMatch(t, []string{"builtin", "mattermost__read_channel"}, toolNames(context.Tools))
			require.Empty(t, context.ToolAuthMode)
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		provider := &staticMCPToolProvider{
			tools:   []llm.Tool{testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts")},
			saTools: []llm.Tool{testMCPTool("sa_jira__get_issue", licenseTestRemoteOrigin, "service account Jira")},
		}
		builder := newLicenseTestBuilderAt(t, enterprise.LevelEnterpriseAdvanced,
			&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
			provider,
		)
		builder.licenseChecker = nil
		bot := newTestBotWithConfig(llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UseServiceAccountAuth: true,
		})

		context := buildToolsContext(builder, bot)
		require.Equal(t, []string{"user-id"}, provider.userCalls)
		require.Empty(t, provider.saCalls)
		require.Empty(t, context.ToolAuthMode)
	})
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
