// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const licenseTestRemoteOrigin = "https://jira.example.com"

type staticToolProvider struct {
	tools []llm.Tool
}

func (p *staticToolProvider) GetTools(*bots.Bot) []llm.Tool {
	return p.tools
}

func testBuiltinTool(name string) llm.Tool {
	return llm.Tool{
		Name:        name,
		Description: name + " built-in",
		Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "builtin:" + name, nil
		},
	}
}

func testMCPTool(name, origin, description string) llm.Tool {
	return llm.Tool{
		Name:         name,
		Description:  description,
		ServerOrigin: origin,
		Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "mcp:" + name, nil
		},
	}
}

func toolNames(store *llm.ToolStore) []string {
	if store == nil {
		return nil
	}

	tools := store.GetTools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func buildToolsContext(builder *Builder, bot *bots.Bot, opts ...llm.ContextOption) *llm.Context {
	allOpts := append([]llm.ContextOption{}, opts...)
	allOpts = append(allOpts, builder.WithLLMContextDefaultTools(bot))
	return builder.BuildLLMContextUserRequest(
		bot,
		&model.User{Id: "user-id", Username: "test-user", Locale: "en"},
		&model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect},
		allOpts...,
	)
}

// newLicenseTestBuilder mirrors the other builder helpers but lets the test
// control the license state the builder sees.
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
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()

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
