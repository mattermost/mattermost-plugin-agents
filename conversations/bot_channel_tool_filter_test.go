// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mapPolicyChecker map[string]map[string]struct {
	policy  string
	enabled bool
}

func (m mapPolicyChecker) GetToolPolicy(serverOrigin, toolName string) (string, bool) {
	byServer, ok := m[serverOrigin]
	if !ok {
		return mcp.ToolPolicyAsk, false
	}
	cfg, ok := byServer[toolName]
	if !ok {
		cfg, ok = byServer[llm.BareMCPToolName(toolName)]
	}
	if !ok {
		return mcp.ToolPolicyAsk, true
	}
	return cfg.policy, cfg.enabled
}

type channelFollowUpTestToolProvider struct{}

func (p *channelFollowUpTestToolProvider) GetTools(*bots.Bot, *llm.Context) []llm.Tool {
	return nil
}

type channelFollowUpTestMCPToolProvider struct {
	tools []llm.Tool
}

func (p *channelFollowUpTestMCPToolProvider) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	return p.tools, nil
}

type channelFollowUpTestConfig struct {
	enableChannelMentionToolCalling bool
}

func (c *channelFollowUpTestConfig) EnableChannelMentionToolCalling() bool {
	return c.enableChannelMentionToolCalling
}

func (c *channelFollowUpTestConfig) AllowNativeWebSearchInChannels() bool {
	return false
}

func (c *channelFollowUpTestConfig) MCP() mcp.Config {
	return mcp.Config{}
}

func (c *channelFollowUpTestConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func channelFollowUpTestMCPTool(name, origin, description string) llm.Tool {
	return llm.Tool{
		Name:         name,
		Description:  description,
		ServerOrigin: origin,
		Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "mcp:" + name, nil
		},
	}
}

func newChannelFollowUpTestBuilder(t *testing.T, mcpTools []llm.Tool, config *channelFollowUpTestConfig) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
	mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&channelFollowUpTestToolProvider{},
		&channelFollowUpTestMCPToolProvider{tools: mcpTools},
		config,
	)
}

func channelFollowUpTestBot() *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			MCPDynamicToolLoading: true,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		nil,
	)
}

func channelFollowUpContextToolArgs(raw string) llm.ToolArgumentGetter {
	return func(args any) error {
		return json.Unmarshal([]byte(raw), args)
	}
}

func channelFollowUpSearchToolNames(t *testing.T, store *llm.ToolStore, query string) []string {
	t.Helper()

	searchTool := store.GetTool(mcp.SearchToolsName)
	require.NotNil(t, searchTool)

	resultJSON, err := searchTool.Resolver(context.Background(), &llm.Context{Tools: store}, channelFollowUpContextToolArgs(`{"query":"`+query+`"}`))
	require.NoError(t, err)

	var result mcp.SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))

	names := make([]string, 0, len(result.Tools))
	for _, item := range result.Tools {
		names = append(names, item.Name)
	}
	return names
}

func buildChannelFollowUpStrictContext(t *testing.T, builder *llmcontext.Builder, opts []llm.ContextOption) *llm.Context {
	t.Helper()

	allOpts := append([]llm.ContextOption{}, opts...)
	bot := channelFollowUpTestBot()
	allOpts = append(allOpts, builder.WithLLMContextDefaultTools(context.Background(), bot))

	return builder.BuildLLMContextUserRequest(
		bot,
		&model.User{Id: "user-id", Username: "user", Locale: "en"},
		&model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen},
		allOpts...,
	)
}

func TestApplyBotChannelAutoEverywhereToolFilter(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"auto_run_tool":   {policy: mcp.ToolPolicyAutoRunInDM, enabled: true},
				"ask_tool":        {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", ServerOrigin: "", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "everywhere_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "auto_run_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "everywhere_tool", tools[0].Name)
	require.Len(t, llmContext.DisabledToolsInfo, 3)
}

func TestApplyBotChannelAutoEverywhereToolFilter_NamespacedToolUsesBarePolicy(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "server__everywhere_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "server__everywhere_tool", tools[0].Name)
}

func TestApplyToolAvailabilityBeforeBotChannelFilterPreservesDisabledToolsInfo(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"everywhere_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"ask_tool":        {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", Description: "builtin tool", ServerOrigin: "", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "everywhere_tool", Description: "auto everywhere", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", Description: "needs approval", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	toolsDisabled := applyToolAvailability(llmContext, false, true)
	require.False(t, toolsDisabled)

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "everywhere_tool", tools[0].Name)

	disabledNames := make([]string, 0, len(llmContext.DisabledToolsInfo))
	for _, info := range llmContext.DisabledToolsInfo {
		disabledNames = append(disabledNames, info.Name)
	}
	require.ElementsMatch(t, []string{"builtin", "ask_tool"}, disabledNames)
}

