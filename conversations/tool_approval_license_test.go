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
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
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

const toolLicenseRemoteOrigin = "https://jira.example.com"

// mockLicenseState registers GetConfig/GetLicense expectations reflecting the
// requested license state (enterprise-licensed or unlicensed).
func mockLicenseState(mockAPI *plugintest.API, licensed bool) {
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	if licensed {
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	} else {
		mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
	}
}

// toolLicenseChecker returns a LicenseChecker whose IsBasicsLicensed reports
// the requested state.
func toolLicenseChecker(t *testing.T, licensed bool) *enterprise.LicenseChecker {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, licensed)
	return enterprise.NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
}

type toolLicenseBuiltinProvider struct {
	tools []llm.Tool
}

func (p *toolLicenseBuiltinProvider) GetTools(*bots.Bot, *llm.Context) []llm.Tool {
	return p.tools
}

// toolLicenseTestBot returns a bot without dynamic MCP tool loading so every
// provided tool is immediately resolvable in the visible tool store.
func toolLicenseTestBot(lm llm.LanguageModel) *bots.Bot {
	if lm == nil {
		lm = &loadedStateLLM{}
	}
	return bots.NewBot(
		llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		lm,
	)
}

// toolLicenseTestBuilder builds a context builder whose license state matches
// the scenario under test: unlicensed builders drop remote MCP tools at
// supply time, mirroring production.
func toolLicenseTestBuilder(t *testing.T, licensed bool) *llmcontext.Builder {
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
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}

	builtinTools := []llm.Tool{channelFollowUpTestMCPTool("builtin_tool", "", "built-in tool")}
	mcpTools := []llm.Tool{
		channelFollowUpTestMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
		channelFollowUpTestMCPTool("jira__get_issue", toolLicenseRemoteOrigin, "fetch Jira issue"),
	}

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&toolLicenseBuiltinProvider{tools: builtinTools},
		&channelFollowUpTestMCPToolProvider{tools: mcpTools},
		&channelFollowUpTestConfig{},
	)
}

func toolLicenseConversations(t *testing.T, convStore *loadedStateFlowStore, licensed bool) (*Conversations, *loadedStateLLM, *loadedStateStreamingService) {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := toolLicenseChecker(t, licensed)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	lm := &loadedStateLLM{}
	streamingService := &loadedStateStreamingService{}
	botsService.SetBotsForTesting([]*bots.Bot{toolLicenseTestBot(lm)})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Maybe()
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})

	return &Conversations{
		mmClient:         mmClient,
		contextBuilder:   toolLicenseTestBuilder(t, licensed),
		bots:             botsService,
		licenseChecker:   licenseChecker,
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}, lm, streamingService
}

// TestHandleToolCallLicenseGate pins the license split for tool approvals:
// built-in tools (empty ServerOrigin) and embedded Mattermost MCP tools
// (mcp.EmbeddedClientKey) never require a license, while tools from remote
// MCP servers require one to execute. Rejections never require a license.
func TestHandleToolCallLicenseGate(t *testing.T) {
	tests := []struct {
		name                  string
		toolName              string
		origin                string
		licensed              bool
		accept                bool
		wantErr               error
		wantStatus            string
		wantResult            string
		wantFollowUp          bool
		wantRejectionGuidance bool
	}{
		{
			name:       "embedded MCP tool executes without license",
			toolName:   "mattermost__read_channel",
			origin:     mcp.EmbeddedClientKey,
			licensed:   false,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:mattermost__read_channel",
		},
		{
			name:       "built-in tool executes without license",
			toolName:   "builtin_tool",
			origin:     "",
			licensed:   false,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:builtin_tool",
		},
		{
			name:     "remote MCP tool approval is rejected without license",
			toolName: "jira__get_issue",
			origin:   toolLicenseRemoteOrigin,
			licensed: false,
			accept:   true,
			wantErr:  ErrRemoteMCPNotLicensed,
		},
		{
			name:       "remote MCP tool executes with license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   true,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:jira__get_issue",
		},
		{
			name:                  "remote MCP tool rejection is allowed without license",
			toolName:              "jira__get_issue",
			origin:                toolLicenseRemoteOrigin,
			licensed:              false,
			accept:                false,
			wantStatus:            conversation.StatusRejected,
			wantResult:            toolCallRejectedByUserResult,
			wantFollowUp:          true,
			wantRejectionGuidance: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			blocks := []conversation.ContentBlock{{
				Type:         conversation.BlockTypeToolUse,
				ID:           "tool-use-1",
				Name:         tc.toolName,
				ServerOrigin: tc.origin,
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

			c, lm, streamingService := toolLicenseConversations(t, convStore, tc.licensed)

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			var acceptedIDs []string
			if tc.accept {
				acceptedIDs = []string{"tool-use-1"}
			}

			err = c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, acceptedIDs, nil)
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// The pending decision must be left untouched so the user
				// can act again once the server is licensed.
				require.Len(t, turns, 1)
				var untouched []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &untouched))
				require.Equal(t, conversation.StatusPending, untouched[0].Status)
				require.Empty(t, lm.requests)
				return
			}

			require.NoError(t, err)
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			require.Equal(t, tc.wantStatus, updatedBlocks[0].Status)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			require.Equal(t, tc.wantResult, resultBlocks[0].Content)

			if tc.wantFollowUp {
				require.Len(t, lm.requests, 1, "rejection continuation must start one follow-up")
				if tc.wantRejectionGuidance {
					requireRejectionGuidanceIsFinalUserPost(t, lm.requests[0].Posts)
				}
			} else {
				require.Empty(t, lm.requests)
			}
		})
	}
}

