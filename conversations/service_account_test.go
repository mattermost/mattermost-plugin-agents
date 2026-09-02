// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	serviceAccountRemoteOrigin = "https://jira.example.com"
	serviceAccountBotUserID    = "bot-user-id"
)

// serviceAccountTestBot returns an agent without dynamic MCP tool loading, so provided tools resolve immediately.
func serviceAccountTestBot(useServiceAccount bool) *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
			UseServiceAccountAuth: useServiceAccount,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: serviceAccountBotUserID, Username: "matty", DisplayName: "Matty"},
		&loadedStateLLM{},
	)
}

// The human initiator approves, but execution resolves against the re-derived SA catalog.
func TestHandleToolCallExecutesFromServiceAccountCatalog(t *testing.T) {
	executed := 0
	saTool := channelFollowUpTestMCPTool("sa_jira__get_issue", serviceAccountRemoteOrigin, "service account Jira")
	saTool.Resolver = func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
		executed++
		return "mcp:sa_jira__get_issue", nil
	}
	provider := &countingMCPToolProvider{
		// A different Jira tool, so the assertions cannot pass by resolving the wrong catalog.
		tools:   []llm.Tool{channelFollowUpTestMCPTool("jira__get_issue", serviceAccountRemoteOrigin, "user OAuth Jira")},
		saTools: []llm.Tool{saTool},
	}

	convStore := newLoadedStateFlowStore()
	conv := &store.Conversation{
		ID:           "conv-id",
		UserID:       "user-id",
		BotID:        serviceAccountBotUserID,
		SystemPrompt: "system",
		Operation:    "conversation",
	}
	require.NoError(t, convStore.CreateConversation(conv))

	blocks := []conversation.ContentBlock{{
		Type:         conversation.BlockTypeToolUse,
		ID:           "tool-use-1",
		Name:         "sa_jira__get_issue",
		ServerOrigin: serviceAccountRemoteOrigin,
		Input:        json.RawMessage(`{}`),
		Status:       conversation.StatusPending,
	}}
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	approvalPostID := "approval-post-id"
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "assistant-turn",
		ConversationID: conv.ID,
		PostID:         &approvalPostID,
		Role:           "assistant",
		Content:        content,
		Sequence:       1,
	}))

	c := serviceAccountConversations(t, convStore, provider)

	approvalPost := &model.Post{Id: approvalPostID, UserId: serviceAccountBotUserID}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}, nil))

	require.Equal(t, []string{serviceAccountBotUserID}, provider.SAIdentities(),
		"the approval resume must re-derive the catalog for the agent bot identity")
	require.Equal(t, []string{"user-id"}, provider.SAInvokers(),
		"embedded/plugin identity on the SA catalog is the initiator")
	require.Equal(t, 0, provider.Calls(), "service account agents never build the per-user remotes catalog")
	require.Equal(t, 1, executed, "the service account resolver must run exactly once")

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)

	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
	require.Equal(t, conversation.StatusSuccess, updatedBlocks[0].Status)

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
	require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
	require.Equal(t, "mcp:sa_jira__get_issue", resultBlocks[0].Content)
}

func serviceAccountConversations(t *testing.T, convStore *loadedStateFlowStore, provider llmcontext.MCPToolProvider) *Conversations {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := toolLicenseChecker(t, true)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{serviceAccountTestBot(true)})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Maybe()

	return &Conversations{
		mmClient:       mmClient,
		contextBuilder: newSingleBuildLLMContextBuilder(t, provider),
		bots:           botsService,
		licenseChecker: licenseChecker,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}
}
