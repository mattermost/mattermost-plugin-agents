// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestHandleToolCallAnswersUserQuestion covers the AskUserQuestion answer
// round-trip: the user's selection becomes the tool result, answer results are
// terminal (no share stage) even in channels, a skip records a decline and
// still continues, and an invalid answer fails the request without consuming
// the pending question.
func TestHandleToolCallAnswersUserQuestion(t *testing.T) {
	questionInput := json.RawMessage(`{
		"question": "Which channel should I post in?",
		"options": [{"label": "UX Design"}, {"label": "Design team"}]
	}`)

	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	openChannel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	cases := []struct {
		name          string
		channel       *model.Channel
		acceptedIDs   []string
		answers       map[string]mmtools.UserInteractionAnswer
		wantErr       error
		wantStatus    string
		wantResult    string
		wantShared    bool
		wantFollowUp  bool
		wantResultErr bool
	}{
		{
			name:         "answered in DM streams follow-up",
			channel:      dmChannel,
			acceptedIDs:  []string{"q-1"},
			answers:      map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"UX Design"}}},
			wantStatus:   conversation.StatusSuccess,
			wantResult:   `{"selected":["UX Design"]}`,
			wantShared:   true,
			wantFollowUp: true,
		},
		{
			name:         "answered in channel is terminal and streams follow-up immediately",
			channel:      openChannel,
			acceptedIDs:  []string{"q-1"},
			answers:      map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"Design team"}}},
			wantStatus:   conversation.StatusSuccess,
			wantResult:   `{"selected":["Design team"]}`,
			wantShared:   true,
			wantFollowUp: true,
		},
		{
			name:         "custom free-form answer round-trips into the result",
			channel:      dmChannel,
			acceptedIDs:  []string{"q-1"},
			answers:      map[string]mmtools.UserInteractionAnswer{"q-1": {Custom: "Post it in #random instead"}},
			wantStatus:   conversation.StatusSuccess,
			wantResult:   `{"selected":null,"custom":"Post it in #random instead"}`,
			wantShared:   true,
			wantFollowUp: true,
		},
		{
			name:          "skipped question records decline and continues",
			channel:       openChannel,
			acceptedIDs:   []string{},
			wantStatus:    conversation.StatusRejected,
			wantResult:    "User skipped the question",
			wantShared:    true,
			wantFollowUp:  true,
			wantResultErr: true,
		},
		{
			name:        "invalid answer leaves the question pending",
			channel:     openChannel,
			acceptedIDs: []string{"q-1"},
			answers:     map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"Engineering"}}},
			wantErr:     ErrInvalidToolAnswer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			blocks := []conversation.ContentBlock{{
				Type:            conversation.BlockTypeToolUse,
				ID:              "q-1",
				Name:            "AskUserQuestion",
				Input:           questionInput,
				Status:          conversation.StatusPending,
				UserInteraction: llm.UserInteractionSelect,
				Shared:          conversation.BoolPtr(false),
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

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			botsService.SetBotsForTesting([]*bots.Bot{bot})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   loadedStateBuilder(t),
				bots:             botsService,
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

			err = c.HandleToolCall(context.Background(), "user-id", approvalPost, tc.channel, tc.acceptedIDs, tc.answers)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// The pending question must be untouched and answerable.
				turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
				require.NoError(t, turnsErr)
				require.Len(t, turns, 1)
				var unchanged []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &unchanged))
				assert.Equal(t, conversation.StatusPending, unchanged[0].Status)
				return
			}
			require.NoError(t, err)
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			assert.Equal(t, tc.wantStatus, updatedBlocks[0].Status)
			require.NotNil(t, updatedBlocks[0].Shared)
			assert.Equal(t, tc.wantShared, *updatedBlocks[0].Shared)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Len(t, resultBlocks, 1)
			assert.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			assert.Equal(t, "q-1", resultBlocks[0].ToolUseID)
			if tc.wantResultErr {
				assert.Equal(t, conversation.StatusError, resultBlocks[0].Status)
				assert.Contains(t, resultBlocks[0].Content, tc.wantResult)
			} else {
				assert.Equal(t, conversation.StatusSuccess, resultBlocks[0].Status)
				assert.JSONEq(t, tc.wantResult, resultBlocks[0].Content)
			}

			// Answer results are terminal: decided at write time, never
			// waiting on the share/keep-private stage.
			assert.NotNil(t, resultBlocks[0].DecidedAt)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.Equal(t, tc.wantShared, *resultBlocks[0].Shared)

			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests, "expected no follow-up LLM request")
			}
		})
	}
}

