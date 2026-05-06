// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// countingMCPToolProvider counts how many times GetToolsForUser is invoked,
// so single-build refactors can assert there is no second pipeline pass per
// message.
type countingMCPToolProvider struct {
	calls int32
	tools []llm.Tool
}

func (p *countingMCPToolProvider) GetToolsForUser(string) ([]llm.Tool, *mcp.Errors) {
	atomic.AddInt32(&p.calls, 1)
	return append([]llm.Tool(nil), p.tools...), nil
}

func (p *countingMCPToolProvider) Calls() int {
	return int(atomic.LoadInt32(&p.calls))
}

func newSingleBuildLLMContextBuilder(t *testing.T, mcpProvider llmcontext.MCPToolProvider, loadedStore llmcontext.LoadedMCPToolStore) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()

	builder := llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&channelFollowUpTestToolProvider{},
		mcpProvider,
		&channelFollowUpTestConfig{},
	)
	if loadedStore != nil {
		builder.SetLoadedMCPToolStore(loadedStore)
	}
	return builder
}

// TestBuildConversationContextWithTools_MentionShapeBuildsOnce asserts that the
// shared helper used by the mention path performs a single GetToolsForUser
// pass even when the caller has not yet learned the conversation ID, and
// AttachConversationID does NOT trigger another pass.
func TestBuildConversationContextWithTools_MentionShapeBuildsOnce(t *testing.T) {
	provider := &countingMCPToolProvider{tools: []llm.Tool{
		{
			Name:         "jira__get_issue",
			Description:  "fetch Jira issue details",
			ServerOrigin: "https://jira.example.com",
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		},
	}}
	loadedStore := &loadedStateStore{}
	builder := newSingleBuildLLMContextBuilder(t, provider, loadedStore)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen}

	llmCtx := c.buildConversationContextWithTools(bot, user, channel, "", "")
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)
	require.Equal(t, 1, provider.Calls(), "initial build should call GetToolsForUser exactly once")

	c.contextBuilder.AttachConversationID(llmCtx, bot, "conv-1")
	require.Equal(t, "conv-1", llmCtx.ConversationID)
	require.Equal(t, 1, provider.Calls(), "AttachConversationID must not trigger a second GetToolsForUser pass")
}

// TestBuildConversationContextWithTools_DMShapeBuildsOnce mirrors the DM path:
// the helper applies user MCP preferences (DM/group), builds tools once with
// no ConversationID, and the AttachConversationID step does not re-run the
// MCP pipeline.
func TestBuildConversationContextWithTools_DMShapeBuildsOnce(t *testing.T) {
	provider := &countingMCPToolProvider{}
	builder := newSingleBuildLLMContextBuilder(t, provider, nil)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	llmCtx := c.buildConversationContextWithTools(bot, user, channel, "", "Failed to load user tool preferences")
	require.NotNil(t, llmCtx)
	require.Equal(t, 1, provider.Calls(), "DM build should call GetToolsForUser exactly once")

	c.contextBuilder.AttachConversationID(llmCtx, bot, "conv-2")
	require.Equal(t, "conv-2", llmCtx.ConversationID)
	require.Equal(t, 1, provider.Calls(), "AttachConversationID must not trigger a second GetToolsForUser pass on the DM path")
}

// TestAttachConversationID_RestoresLoadedToolsAfterLateBinding ensures that
// loaded MCP tools are surfaced via the late-bound conversation ID without
// rebuilding the registry, including the unloaded-set bookkeeping (the
// restored tool must no longer appear as unloaded).
func TestAttachConversationID_RestoresLoadedToolsAfterLateBinding(t *testing.T) {
	const convID = "conv-attach"
	provider := &countingMCPToolProvider{tools: []llm.Tool{
		{
			Name:         "jira__get_issue",
			Description:  "fetch Jira issue details",
			ServerOrigin: "https://jira.example.com",
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "ok", nil
			},
		},
	}}
	loadedStore := &loadedStateStore{rows: []store.LoadedMCPTool{
		{ConversationID: convID, BotID: "bot-id", UserID: "user-id", ToolName: "jira__get_issue"},
	}}
	builder := newSingleBuildLLMContextBuilder(t, provider, loadedStore)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	llmCtx := c.buildConversationContextWithTools(bot, user, channel, "", "Failed to load user tool preferences")
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)

	// Before late-binding, the loaded MCP tool is not yet visible because the
	// conversation does not exist; restoreLoadedMCPTools no-ops on the empty
	// ConversationID. The tool is recorded as unloaded.
	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"))
	require.True(t, llmCtx.Tools.IsUnloadedMCPTool("jira__get_issue"),
		"prior to AttachConversationID, the loaded tool should still appear as unloaded")

	c.contextBuilder.AttachConversationID(llmCtx, bot, convID)
	require.Equal(t, convID, llmCtx.ConversationID)
	require.Equal(t, 1, provider.Calls(), "registry replay must not re-fetch MCP tools")

	// After late-binding, the persisted loaded tool is surfaced and removed
	// from the unloaded set.
	require.NotNil(t, llmCtx.Tools.GetTool("jira__get_issue"))
	require.False(t, llmCtx.Tools.IsUnloadedMCPTool("jira__get_issue"),
		"AttachConversationID must drop restored tools from the unloaded set")
}
