// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// countingMCPToolProvider counts how many times the MCP GetTools catalog build is invoked,
// so single-build refactors can assert there is no second pipeline pass per
// message.
type countingMCPToolProvider struct {
	calls   int32
	tools   []llm.Tool
	saTools []llm.Tool

	mu           sync.Mutex
	saIdentities []string
	saInvokers   []string
}

func (p *countingMCPToolProvider) GetTools(_ context.Context, req mcp.CatalogRequest) ([]llm.Tool, *mcp.Errors) {
	if req.ServiceAccount {
		p.mu.Lock()
		p.saIdentities = append(p.saIdentities, req.RemoteOwnerID)
		p.saInvokers = append(p.saInvokers, req.InvokingUserID)
		p.mu.Unlock()
		return append([]llm.Tool(nil), p.saTools...), nil
	}
	atomic.AddInt32(&p.calls, 1)
	return append([]llm.Tool(nil), p.tools...), nil
}

func (p *countingMCPToolProvider) Calls() int {
	return int(atomic.LoadInt32(&p.calls))
}

func (p *countingMCPToolProvider) SAIdentities() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.saIdentities...)
}

func (p *countingMCPToolProvider) SAInvokers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.saInvokers...)
}

func newSingleBuildLLMContextBuilder(t *testing.T, mcpProvider llmcontext.MCPToolProvider) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&channelFollowUpTestToolProvider{},
		mcpProvider,
		&channelFollowUpTestConfig{},
	)
}

// TestBuildConversationContextWithTools_MentionShapeBuildsOnce asserts that the
// shared helper used by the mention path performs a single MCP GetTools pass.
func TestBuildConversationContextWithTools_MentionShapeBuildsOnce(t *testing.T) {
	provider := &countingMCPToolProvider{tools: []llm.Tool{
		{
			Name:         "jira__get_issue",
			Description:  "fetch Jira issue details",
			ServerOrigin: "https://jira.example.com",
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		},
	}}
	builder := newSingleBuildLLMContextBuilder(t, provider)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen}

	llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "")
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)
	require.Equal(t, 1, provider.Calls(), "initial build should call MCP GetTools exactly once")
}

// TestBuildConversationContextWithTools_DMShapeBuildsOnce mirrors the DM path:
// the helper applies user MCP preferences (DM/group) and builds tools once.
// Service account agents share one catalog, so their preferences never apply.
func TestBuildConversationContextWithTools_DMShapeBuildsOnce(t *testing.T) {
	const disabledOrigin = "https://jira.example.com"

	tests := []struct {
		name                string
		bot                 *bots.Bot
		wantPrefsLoaded     bool
		wantUserCalls       int
		wantSAIdentities    []string
		wantDisabledOrigins []string
	}{
		{
			name:                "normal agent applies per-user server preferences",
			bot:                 loadedStateBot(nil),
			wantPrefsLoaded:     true,
			wantUserCalls:       1,
			wantDisabledOrigins: []string{disabledOrigin},
		},
		{
			name:             "service account agent ignores per-user server preferences",
			bot:              serviceAccountTestBot(true),
			wantSAIdentities: []string{serviceAccountBotUserID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := &countingMCPToolProvider{}

			// A missing expectation fails the test if the preferences are read anyway.
			mmClient := mocks.NewMockClient(t)
			if tc.wantPrefsLoaded {
				mmClient.On("KVGet", "user_tool_providers_user-id", mock.AnythingOfType("*mcp.UserToolProviderPreferences")).
					Run(func(args mock.Arguments) {
						prefs := args.Get(1).(*mcp.UserToolProviderPreferences)
						prefs.DisabledServers = []string{disabledOrigin}
					}).
					Return(nil).
					Once()
			}

			c := &Conversations{
				mmClient:       mmClient,
				contextBuilder: newSingleBuildLLMContextBuilder(t, provider),
			}
			user := &model.User{Id: "user-id", Username: "user"}
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

			llmCtx := c.buildConversationContextWithTools(context.Background(), tc.bot, user, channel, "Failed to load user tool preferences")
			require.NotNil(t, llmCtx)
			require.Equal(t, tc.wantUserCalls, provider.Calls(), "per-user catalog builds")
			require.Equal(t, tc.wantSAIdentities, provider.SAIdentities(), "service account catalog builds")
			require.ElementsMatch(t, tc.wantDisabledOrigins, llmCtx.ToolCatalog.DisabledMCPServerOrigins)
		})
	}
}

// TestBuildConversationContextWithTools_DoesNotMaterializeDynamicMCPTools pins
// that the normal build path leaves dynamically loaded MCP tools unloaded.
// Restoration is the caller's responsibility (via Tools.LoadMCPTools), driven
// from retained turns.
func TestBuildConversationContextWithTools_DoesNotMaterializeDynamicMCPTools(t *testing.T) {
	provider := &countingMCPToolProvider{tools: []llm.Tool{
		{
			Name:         "jira__get_issue",
			Description:  "fetch Jira issue details",
			ServerOrigin: "https://jira.example.com",
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		},
	}}
	builder := newSingleBuildLLMContextBuilder(t, provider)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "Failed to load user tool preferences")
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)

	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"strict registry must not surface dynamic MCP tools until they are restored")
	require.True(t, llmCtx.Tools.IsUnloadedMCPTool("jira__get_issue"),
		"dynamic MCP tools must appear as unloaded before restoration")
	require.Equal(t, 1, provider.Calls(), "build should call MCP GetTools exactly once")
}

// TestBuildConversationContextWithTools_DropsPreFilteredMCPServers pins that
// buildConversationContextWithTools removes tools whose ServerOrigin is in
// DisabledMCPServerOrigins for DM/group channels.
func TestBuildConversationContextWithTools_DropsPreFilteredMCPServers(t *testing.T) {
	const disabledOrigin = "https://jira.example.com"
	provider := &countingMCPToolProvider{tools: []llm.Tool{
		{
			Name:         "jira__get_issue",
			Description:  "fetch Jira issue details",
			ServerOrigin: disabledOrigin,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		},
	}}
	builder := newSingleBuildLLMContextBuilder(t, provider)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	llmCtx := c.buildConversationContextWithTools(
		context.Background(),
		bot, user, channel,
		"",
		builder.WithLLMContextDisabledMCPServers([]string{disabledOrigin}),
	)
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)
	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"buildConversationContextWithTools must drop tools from disabled MCP servers for DM/group channels")
}
