// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
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

func (p *countingMCPToolProvider) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	atomic.AddInt32(&p.calls, 1)
	return append([]llm.Tool(nil), p.tools...), nil
}

func (p *countingMCPToolProvider) Calls() int {
	return int(atomic.LoadInt32(&p.calls))
}

func newSingleBuildLLMContextBuilder(t *testing.T, mcpProvider llmcontext.MCPToolProvider) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
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

	llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "", "")
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
	builder := newSingleBuildLLMContextBuilder(t, provider)

	c := &Conversations{contextBuilder: builder}
	bot := loadedStateBot(nil)
	user := &model.User{Id: "user-id", Username: "user"}
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "", "Failed to load user tool preferences")
	require.NotNil(t, llmCtx)
	require.Equal(t, 1, provider.Calls(), "DM build should call GetToolsForUser exactly once")

	c.contextBuilder.AttachConversationID(llmCtx, bot, "conv-2")
	require.Equal(t, "conv-2", llmCtx.ConversationID)
	require.Equal(t, 1, provider.Calls(), "AttachConversationID must not trigger a second GetToolsForUser pass on the DM path")
}

// TestAttachConversationID_DoesNotMaterializeTools pins that AttachConversationID
// is a pure late-bind of the conversation ID. Restoration of dynamically loaded
// MCP tools is the caller's responsibility (via Tools.LoadMCPTools) and is
// driven from retained turns, not from anything AttachConversationID does.
func TestAttachConversationID_DoesNotMaterializeTools(t *testing.T) {
	const convID = "conv-3"
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

	llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "", "Failed to load user tool preferences")
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)

	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"strict registry must not surface dynamic MCP tools until they are restored")
	require.True(t, llmCtx.Tools.IsUnloadedMCPTool("jira__get_issue"),
		"dynamic MCP tools must appear as unloaded before restoration")

	c.contextBuilder.AttachConversationID(llmCtx, bot, convID)
	require.Equal(t, convID, llmCtx.ConversationID)
	require.Equal(t, 1, provider.Calls(), "AttachConversationID must not trigger a second GetToolsForUser pass")
	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"AttachConversationID is a thin late-bind and must not materialize MCP tools")
	require.True(t, llmCtx.Tools.IsUnloadedMCPTool("jira__get_issue"),
		"AttachConversationID must not touch the unloaded-set bookkeeping")
}

// TestAttachConversationID_DoesNotReintroducePreFilteredMCPServers pins that the
// DM handler does not need a second RemoveToolsByServerOrigin pass after
// AttachConversationID. buildConversationContextWithTools removes tools whose
// ServerOrigin is in DisabledMCPServerOrigins for DM/group channels; nothing
// between that build and AttachConversationID (or AttachConversationID itself)
// is allowed to re-introduce them.
func TestAttachConversationID_DoesNotReintroducePreFilteredMCPServers(t *testing.T) {
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
		"",
		builder.WithLLMContextDisabledMCPServers([]string{disabledOrigin}),
	)
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)
	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"buildConversationContextWithTools must drop tools from disabled MCP servers for DM/group channels")

	c.contextBuilder.AttachConversationID(llmCtx, bot, "conv-4")
	require.Equal(t, "conv-4", llmCtx.ConversationID)
	require.Nil(t, llmCtx.Tools.GetTool("jira__get_issue"),
		"AttachConversationID must not re-introduce tools from disabled MCP servers")
}
