// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const plantedRejectionArg = "SECRET-TOOL-ARG-do-not-share"

func TestHandleToolCallRejectionFollowsUp(t *testing.T) {
	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	openChannel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	pendingJira := func(id string, shared bool) conversation.ContentBlock {
		return conversation.ContentBlock{
			Type:   conversation.BlockTypeToolUse,
			ID:     id,
			Name:   "jira__get_issue",
			Input:  json.RawMessage(`{"issue_key":"` + plantedRejectionArg + `"}`),
			Status: conversation.StatusPending,
			Shared: conversation.BoolPtr(shared),
		}
	}

	cases := []struct {
		name               string
		channel            *model.Channel
		blocks             []conversation.ContentBlock
		acceptedIDs        []string
		failingTool        bool
		wantFollowUp       bool
		wantGuidance       bool
		wantRequestHas     []string
		wantRequestOmits   []string
		wantToolUseShared  []bool
		wantResultShared   []bool
		wantResultContents []string
	}{
		{
			name:               "lone DM rejection continues with guidance",
			channel:            dmChannel,
			blocks:             []conversation.ContentBlock{pendingJira("tool-use-1", true)},
			wantFollowUp:       true,
			wantGuidance:       true,
			wantRequestHas:     []string{llm.ToolRejectionUserMessage, "Tool call rejected by user"},
			wantToolUseShared:  []bool{true},
			wantResultShared:   []bool{true},
			wantResultContents: []string{"Tool call rejected by user"},
		},
		{
			name:               "lone channel rejection continues with visible reason and private args",
			channel:            openChannel,
			blocks:             []conversation.ContentBlock{pendingJira("tool-use-1", false)},
			wantFollowUp:       true,
			wantGuidance:       true,
			wantRequestHas:     []string{llm.ToolRejectionUserMessage, "Tool call rejected by user"},
			wantRequestOmits:   []string{plantedRejectionArg},
			wantToolUseShared:  []bool{false},
			wantResultShared:   []bool{true},
			wantResultContents: []string{"Tool call rejected by user"},
		},
		{
			name:    "mixed DM accept and reject includes rejection guidance",
			channel: dmChannel,
			blocks: []conversation.ContentBlock{
				pendingJira("tool-use-1", true),
				{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-2",
					Name:   "jira__transition_issue",
					Input:  json.RawMessage(`{"issue_key":"` + plantedRejectionArg + `"}`),
					Status: conversation.StatusPending,
					Shared: conversation.BoolPtr(true),
				},
			},
			acceptedIDs:        []string{"tool-use-1"},
			wantFollowUp:       true,
			wantGuidance:       true,
			wantRequestHas:     []string{llm.ToolRejectionUserMessage, "Tool call rejected by user", "restored-result"},
			wantToolUseShared:  []bool{true, true},
			wantResultShared:   []bool{true, true},
			wantResultContents: []string{"restored-result", "Tool call rejected by user"},
		},
		{
			name:               "execution error continues without rejection guidance",
			channel:            dmChannel,
			blocks:             []conversation.ContentBlock{pendingJira("tool-use-1", true)},
			acceptedIDs:        []string{"tool-use-1"},
			failingTool:        true,
			wantFollowUp:       true,
			wantGuidance:       false,
			wantRequestHas:     []string{"jira unavailable"},
			wantRequestOmits:   []string{llm.ToolRejectionUserMessage},
			wantToolUseShared:  []bool{true},
			wantResultShared:   []bool{true},
			wantResultContents: []string{"jira unavailable"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

			content, err := json.Marshal(tc.blocks)
			require.NoError(t, err)
			approvalPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &approvalPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       nextSeq,
			}))

			lm := &loadedStateLLM{}
			streamingService := &loadedStateStreamingService{}
			c := newRejectionFollowUpConversations(t, convStore, lm, streamingService, tc.failingTool)

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

			require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, tc.channel, tc.acceptedIDs, nil))
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.GreaterOrEqual(t, len(turns), 4)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[2].Content, &updatedBlocks))
			require.Len(t, updatedBlocks, len(tc.wantToolUseShared))
			for i, wantShared := range tc.wantToolUseShared {
				require.NotNil(t, updatedBlocks[i].Shared)
				assert.Equal(t, wantShared, *updatedBlocks[i].Shared)
			}

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[3].Content, &resultBlocks))
			require.Len(t, resultBlocks, len(tc.wantResultContents))
			for i, wantContent := range tc.wantResultContents {
				assert.Equal(t, wantContent, resultBlocks[i].Content)
				require.NotNil(t, resultBlocks[i].Shared)
				assert.Equal(t, tc.wantResultShared[i], *resultBlocks[i].Shared)
				assert.NotNil(t, resultBlocks[i].DecidedAt)
			}

			if !tc.wantFollowUp {
				assert.Empty(t, lm.requests)
				return
			}
			require.Len(t, lm.requests, 1, "expected one immediate continuation")
			requestText := completionRequestText(lm.requests[0])
			if tc.wantGuidance {
				assert.Equal(t, 1, countUserMessagesContaining(lm.requests[0].Posts, llm.ToolRejectionUserMessage),
					"rejection guidance must appear exactly once")
			} else {
				assert.Zero(t, countUserMessagesContaining(lm.requests[0].Posts, llm.ToolRejectionUserMessage),
					"execution errors must not receive rejection guidance")
			}
			for _, want := range tc.wantRequestHas {
				assert.Contains(t, requestText, want)
			}
			for _, omit := range tc.wantRequestOmits {
				assert.NotContains(t, requestText, omit)
			}
		})
	}
}