// TestHandleToolCallMixedBatchInChannelAwaitsShareDecision pins the channel
// privacy gate for a batch mixing a normal tool with a question: the answered
// question is terminal, but the normal tool's result still needs a Share /
// Keep Private decision, so no channel-visible follow-up may stream yet.
func TestHandleToolCallMixedBatchInChannelAwaitsShareDecision(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	nextSeq := 1
	seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

	blocks := []conversation.ContentBlock{
		{
			Type:   conversation.BlockTypeToolUse,
			ID:     "tool-use-1",
			Name:   "jira__get_issue",
			Input:  json.RawMessage(`{}`),
			Status: conversation.StatusPending,
			Shared: conversation.BoolPtr(false),
		},
		{
			Type: conversation.BlockTypeToolUse,
			ID:   "q-1",
			Name: "AskUserQuestion",
			Input: json.RawMessage(`{
				"question": "Which channel should I post in?",
				"options": [{"label": "UX Design"}, {"label": "Design team"}]
			}`),
			Status:          conversation.StatusPending,
			UserInteraction: llm.UserInteractionSelect,
			Shared:          conversation.BoolPtr(false),
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

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)

	c := &Conversations{
		mmClient:         mmClient,
		contextBuilder:   loadedStateBuilder(t),
		bots:             botsService,
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: &loadedStateStreamingService{},
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"UX Design"}}}
	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1", "q-1"}, answers))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 4)

	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[2].Content, &updatedBlocks))
	assert.Equal(t, conversation.StatusSuccess, updatedBlocks[0].Status)
	assert.False(t, *updatedBlocks[0].Shared, "normal tool must not be auto-shared")
	assert.Equal(t, conversation.StatusSuccess, updatedBlocks[1].Status)
	assert.True(t, *updatedBlocks[1].Shared, "answered question is user-authored and shared")

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[3].Content, &resultBlocks))
	require.Len(t, resultBlocks, 2)
	assert.Nil(t, resultBlocks[0].DecidedAt, "normal result awaits the share decision")
	assert.False(t, *resultBlocks[0].Shared)
	assert.NotNil(t, resultBlocks[1].DecidedAt, "answer result is terminal")
	assert.True(t, *resultBlocks[1].Shared)

	assert.Empty(t, lm.requests, "follow-up must wait for the share decision in HandleToolResult")
}

// TestStreamToolFollowUpInteractiveFlag pins when the follow-up context offers
// user-interaction tools: a requester-driven follow-up is interactive, while a
// bot activate_ai conversation stays constrained to unattended tools.
func TestStreamToolFollowUpInteractiveFlag(t *testing.T) {
	t.Run("requester follow-up is interactive", func(t *testing.T) {
		convStore, conv := loadedStateConversationStore()
		nextSeq := 1
		seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

		lm := &loadedStateLLM{}
		streamingService := &loadedStateStreamingService{}
		c := &Conversations{
			contextBuilder:   loadedStateBuilder(t),
			convService:      conversation.NewService(convStore, nil, nil, nil),
			streamingService: streamingService,
		}

		err := c.streamToolFollowUp(
			context.Background(),
			loadedStateBot(lm),
			&model.User{Id: "user-id", Username: "user"},
			&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
			&model.Post{Id: "root-post-id"},
			conv,
			true,
		)
		require.NoError(t, err)
		streamingService.waitForStreaming()
		require.Len(t, lm.requests, 1)
		assert.True(t, lm.requests[0].Context.ToolCatalog.InteractiveUserPresent)
	})

	t.Run("activate_ai channel follow-up is not interactive", func(t *testing.T) {
		convStore, conv := loadedStateConversationStore()
		nextSeq := 1
		seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)
		rootID := "root-id"
		conv.RootPostID = &rootID

		rootPost := &model.Post{Id: "root-id", UserId: "automation-bot", Message: "automated request"}
		rootPost.AddProp(ActivateAIProp, true)
		// A human reply posted to the thread that was never stored as a turn;
		// the rebuilt request must carry it so the follow-up keeps its context.
		threadReply := &model.Post{Id: "reply-id", RootId: "root-id", UserId: "human-user", Message: "non-turn thread reply"}
		mmClient := mocks.NewMockClient(t)
		mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
		mmClient.On("GetPost", "root-id").Return(rootPost, nil).Once()
		mmClient.On("GetUser", "automation-bot").Return(&model.User{Id: "automation-bot", IsBot: true}, nil)
		mmClient.On("GetUser", "human-user").Return(&model.User{Id: "human-user", Username: "human"}, nil)
		mmClient.On("GetConfig").Maybe().Return(&model.Config{})
		// Channel follow-ups rebuild thread context via GetThreadData; return a
		// live thread with a non-turn reply to exercise the thread-aware path.
		mmClient.On("GetPostThread", "root-id").Return(&model.PostList{
			Order: []string{"root-id", "reply-id"},
			Posts: map[string]*model.Post{"root-id": rootPost, "reply-id": threadReply},
		}, nil).Once()

		lm := &loadedStateLLM{}
		streamingService := &loadedStateStreamingService{}
		c := &Conversations{
			mmClient:          mmClient,
			contextBuilder:    loadedStateBuilder(t),
			convService:       conversation.NewService(convStore, nil, nil, nil),
			streamingService:  streamingService,
			configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
			toolPolicyChecker: mapPolicyChecker{},
		}

		err := c.streamToolFollowUp(
			context.Background(),
			loadedStateBot(lm),
			&model.User{Id: "user-id", Username: "user"},
			&model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen},
			&model.Post{Id: "post-id"},
			conv,
			false,
		)
		require.NoError(t, err)
		streamingService.waitForStreaming()
		require.Len(t, lm.requests, 1)
		assert.False(t, lm.requests[0].Context.ToolCatalog.InteractiveUserPresent)

		var threadContext string
		for _, p := range lm.requests[0].Posts {
			threadContext += p.Message + "\n"
		}
		assert.Contains(t, threadContext, "non-turn thread reply",
			"channel follow-up must rebuild live thread context for non-turn posts")
	})
}

