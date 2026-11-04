// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
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

// Each decision request reads the turns twice before acting: once to
// correlate the trace and once for the decision itself.
const decisionReadsPerRequest = 2

func newClaimTestConversations(t *testing.T, convStore *loadedStateFlowStore, lm *loadedStateLLM, streamingService *loadedStateStreamingService, resolver llm.ToolResolver) *Conversations {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	botsService := bots.New(mockAPI, pluginAPI, enterprise.NewLicenseChecker(pluginAPI), nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(lm)})

	mmClient := mocks.NewMockClient(t)
	seedRequesterInitiator(t, convStore, "conv-id", mmClient)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", testRequesterID).Maybe().Return(&model.User{Id: testRequesterID, Username: "user"}, nil)
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})

	tool := loadedStateTool()
	if resolver != nil {
		tool.Resolver = resolver
	}
	return &Conversations{
		configProvider:   &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
		mmClient:         mmClient,
		contextBuilder:   newChannelFollowUpTestBuilder(t, []llm.Tool{tool}, &channelFollowUpTestConfig{}),
		bots:             botsService,
		licenseChecker:   enterprisetest.CheckerAt(enterprise.LevelProfessional),
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}
}

func seedClaimTestTurns(t *testing.T, convStore *loadedStateFlowStore, convID, postID string, assistant []conversation.ContentBlock, results []conversation.ContentBlock) {
	t.Helper()
	nextSeq := 1
	seedLoadToolPair(t, convStore, convID, "load-1", "jira__get_issue", &nextSeq)

	content, err := json.Marshal(assistant)
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID: "assistant-turn", ConversationID: convID, PostID: &postID, Role: "assistant", Content: content, Sequence: nextSeq,
	}))
	if results == nil {
		return
	}
	content, err = json.Marshal(results)
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID: "result-turn", ConversationID: convID, Role: "tool_result", Content: content, Sequence: nextSeq + 1,
	}))
}

func countToolResultTurns(t *testing.T, convStore *loadedStateFlowStore, convID string) int {
	t.Helper()
	turns, err := convStore.GetTurnsForConversation(convID)
	require.NoError(t, err)
	count := 0
	for _, turn := range turns {
		if turn.Role == "tool_result" && turn.ID != "load-result-load-1" {
			count++
		}
	}
	return count
}

// A double click, a client retry, or an HA peer handling the same approval
// must execute the approved calls once, whichever way the requests interleave.
func TestHandleToolCallExecutesApprovedCallsOnceUnderConcurrentClicks(t *testing.T) {
	tests := []struct {
		name string
		// interleave arranges for secondClick to run inside the first
		// request; onExecute runs on every tool execution.
		interleave func(convStore *loadedStateFlowStore, secondClick func()) (onExecute func())
	}{
		{
			name: "second click arrives while the first is executing",
			interleave: func(_ *loadedStateFlowStore, secondClick func()) func() {
				fired := false
				return func() {
					if !fired {
						fired = true
						secondClick()
					}
				}
			},
		},
		{
			name: "second click reads the turn before the first records its decision",
			interleave: func(convStore *loadedStateFlowStore, secondClick func()) func() {
				convStore.afterTurnsRead = func(reads int) {
					if reads == decisionReadsPerRequest {
						secondClick()
					}
				}
				return func() {}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			postID := "approval-post-id"
			seedClaimTestTurns(t, convStore, conv.ID, postID, []conversation.ContentBlock{{
				Type: conversation.BlockTypeToolUse, ID: "tool-use-1", Name: "jira__get_issue",
				Input: json.RawMessage(`{}`), Status: conversation.StatusPending, Shared: new(false),
			}}, nil)

			post := &model.Post{Id: postID, UserId: "bot-id"}
			post.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			var c *Conversations
			var errs []error
			click := func() {
				errs = append(errs, c.HandleToolCall(context.Background(), testRequesterID, post, channel, []string{"tool-use-1"}, nil))
			}

			executions := 0
			var onExecute func()
			c = newClaimTestConversations(t, convStore, &loadedStateLLM{}, &loadedStateStreamingService{},
				func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
					executions++
					onExecute()
					return "result", nil
				})
			onExecute = tt.interleave(convStore, click)

			click()

			assert.Equal(t, 1, executions, "the approved call must run exactly once")
			require.Len(t, errs, 2)
			succeeded, stale := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					succeeded++
				case assert.ErrorIs(t, err, ErrStaleToolClick):
					stale++
				}
			}
			assert.Equal(t, 1, succeeded)
			assert.Equal(t, 1, stale)
			assert.Equal(t, 1, countToolResultTurns(t, convStore, conv.ID), "exactly one tool_result turn is recorded")
		})
	}
}