func TestHandleToolCallMixedChannelRejectionGuidanceAfterShare(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	nextSeq := 1
	seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

	blocks := []conversation.ContentBlock{
		{
			Type:   conversation.BlockTypeToolUse,
			ID:     "tool-use-1",
			Name:   "jira__get_issue",
			Input:  json.RawMessage(`{"issue_key":"MM-1"}`),
			Status: conversation.StatusPending,
			Shared: conversation.BoolPtr(false),
		},
		{
			Type:   conversation.BlockTypeToolUse,
			ID:     "tool-use-2",
			Name:   "jira__transition_issue",
			Input:  json.RawMessage(`{"issue_key":"` + plantedRejectionArg + `"}`),
			Status: conversation.StatusPending,
			Shared: conversation.BoolPtr(false),
		},
	}
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	approvalPostID := "approval-post-id"
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "assistant-turn",
		ConversationID: conv.ID,
		PostID:         &approvalPostID,
		Role:           "assistant",
		Content:        content,
		Sequence:       nextSeq,
	}))

	lm := &loadedStateLLM{}
	streamingService := &loadedStateStreamingService{}
	c := newRejectionFollowUpConversations(t, convStore, lm, streamingService, false)

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}, nil))
	assert.Empty(t, lm.requests, "mixed channel batch must wait for the share decision")

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[3].Content, &resultBlocks))
	require.Len(t, resultBlocks, 2)
	assert.Nil(t, resultBlocks[0].DecidedAt)
	assert.False(t, *resultBlocks[0].Shared)
	assert.NotNil(t, resultBlocks[1].DecidedAt)
	assert.True(t, *resultBlocks[1].Shared)
	assert.Equal(t, "Tool call rejected by user", resultBlocks[1].Content)

	require.NoError(t, c.HandleToolResult(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}))
	streamingService.waitForStreaming()

	require.Len(t, lm.requests, 1)
	requestText := completionRequestText(lm.requests[0])
	assert.Equal(t, 1, countUserMessagesContaining(lm.requests[0].Posts, llm.ToolRejectionUserMessage),
		"mixed rejection must receive the same guidance exactly once")
	assert.Contains(t, requestText, "Tool call rejected by user")
	assert.Contains(t, requestText, "restored-result")
	assert.NotContains(t, requestText, plantedRejectionArg)
}

func TestHandleToolResultRejectedOnlyDoesNotFollowUp(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	clickedPostID := "approval-post-id"

	assistantBlocks := []conversation.ContentBlock{{
		Type:   conversation.BlockTypeToolUse,
		ID:     "tool-use-1",
		Name:   "jira__get_issue",
		Input:  json.RawMessage(`{"issue_key":"` + plantedRejectionArg + `"}`),
		Status: conversation.StatusRejected,
		Shared: conversation.BoolPtr(false),
	}}
	resultBlocks := []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: "tool-use-1",
		Content:   "Tool call rejected by user",
		Status:    conversation.StatusError,
		Shared:    conversation.BoolPtr(true),
	}}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "assistant-turn",
		ConversationID: conv.ID,
		PostID:         &clickedPostID,
		Role:           "assistant",
		Content:        assistantContent,
		Sequence:       1,
	}))
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "result-turn",
		ConversationID: conv.ID,
		Role:           "tool_result",
		Content:        resultContent,
		Sequence:       2,
	}))

	lm := &loadedStateLLM{}
	streamingService := &loadedStateStreamingService{}
	c := newRejectionFollowUpConversations(t, convStore, lm, streamingService, false)

	clickedPost := &model.Post{Id: clickedPostID, UserId: "bot-id"}
	clickedPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolResult(context.Background(), "user-id", clickedPost, channel, nil))
	assert.Empty(t, lm.requests, "rejected-only share-stage must not start a follow-up")
}

func newRejectionFollowUpConversations(
	t *testing.T,
	convStore *loadedStateFlowStore,
	lm *loadedStateLLM,
	streamingService *loadedStateStreamingService,
	failingTool bool,
) *Conversations {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(lm)})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})

	tools := []llm.Tool{loadedStateTool(), loadedStateTransitionTool(nil)}
	if failingTool {
		failing := loadedStateTool()
		failing.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return "", errors.New("jira unavailable")
		}
		tools = []llm.Tool{failing}
	}

	return &Conversations{
		mmClient:         mmClient,
		contextBuilder:   newChannelFollowUpTestBuilder(t, tools, &channelFollowUpTestConfig{}),
		bots:             botsService,
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}
}

func completionRequestText(req llm.CompletionRequest) string {
	var b strings.Builder
	for _, post := range req.Posts {
		b.WriteString(post.Message)
		b.WriteByte('\n')
		for _, tc := range post.ToolUse {
			b.Write(tc.Arguments)
			b.WriteByte('\n')
			b.WriteString(tc.Result)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func countUserMessagesContaining(posts []llm.Post, msg string) int {
	n := 0
	for _, post := range posts {
		if post.Role == llm.PostRoleUser && strings.Contains(post.Message, msg) {
			n++
		}
	}
	return n
}