func TestApplyBotChannelAutoEverywhereToolFilter_nilCheckerFailClosed(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{toolPolicyChecker: nil}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "builtin", ServerOrigin: "", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "mcp_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	require.Empty(t, llmContext.Tools.GetTools())
	require.Len(t, llmContext.DisabledToolsInfo, 2)
}

func TestBotChannelAutoEverywhereFilterKeepsMetaTools(t *testing.T) {
	origin := "https://mcp.example.com/mcp"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"safe_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"ask_tool":  {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: mcp.SearchToolsName, Description: "search meta", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: mcp.LoadToolName, Description: "load meta", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "builtin", Description: "builtin tool", ServerOrigin: "", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "safe_tool", Description: "auto everywhere", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", Description: "needs approval", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	toolNames := make([]string, 0, len(llmContext.Tools.GetTools()))
	for _, tool := range llmContext.Tools.GetTools() {
		toolNames = append(toolNames, tool.Name)
	}
	require.ElementsMatch(t, []string{mcp.SearchToolsName, mcp.LoadToolName, "safe_tool"}, toolNames)

	disabledNames := make([]string, 0, len(llmContext.DisabledToolsInfo))
	for _, info := range llmContext.DisabledToolsInfo {
		disabledNames = append(disabledNames, info.Name)
	}
	require.ElementsMatch(t, []string{"builtin", "ask_tool"}, disabledNames)
}

func TestBotChannelAutoEverywhereFilterKeepsMetaToolsWithNilChecker(t *testing.T) {
	c := &Conversations{toolPolicyChecker: nil}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: mcp.SearchToolsName, Description: "search meta", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: mcp.LoadToolName, Description: "load meta", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "builtin", Description: "builtin tool", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "ask_tool", Description: "needs approval", ServerOrigin: "https://mcp.example.com", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	toolNames := make([]string, 0, len(llmContext.Tools.GetTools()))
	for _, tool := range llmContext.Tools.GetTools() {
		toolNames = append(toolNames, tool.Name)
	}
	require.ElementsMatch(t, []string{mcp.SearchToolsName, mcp.LoadToolName}, toolNames)

	disabledNames := make([]string, 0, len(llmContext.DisabledToolsInfo))
	for _, info := range llmContext.DisabledToolsInfo {
		disabledNames = append(disabledNames, info.Name)
	}
	require.ElementsMatch(t, []string{"builtin", "ask_tool"}, disabledNames)
}

