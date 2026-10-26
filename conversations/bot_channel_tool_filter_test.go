// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
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

func (p *channelFollowUpTestMCPToolProvider) GetToolsWithSelection(_ context.Context, _ mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	// Mirror the real client manager's contract: servers outside the selection
	// (for example remote servers on an unlicensed installation) are never
	// contacted, so their tools never appear in the result.
	var tools []llm.Tool
	for _, tool := range p.tools {
		if selection.Allows(tool.ServerOrigin) {
			tools = append(tools, tool)
		}
	}
	return tools, nil
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
	return newChannelFollowUpTestBuilderWithLicense(t, mcpTools, config, true)
}

// newChannelFollowUpTestBuilderWithLicense builds a context builder whose
// license state controls remote MCP tool supply: unlicensed builders drop
// remote-origin tools before they reach the LLM context.
func newChannelFollowUpTestBuilderWithLicense(t *testing.T, mcpTools []llm.Tool, config *channelFollowUpTestConfig, licensed bool) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, licensed)
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
	require.Empty(t, llmContext.DisabledTools, "removed tools must not be listed to the model")
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
	require.Empty(t, llmContext.DisabledTools, "removed tools must not be listed to the model")
}

// TestBotChannelAutoEverywhereKeepToolAutoExecuteBuiltIn pins that
// auto-execute built-ins survive the strict bot-channel filter like
// meta-tools do, while a plain built-in and an MCP tool carrying the flag are
// still removed.
func TestBotChannelAutoEverywhereKeepToolAutoExecuteBuiltIn(t *testing.T) {
	cases := []struct {
		name string
		tool llm.Tool
		want bool
	}{
		{name: "auto-execute built-in kept", tool: llm.Tool{Name: "CreateFile", AutoExecute: true}, want: true},
		{name: "plain built-in removed", tool: llm.Tool{Name: "builtin"}, want: false},
		{name: "MCP tool with flag not kept by the flag", tool: llm.Tool{Name: "mcp_tool", ServerOrigin: "https://mcp.example.com", AutoExecute: true}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, botChannelAutoEverywhereKeepTool(mapPolicyChecker{}, tc.tool))
		})
	}
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

// TestChannelFollowUpStrictRegistry pins which catalog a resumed run gets:
// a channel run whose initiator cannot be confirmed as a present human is
// limited to auto_run_everywhere tools, failing to look the initiator up is
// an error, and DMs are never limited, whoever started the run.
func TestChannelFollowUpStrictRegistry(t *testing.T) {
	const origin = "https://mcp.atlassian.com"

	channelPolicyChecker := mapPolicyChecker{
		origin: {
			"safe_tool": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			"ask_tool":  {policy: mcp.ToolPolicyAsk, enabled: true},
		},
	}
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}
	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__" + testRequesterID}
	activateAIPost := &model.Post{Id: "initiator-post", UserId: "bot-user"}
	activateAIPost.AddProp(ActivateAIProp, true)

	tests := []struct {
		name          string
		channel       *model.Channel
		postID        string
		setupMMClient func(mmClient *mocks.MockClient)
		expectAskTool bool
		wantErr       bool
	}{
		{
			name:    "initiating post lookup failure is an error",
			channel: channel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return((*model.Post)(nil), errors.New("database unavailable")).Once()
			},
			wantErr: true,
		},
		{
			name:    "initiating user lookup failure is an error",
			channel: channel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return(&model.Post{Id: "initiator-post", UserId: "root-user"}, nil).Once()
				mmClient.On("GetUser", "root-user").Return((*model.User)(nil), errors.New("database unavailable")).Once()
			},
			wantErr: true,
		},
		{
			name:    "deleted initiating post is limited to auto everywhere",
			channel: channel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return((*model.Post)(nil), pluginapi.ErrNotFound).Once()
			},
			expectAskTool: false,
		},
		{
			name:          "post outside the conversation fails closed",
			channel:       channel,
			postID:        "unknown-post",
			expectAskTool: false,
		},
		{
			name:    "bot activate_ai initiator is limited to auto everywhere",
			channel: channel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return(activateAIPost, nil).Once()
				mmClient.On("GetUser", "bot-user").Return(&model.User{Id: "bot-user", IsBot: true}, nil).Once()
			},
			expectAskTool: false,
		},
		{
			name:    "human initiator keeps approval tools",
			channel: channel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return(&model.Post{Id: "initiator-post", UserId: "human-user"}, nil).Once()
				mmClient.On("GetUser", "human-user").Return(&model.User{Id: "human-user"}, nil).Once()
			},
			expectAskTool: true,
		},
		{
			name:    "DM is not limited",
			channel: dmChannel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return(&model.Post{Id: "initiator-post", UserId: testRequesterID}, nil).Maybe()
				mmClient.On("GetUser", testRequesterID).Return(&model.User{Id: testRequesterID}, nil).Maybe()
			},
			expectAskTool: true,
		},
		{
			name:    "DM started by a bot activate_ai is not limited",
			channel: dmChannel,
			postID:  "bot-post",
			setupMMClient: func(mmClient *mocks.MockClient) {
				mmClient.On("GetPost", "initiator-post").Return(activateAIPost, nil).Maybe()
				mmClient.On("GetUser", "bot-user").Return(&model.User{Id: "bot-user", IsBot: true}, nil).Maybe()
			},
			expectAskTool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			initiatorPostID := "initiator-post"
			botPostID := "bot-post"
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "user-turn", ConversationID: conv.ID, PostID: &initiatorPostID, Role: "user", Sequence: 0}))
			require.NoError(t, convStore.CreateTurn(&store.Turn{ID: "bot-turn", ConversationID: conv.ID, PostID: &botPostID, Role: "assistant", Sequence: 1}))

			mmClient := mocks.NewMockClient(t)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Return(nil).Maybe()
			if tt.setupMMClient != nil {
				tt.setupMMClient(mmClient)
			}

			config := &channelFollowUpTestConfig{enableChannelMentionToolCalling: true}
			bot := loadedStateBot(nil)
			c := &Conversations{
				mmClient: mmClient,
				contextBuilder: newChannelFollowUpTestBuilder(t, []llm.Tool{
					channelFollowUpTestMCPTool("jira__safe_tool", origin, "auto-everywhere safe channel follow-up capability"),
					channelFollowUpTestMCPTool("jira__ask_tool", origin, "approval-only channel follow-up capability"),
				}, config),
				bots:              passthroughBotsService(bot),
				licenseChecker:    enterprisetest.CheckerAt(enterprise.LevelEnterprise),
				convService:       conversation.NewService(convStore, nil, nil, nil),
				configProvider:    config,
				toolPolicyChecker: channelPolicyChecker,
			}

			policy, err := c.resolveTurnPolicy(context.Background(), turnRequest{
				bot:     bot,
				user:    &model.User{Id: testRequesterID, Username: "user", Locale: "en"},
				channel: tt.channel,
				conv:    conv,
				post:    &model.Post{Id: tt.postID, UserId: "bot-id"},
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			llmContext, err := policy.buildContext(context.Background(), "Failed to load user tool preferences")
			require.NoError(t, err)

			require.Contains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "safe"), "jira__safe_tool")
			if tt.expectAskTool {
				require.Contains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "approval-only"), "jira__ask_tool")
			} else {
				require.NotContains(t, channelFollowUpSearchToolNames(t, llmContext.Tools, "approval-only"), "jira__ask_tool")
			}
		})
	}
}