// TestHandleToolCallAutoExecutesPolicyEligiblePendingTools pins the deferred
// auto-execution contract: a tool paused only because its batch contained a
// question runs server-side when the user answers — without being in
// accepted_tool_ids — based on a fresh policy check, with its result terminal
// and shared like any auto-run round. A policy disabled since the pause must
// fall back to rejection.
func TestHandleToolCallAutoExecutesPolicyEligiblePendingTools(t *testing.T) {
	const origin = "https://jira.example.com"

	cases := []struct {
		name             string
		wouldAutoExecute bool
		policyChecker    mapPolicyChecker
		wantToolStatus   string
		wantToolResult   string
		wantToolShared   bool
		wantFollowUp     bool
	}{
		{
			name:             "auto_run_everywhere policy executes on resume",
			wouldAutoExecute: true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus: conversation.StatusAutoApproved,
			wantToolResult: "restored-result",
			wantToolShared: true,
			wantFollowUp:   true,
		},
		{
			name:             "policy disabled since the pause rejects instead",
			wouldAutoExecute: true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: false}},
			},
			wantToolStatus: conversation.StatusRejected,
			wantToolResult: "Tool call rejected by user",
			wantToolShared: false,
			wantFollowUp:   true, // the answered question still warrants a follow-up
		},
		{
			name:             "unmarked tool does not auto-run even if policy flipped to auto",
			wouldAutoExecute: false,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus: conversation.StatusRejected,
			wantToolResult: "Tool call rejected by user",
			wantToolShared: false,
			wantFollowUp:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

			blocks := []conversation.ContentBlock{
				{
					Type:             conversation.BlockTypeToolUse,
					ID:               "tool-use-1",
					Name:             "jira__get_issue",
					Input:            json.RawMessage(`{}`),
					Status:           conversation.StatusPending,
					Shared:           conversation.BoolPtr(false),
					WouldAutoExecute: tc.wouldAutoExecute,
				},
				{
					Type: conversation.BlockTypeToolUse,
					ID:   "q-1",
					Name: "AskUserQuestion",
					Input: json.RawMessage(`{
						"question": "Which channel should I post in?",
						"options": [{"label": "UX Design"}, {"label": "Design team"}]
					}`),
					Status:          conversation.StatusPending,
					UserInteraction: llm.UserInteractionSelect,
					Shared:          conversation.BoolPtr(false),
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

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			botsService.SetBotsForTesting([]*bots.Bot{bot})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:          mmClient,
				contextBuilder:    loadedStateBuilder(t),
				bots:              botsService,
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
				toolPolicyChecker: tc.policyChecker,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			// Only the question is in accepted_tool_ids: the paused tool is
			// hidden in the UI and must be resolved by policy, not by click.
			answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"UX Design"}}}
			require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"q-1"}, answers))
			streamingService.waitForStreaming()

			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			require.Len(t, turns, 4)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[2].Content, &updatedBlocks))
			assert.Equal(t, tc.wantToolStatus, updatedBlocks[0].Status)
			require.NotNil(t, updatedBlocks[0].Shared)
			assert.Equal(t, tc.wantToolShared, *updatedBlocks[0].Shared)
			assert.Equal(t, conversation.StatusSuccess, updatedBlocks[1].Status)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[3].Content, &resultBlocks))
			require.Len(t, resultBlocks, 2)
			assert.Equal(t, tc.wantToolResult, resultBlocks[0].Content)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.Equal(t, tc.wantToolShared, *resultBlocks[0].Shared)
			assert.NotNil(t, resultBlocks[0].DecidedAt, "auto/rejected results are terminal")
			assert.NotNil(t, resultBlocks[1].DecidedAt, "answer result is terminal")

			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests)
			}
		})
	}
}
