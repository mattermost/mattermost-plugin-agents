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
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
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

// serviceAccountTestBuilder's license state gates SA mode (it inherits the remote-MCP license gate).
func serviceAccountTestBuilder(t *testing.T, licensed bool, provider llmcontext.MCPToolProvider) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, licensed)
	mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]interface{}, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&channelFollowUpTestToolProvider{},
		provider,
		&channelFollowUpTestConfig{},
	)
}

func TestBuildConversationContextSkipsUserPrefsForServiceAccountAgents(t *testing.T) {
	tests := []struct {
		name                string
		serviceAccount      bool
		licensed            bool
		wantPrefsLoaded     bool
		wantDisabledOrigins []string
		wantVisibleTools    []string
	}{
		{
			name:                "normal agent applies per-user server preferences",
			licensed:            true,
			wantPrefsLoaded:     true,
			wantDisabledOrigins: []string{serviceAccountRemoteOrigin},
			wantVisibleTools:    []string{"mattermost__read_channel"},
		},
		{
			name:             "service account agent ignores per-user server preferences",
			serviceAccount:   true,
			licensed:         true,
			wantVisibleTools: []string{"sa_jira__get_issue", "mattermost__read_channel"},
		},
		{
			name:                "unlicensed service account agent applies them like a normal agent",
			serviceAccount:      true,
			wantPrefsLoaded:     true,
			wantDisabledOrigins: []string{serviceAccountRemoteOrigin},
			wantVisibleTools:    []string{"mattermost__read_channel"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := &countingMCPToolProvider{
				tools: []llm.Tool{
					channelFollowUpTestMCPTool("jira__get_issue", serviceAccountRemoteOrigin, "fetch Jira issue"),
					channelFollowUpTestMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
				},
				saTools: []llm.Tool{
					channelFollowUpTestMCPTool("sa_jira__get_issue", serviceAccountRemoteOrigin, "service account Jira"),
					channelFollowUpTestMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
				},
			}

			mmClient := mocks.NewMockClient(t)
			if tc.wantPrefsLoaded {
				mmClient.On("KVGet", "user_tool_providers_user-id", mock.AnythingOfType("*mcp.UserToolProviderPreferences")).
					Run(func(args mock.Arguments) {
						prefs := args.Get(1).(*mcp.UserToolProviderPreferences)
						prefs.DisabledServers = []string{serviceAccountRemoteOrigin}
					}).
					Return(nil).
					Once()
			}

			c := &Conversations{
				mmClient:       mmClient,
				contextBuilder: serviceAccountTestBuilder(t, tc.licensed, provider),
			}
			user := &model.User{Id: "user-id", Username: "user"}
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: serviceAccountBotUserID + "__user-id"}

			llmCtx := c.buildConversationContextWithTools(
				context.Background(),
				serviceAccountTestBot(tc.serviceAccount), user, channel,
				"Failed to load user tool preferences",
			)

			require.NotNil(t, llmCtx)
			require.ElementsMatch(t, tc.wantDisabledOrigins, llmCtx.ToolCatalog.DisabledMCPServerOrigins)

			names := make([]string, 0)
			for _, tool := range llmCtx.Tools.GetTools() {
				names = append(names, tool.Name)
			}
			require.ElementsMatch(t, tc.wantVisibleTools, names)
		})
	}
}

// The human initiator approves, but execution resolves against the re-derived SA catalog.
func TestHandleToolCallExecutesFromServiceAccountCatalog(t *testing.T) {
	tests := []struct {
		name         string
		callerID     string
		toolName     string
		wantErr      error
		wantExecuted bool
		wantStatus   string
		wantResult   string
	}{
		{
			name:         "approved tool executes from the service account catalog",
			callerID:     "user-id",
			toolName:     "sa_jira__get_issue",
			wantExecuted: true,
			wantStatus:   conversation.StatusSuccess,
			wantResult:   "mcp:sa_jira__get_issue",
		},
		{
			name:     "only the conversation requester can approve",
			callerID: "other-user-id",
			toolName: "sa_jira__get_issue",
			wantErr:  ErrNotRequester,
		},
		{
			name:       "tool absent from the service account catalog fails closed",
			callerID:   "user-id",
			toolName:   "jira__get_issue",
			wantStatus: conversation.StatusError,
			wantResult: "tool jira__get_issue is no longer available",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executed := 0
			saTool := channelFollowUpTestMCPTool("sa_jira__get_issue", serviceAccountRemoteOrigin, "service account Jira")
			saTool.Resolver = func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				executed++
				return "mcp:sa_jira__get_issue", nil
			}
			provider := &countingMCPToolProvider{
				// A different Jira tool, so fail-closed cannot pass by resolving the wrong catalog.
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
				Name:         tc.toolName,
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

			err = c.HandleToolCall(context.Background(), tc.callerID, approvalPost, channel, []string{"tool-use-1"}, nil)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, 0, executed, "a non-requester must not trigger tool execution")
				require.Len(t, turns, 1, "the pending decision must be left untouched")
				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{serviceAccountBotUserID}, provider.SAIdentities(),
				"the approval resume must re-derive the catalog for the agent bot identity")
			require.Equal(t, 0, provider.Calls(), "service account agents never build the per-user catalog")
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			require.Equal(t, tc.wantStatus, updatedBlocks[0].Status)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			require.Equal(t, tc.wantResult, resultBlocks[0].Content)

			if tc.wantExecuted {
				require.Equal(t, 1, executed, "the service account resolver must run exactly once")
			} else {
				require.Equal(t, 0, executed)
			}
		})
	}
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
		contextBuilder: serviceAccountTestBuilder(t, true, provider),
		bots:           botsService,
		licenseChecker: licenseChecker,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}
}