// failingCASStore fails the failAt-th compare-and-swap with a store error.
type failingCASStore struct {
	*loadedStateFlowStore
	calls  int
	failAt int
}

func (s *failingCASStore) CompareAndSwapTurnContent(id string, expected, content json.RawMessage) (bool, error) {
	s.calls++
	if s.calls == s.failAt {
		return false, errors.New("database unavailable")
	}
	return s.loadedStateFlowStore.CompareAndSwapTurnContent(id, expected, content)
}

// A share that fails after recording its decision must stay retryable: the
// retry applies the share and streams the follow-up.
func TestHandleToolResultRetriesAfterPartialWriteFailure(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	postID := "approval-post-id"
	seedClaimTestTurns(t, convStore, conv.ID, postID,
		[]conversation.ContentBlock{{
			Type: conversation.BlockTypeToolUse, ID: "tool-use-1", Name: "jira__get_issue",
			Input: json.RawMessage(`{}`), Status: conversation.StatusSuccess, Shared: new(false),
		}},
		[]conversation.ContentBlock{{
			Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-1",
			Content: "result", Status: conversation.StatusSuccess, Shared: new(false),
		}},
	)

	post := &model.Post{Id: postID, UserId: "bot-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	lm := &loadedStateLLM{}
	streamingService := &loadedStateStreamingService{}
	c := newClaimTestConversations(t, convStore, lm, streamingService, nil)
	c.convService = conversation.NewService(&failingCASStore{loadedStateFlowStore: convStore, failAt: 2}, nil, nil, nil)

	require.Error(t, c.HandleToolResult(context.Background(), testRequesterID, post, channel, []string{"tool-use-1"}))
	require.NoError(t, c.HandleToolResult(context.Background(), testRequesterID, post, channel, []string{"tool-use-1"}))
	streamingService.waitForStreaming()

	assert.Len(t, lm.requests, 1, "the retry must stream the follow-up")
	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	for _, turn := range turns {
		if turn.ID != "assistant-turn" && turn.ID != "result-turn" {
			continue
		}
		blocks, err := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, err)
		require.NotNil(t, blocks[0].Shared)
		assert.True(t, *blocks[0].Shared, "turn %s must carry the share", turn.ID)
	}
}

// Concurrent share clicks must record the decision and stream the
// channel-visible follow-up once.
func TestHandleToolResultStreamsFollowUpOnceUnderConcurrentClicks(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	postID := "approval-post-id"
	seedClaimTestTurns(t, convStore, conv.ID, postID,
		[]conversation.ContentBlock{{
			Type: conversation.BlockTypeToolUse, ID: "tool-use-1", Name: "jira__get_issue",
			Input: json.RawMessage(`{}`), Status: conversation.StatusSuccess, Shared: new(false),
		}},
		[]conversation.ContentBlock{{
			Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-1",
			Content: "result", Status: conversation.StatusSuccess, Shared: new(false),
		}},
	)

	post := &model.Post{Id: postID, UserId: "bot-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	lm := &loadedStateLLM{}
	streamingService := &loadedStateStreamingService{}
	c := newClaimTestConversations(t, convStore, lm, streamingService, nil)

	var errs []error
	click := func() {
		errs = append(errs, c.HandleToolResult(context.Background(), testRequesterID, post, channel, []string{"tool-use-1"}))
	}
	convStore.afterTurnsRead = func(reads int) {
		if reads == decisionReadsPerRequest {
			click()
		}
	}

	click()
	streamingService.waitForStreaming()

	require.Len(t, errs, 2)
	assert.NoError(t, errs[0], "the inner click reads first and wins")
	assert.ErrorIs(t, errs[1], ErrStaleToolClick)
	assert.Len(t, lm.requests, 1, "the follow-up must stream once")
}