// A channel mention with tool calling off sends no tool definitions to the
// provider, so the system prompt must not tell the model to call the dynamic
// MCP meta-tools; models otherwise write the call out as plain text.
func TestChannelMentionSystemPromptDynamicToolWorkflow(t *testing.T) {
	const workflowInstruction = "call search_tools"
	const dmOnlyNotice = "can only be used in a Direct Message (DM) or via the Agents tab"

	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	tests := []struct {
		name                string
		channelType         model.ChannelType
		allowToolsInChannel bool
		wantToolsDisabled   bool
	}{
		{name: "channel with tool calling disabled", channelType: model.ChannelTypeOpen, wantToolsDisabled: true},
		{name: "channel with tool calling enabled", channelType: model.ChannelTypeOpen, allowToolsInChannel: true},
		{name: "direct message", channelType: model.ChannelTypeDirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newChannelFollowUpTestBuilder(t, []llm.Tool{
				channelFollowUpTestMCPTool("fusion__create_geojson", "https://fusion.example.com", "Build a GeoJSON document"),
			}, &channelFollowUpTestConfig{enableChannelMentionToolCalling: tt.allowToolsInChannel})
			isDM := tt.channelType == model.ChannelTypeDirect
			policy := &turnPolicy{
				c:            &Conversations{contextBuilder: builder, toolPolicyChecker: mapPolicyChecker{}},
				bot:          channelFollowUpTestBot(),
				user:         &model.User{Id: testRequesterID, Username: "user", Locale: "en"},
				channel:      &model.Channel{Id: "channel-id", TeamId: "team-id", Type: tt.channelType},
				isDM:         isDM,
				toolsEnabled: isDM || tt.allowToolsInChannel,
			}
			llmContext, err := policy.buildContext(context.Background(), "")
			require.NoError(t, err)
			require.NotNil(t, llmContext.Tools.GetTool(mcp.SearchToolsName))

			systemPrompt, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, llmContext)
			require.NoError(t, err)

			if tt.wantToolsDisabled {
				require.NotContains(t, systemPrompt, workflowInstruction)
				require.Contains(t, systemPrompt, dmOnlyNotice)
			} else {
				require.Contains(t, systemPrompt, workflowInstruction)
				require.NotContains(t, systemPrompt, dmOnlyNotice)
			}
		})
	}
}

func TestUserMCPPreferenceContextOptionsNormalizesDisabledServersBeforeBuild(t *testing.T) {
	origin := "https://mcp.example.com"
	mmClient := mocks.NewMockClient(t)
	mmClient.On("KVGet", "user_tool_providers_"+testRequesterID, mock.AnythingOfType("*mcp.UserToolProviderPreferences")).
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

	opts := c.userMCPPreferenceContextOptions(testRequesterID, "Failed to load user tool preferences")
	require.Len(t, opts, 1)

	llmContext := &llm.Context{}
	opts[0](llmContext)

	require.Equal(t, []string{origin}, llmContext.ToolCatalog.DisabledMCPServerOrigins)
}