func TestBotChannelAutoEverywhereFilterDenormalizesNamespacedTool(t *testing.T) {
	origin := "https://mcp.atlassian.com"
	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"safe_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
				"ask_tool":  {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	llmContext := &llm.Context{
		Tools: llm.NewToolStore(),
	}
	llmContext.Tools.AddTools([]llm.Tool{
		{Name: "jira__safe_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
		{Name: "jira__ask_tool", ServerOrigin: origin, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "", nil }},
	})

	c.applyBotChannelAutoEverywhereToolFilter(llmContext)

	tools := llmContext.Tools.GetTools()
	require.Len(t, tools, 1)
	require.Equal(t, "jira__safe_tool", tools[0].Name)
}

func TestChannelFollowUpStrictRegistry(t *testing.T) {
	const origin = "https://mcp.atlassian.com"

	channelPolicyChecker := mapPolicyChecker{
		origin: {
			"safe_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			"ask_tool":  {policy: mcp.ToolPolicyAsk, enabled: true},
		},
	}
	tests := []struct {
		name              string
		isDM              bool
		toolPolicyChecker mapPolicyChecker
		setupMMClient     func(t *testing.T) *mocks.MockClient
		expectedFiltered  bool
		expectedOptsEmpty bool
		expectSafeTool    bool
		expectAskTool     bool
	}{
		{
			name:              "get post failure fails closed",
			toolPolicyChecker: channelPolicyChecker,
			setupMMClient: func(t *testing.T) *mocks.MockClient {
				mmClient := mocks.NewMockClient(t)
				mmClient.On("GetPost", "root-id").Return((*model.Post)(nil), errors.New("missing root post")).Once()
				return mmClient
			},
			expectedFiltered: true,
			expectSafeTool:   true,
			expectAskTool:    false,
		},
		{
			name:              "get user failure fails closed",
			toolPolicyChecker: channelPolicyChecker,
			setupMMClient: func(t *testing.T) *mocks.MockClient {
				mmClient := mocks.NewMockClient(t)
				mmClient.On("GetPost", "root-id").Return(&model.Post{UserId: "root-user"}, nil).Once()
				mmClient.On("GetUser", "root-user").Return((*model.User)(nil), errors.New("missing root user")).Once()
				return mmClient
			},
			expectedFiltered: true,
			expectSafeTool:   true,
			expectAskTool:    false,
		},
		{
			name:              "DM does not apply channel filter",
			isDM:              true,
			toolPolicyChecker: mapPolicyChecker{},
			expectedFiltered:  false,
			expectedOptsEmpty: true,
			expectSafeTool:    true,
			expectAskTool:     true,
		},
		{
			name:              "confirmed ActivateAI filters auto everywhere",
			toolPolicyChecker: channelPolicyChecker,
			setupMMClient: func(t *testing.T) *mocks.MockClient {
				rootPost := &model.Post{UserId: "bot-user"}
				rootPost.AddProp(ActivateAIProp, true)
				mmClient := mocks.NewMockClient(t)
				mmClient.On("GetPost", "root-id").Return(rootPost, nil).Once()
				mmClient.On("GetUser", "bot-user").Return(&model.User{Id: "bot-user", IsBot: true}, nil).Once()
				return mmClient
			},
			expectedFiltered: true,
			expectSafeTool:   true,
			expectAskTool:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &channelFollowUpTestConfig{enableChannelMentionToolCalling: true}
			builder := newChannelFollowUpTestBuilder(t, []llm.Tool{
				channelFollowUpTestMCPTool("jira__safe_tool", origin, "auto-everywhere safe channel follow-up capability"),
				channelFollowUpTestMCPTool("jira__ask_tool", origin, "approval-only channel follow-up capability"),
			}, config)
			c := &Conversations{
				contextBuilder:    builder,
				configProvider:    config,
				toolPolicyChecker: tt.toolPolicyChecker,
			}
			if tt.setupMMClient != nil {
				c.mmClient = tt.setupMMClient(t)
			}
			rootID := "root-id"

			opts, filtered := c.channelFollowUpMCPToolFilterContextOptions(tt.isDM, &store.Conversation{RootPostID: &rootID})
			require.Equal(t, tt.expectedFiltered, filtered)
			if tt.expectedOptsEmpty {
				require.Empty(t, opts)
			}

			llmContext := buildChannelFollowUpStrictContext(t, builder, opts)
			if tt.expectSafeTool {
				require.Contains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "safe"), "jira__safe_tool")
			} else {
				require.NotContains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "safe"), "jira__safe_tool")
			}
			if tt.expectAskTool {
				require.Contains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "approval-only"), "jira__ask_tool")
			} else {
				require.NotContains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "approval-only"), "jira__ask_tool")
			}
		})
	}
}

func TestUserMCPPreferenceContextOptionsNormalizesDisabledServersBeforeBuild(t *testing.T) {
	origin := "https://mcp.example.com"
	mmClient := mocks.NewMockClient(t)
	mmClient.On("KVGet", "user_tool_providers_user-id", mock.AnythingOfType("*mcp.UserToolProviderPreferences")).
		Run(func(args mock.Arguments) {
			prefs := args.Get(1).(*mcp.UserToolProviderPreferences)
			prefs.DisabledServers = []string{"  " + origin + "/  "}
		}).
		Return(nil).
		Once()

	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: &llmcontext.Builder{},
	}

	opts := c.userMCPPreferenceContextOptions("user-id", "Failed to load user tool preferences")
	require.Len(t, opts, 1)

	llmContext := &llm.Context{}
	opts[0](llmContext)

	require.Equal(t, []string{origin}, llmContext.ToolCatalog.DisabledMCPServerOrigins)
}