// TestHandleToolResultLicenseGate pins the license split for the second-stage
// share/keep-private decision: sharing output from a remote MCP tool requires
// a license, while embedded Mattermost MCP output and keep-private decisions
// do not.
func TestHandleToolResultLicenseGate(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		origin     string
		licensed   bool
		accept     bool
		wantErr    error
		wantShared bool
	}{
		{
			name:       "embedded MCP result shares without license",
			toolName:   "mattermost__read_channel",
			origin:     mcp.EmbeddedClientKey,
			licensed:   false,
			accept:     true,
			wantShared: true,
		},
		{
			name:     "remote MCP result share is rejected without license",
			toolName: "jira__get_issue",
			origin:   toolLicenseRemoteOrigin,
			licensed: false,
			accept:   true,
			wantErr:  ErrRemoteMCPNotLicensed,
		},
		{
			name:       "remote MCP result shares with license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   true,
			accept:     true,
			wantShared: true,
		},
		{
			name:       "remote MCP result keep-private is allowed without license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   false,
			accept:     false,
			wantShared: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			resultPostID := "result-post-id"
			assistantBlocks := []conversation.ContentBlock{{
				Type:         conversation.BlockTypeToolUse,
				ID:           "tool-use-1",
				Name:         tc.toolName,
				ServerOrigin: tc.origin,
				Input:        json.RawMessage(`{}`),
				Status:       conversation.StatusSuccess,
			}}
			assistantContent, err := json.Marshal(assistantBlocks)
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &resultPostID,
				Role:           "assistant",
				Content:        assistantContent,
				Sequence:       1,
			}))

			resultBlocks := []conversation.ContentBlock{{
				Type:      conversation.BlockTypeToolResult,
				ToolUseID: "tool-use-1",
				Content:   "tool output",
				Status:    conversation.StatusSuccess,
			}}
			resultContent, err := json.Marshal(resultBlocks)
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "result-turn",
				ConversationID: conv.ID,
				Role:           "tool_result",
				Content:        resultContent,
				Sequence:       2,
			}))

			c, _, _ := toolLicenseConversations(t, convStore, tc.licensed)

			resultPost := &model.Post{Id: resultPostID, UserId: "bot-id"}
			resultPost.AddProp(streaming.ConversationIDProp, conv.ID)
			// DM channel: HandleToolCall streams DM follow-ups, so
			// HandleToolResult returns after recording the decision.
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

			var acceptedIDs []string
			if tc.accept {
				acceptedIDs = []string{"tool-use-1"}
			}

			err = c.HandleToolResult(context.Background(), "user-id", resultPost, channel, acceptedIDs)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 2)
			var updatedResults []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &updatedResults))

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, updatedResults[0].DecidedAt, "rejected submission must not record a decision")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, updatedResults[0].DecidedAt, "decision must be recorded")
			if tc.wantShared {
				require.NotNil(t, updatedResults[0].Shared)
				require.True(t, *updatedResults[0].Shared)
			} else {
				require.True(t, updatedResults[0].Shared == nil || !*updatedResults[0].Shared)
			}
		})
	}
}
