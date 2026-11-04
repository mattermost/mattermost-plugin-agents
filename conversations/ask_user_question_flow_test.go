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
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
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

	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__" + testRequesterID}
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
				Shared:          new(false),
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
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			botsService.SetBotsForTesting([]*bots.Bot{bot})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", testRequesterID).Maybe().Return(&model.User{Id: testRequesterID, Username: "user"}, nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				toolPolicyChecker: mapPolicyChecker{},
				configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
				mmClient:          mmClient,
				contextBuilder:    loadedStateBuilder(t),
				bots:              botsService,
				licenseChecker:    enterprisetest.CheckerAt(enterprise.LevelProfessional),
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

			err = c.HandleToolCall(context.Background(), testRequesterID, approvalPost, tc.channel, tc.acceptedIDs, tc.answers)

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
				assert.Zero(t, countUserMessagesContaining(lm.requests[0].Posts, toolRejectionGuidance),
					"answered or skipped questions must not receive tool-rejection guidance")
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
			Shared: new(false),
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
			Shared:          new(false),
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
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	seedRequesterInitiator(t, convStore, conv.ID, mmClient)

	c := &Conversations{
		toolPolicyChecker: mapPolicyChecker{},
		configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
		mmClient:          mmClient,
		contextBuilder:    loadedStateBuilder(t),
		bots:              botsService,
		licenseChecker:    enterprisetest.CheckerAt(enterprise.LevelProfessional),
		convService:       conversation.NewService(convStore, nil, nil, nil),
		streamingService:  &loadedStateStreamingService{},
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"UX Design"}}}
	require.NoError(t, c.HandleToolCall(context.Background(), testRequesterID, approvalPost, channel, []string{"tool-use-1", "q-1"}, answers))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 5)

	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[3].Content, &updatedBlocks))
	assert.Equal(t, conversation.StatusSuccess, updatedBlocks[0].Status)
	assert.False(t, *updatedBlocks[0].Shared, "normal tool must not be auto-shared")
	assert.Equal(t, conversation.StatusSuccess, updatedBlocks[1].Status)
	assert.True(t, *updatedBlocks[1].Shared, "answered question is user-authored and shared")

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[4].Content, &resultBlocks))
	require.Len(t, resultBlocks, 2)
	assert.Nil(t, resultBlocks[0].DecidedAt, "normal result awaits the share decision")
	assert.False(t, *resultBlocks[0].Shared)
	assert.NotNil(t, resultBlocks[1].DecidedAt, "answer result is terminal")
	assert.True(t, *resultBlocks[1].Shared)

	assert.Empty(t, lm.requests, "follow-up must wait for the share decision in HandleToolResult")
}

func passthroughBotsService(bot *bots.Bot) *bots.MMBots {
	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	botsService := bots.New(mockAPI, pluginAPI, enterprise.NewLicenseChecker(pluginAPI), nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bot})
	return botsService
}

// TestStreamToolFollowUpInteractiveFlag pins when the follow-up context offers
// user-interaction tools: a requester-driven follow-up is interactive, while a
// run a bot or automation started stays unattended, in a DM or a channel.
func TestStreamToolFollowUpInteractiveFlag(t *testing.T) {
	dmCases := []struct {
		name            string
		initiator       *model.Post
		initiatingUser  *model.User
		wantInteractive bool
	}{
		{
			name:            "requester follow-up in a DM is interactive",
			initiator:       &model.Post{Id: "initiator-post", UserId: testRequesterID},
			initiatingUser:  &model.User{Id: testRequesterID, Username: "user"},
			wantInteractive: true,
		},
		{
			name: "DM follow-up of a run a bot activate_ai started is not interactive",
			initiator: func() *model.Post {
				p := &model.Post{Id: "initiator-post", UserId: testRequesterID}
				p.AddProp(ActivateAIProp, true)
				p.AddProp(FromBotProp, "true")
				return p
			}(),
			initiatingUser: &model.User{Id: testRequesterID, Username: "user"},
		},
		{
			name: "DM follow-up of a run a webhook started is not interactive",
			initiator: func() *model.Post {
				p := &model.Post{Id: "initiator-post", UserId: testRequesterID}
				p.AddProp(FromWebhookProp, "true")
				return p
			}(),
			initiatingUser: &model.User{Id: testRequesterID, Username: "user"},
		},
	}
	for _, tc := range dmCases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			initiatorPostID := tc.initiator.Id
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID: "initiating-turn", ConversationID: conv.ID, PostID: &initiatorPostID, Role: "user", Sequence: 0,
			}))
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)
			botPostID := "bot-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID: "bot-turn", ConversationID: conv.ID, PostID: &botPostID, Role: "assistant", Sequence: nextSeq,
			}))

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetPost", initiatorPostID).Return(tc.initiator, nil).Maybe()
			mmClient.On("GetUser", tc.initiator.UserId).Return(tc.initiatingUser, nil).Maybe()

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				toolPolicyChecker: mapPolicyChecker{},
				mmClient:          mmClient,
				contextBuilder:    loadedStateBuilder(t),
				bots:              passthroughBotsService(bot),
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
			}

			post := &model.Post{Id: botPostID, UserId: "bot-id"}
			policy, err := c.resolveTurnPolicy(context.Background(), turnRequest{
				bot:     bot,
				user:    &model.User{Id: testRequesterID, Username: "user"},
				channel: &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__" + testRequesterID},
				conv:    conv,
				post:    post,
			})
			require.NoError(t, err)

			err = c.streamToolFollowUp(context.Background(), policy, post, conv, followUpOptions{})
			require.NoError(t, err)
			streamingService.waitForStreaming()
			require.Len(t, lm.requests, 1)
			assert.Equal(t, tc.wantInteractive, lm.requests[0].Context.ToolCatalog.InteractiveUserPresent)
		})
	}

	t.Run("activate_ai channel follow-up is not interactive", func(t *testing.T) {
		convStore, conv := loadedStateConversationStore()
		rootID := "root-id"
		conv.RootPostID = &rootID
		require.NoError(t, convStore.CreateTurn(&store.Turn{
			ID: "initiating-turn", ConversationID: conv.ID, PostID: &rootID, Role: "user", Sequence: 0,
		}))
		nextSeq := 1
		seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)
		botPostID := "post-id"
		require.NoError(t, convStore.CreateTurn(&store.Turn{
			ID: "bot-turn", ConversationID: conv.ID, PostID: &botPostID, Role: "assistant", Sequence: nextSeq,
		}))

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
		bot := loadedStateBot(lm)
		streamingService := &loadedStateStreamingService{}
		c := &Conversations{
			mmClient:          mmClient,
			contextBuilder:    loadedStateBuilder(t),
			bots:              passthroughBotsService(bot),
			licenseChecker:    enterprisetest.CheckerAt(enterprise.LevelProfessional),
			convService:       conversation.NewService(convStore, nil, nil, nil),
			streamingService:  streamingService,
			configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
			toolPolicyChecker: mapPolicyChecker{},
		}

		post := &model.Post{Id: botPostID, UserId: "bot-id"}
		policy, err := c.resolveTurnPolicy(context.Background(), turnRequest{
			bot:     bot,
			user:    &model.User{Id: testRequesterID, Username: "user"},
			channel: &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen},
			conv:    conv,
			post:    post,
		})
		require.NoError(t, err)

		err = c.streamToolFollowUp(context.Background(), policy, post, conv, followUpOptions{})
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
// auto-execution contract: marked tools run server-side without appearing in
// accepted_tool_ids, including when an interrupted all-auto batch is resumed
// with an empty list. A policy or license change since the pause must fall
// back to a non-user-rejection result so the follow-up cannot blame the user.
func TestHandleToolCallAutoExecutesPolicyEligiblePendingTools(t *testing.T) {
	const origin = "https://jira.example.com"

	cases := []struct {
		name                  string
		wouldAutoExecute      bool
		includeQuestion       bool
		unlicensed            bool
		policyChecker         mapPolicyChecker
		wantToolStatus        string
		wantToolResult        string
		wantToolUseShared     bool
		wantResultShared      bool
		wantRejectionGuidance bool
	}{
		{
			name:             "interrupted all-auto batch resumes with empty accepted list",
			wouldAutoExecute: true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus:        conversation.StatusAutoApproved,
			wantToolResult:        "restored-result",
			wantToolUseShared:     true,
			wantResultShared:      true,
			wantRejectionGuidance: false,
		},
		{
			name:             "interrupted all-auto resume rejects when policy was disabled",
			wouldAutoExecute: true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: false}},
			},
			wantToolStatus:        conversation.StatusRejected,
			wantToolResult:        toolCallPolicyDeniedResult,
			wantToolUseShared:     false,
			wantResultShared:      true,
			wantRejectionGuidance: false,
		},
		{
			// Remote MCP tools are license-gated at supply time: an
			// unlicensed context builder drops them from the tool store, so
			// a marked remote tool must resolve to rejection on resume — not
			// execute and not fail the whole submission.
			name:             "remote auto-run resume below Enterprise rejects instead of executing",
			wouldAutoExecute: true,
			unlicensed:       true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus:        conversation.StatusRejected,
			wantToolResult:        toolCallPolicyDeniedResult,
			wantToolUseShared:     false,
			wantResultShared:      true,
			wantRejectionGuidance: false,
		},
		{
			name:             "auto_run_everywhere policy executes on resume",
			wouldAutoExecute: true,
			includeQuestion:  true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus:        conversation.StatusAutoApproved,
			wantToolResult:        "restored-result",
			wantToolUseShared:     true,
			wantResultShared:      true,
			wantRejectionGuidance: false,
		},
		{
			name:             "policy disabled since the pause rejects instead",
			wouldAutoExecute: true,
			includeQuestion:  true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: false}},
			},
			wantToolStatus:        conversation.StatusRejected,
			wantToolResult:        toolCallPolicyDeniedResult,
			wantToolUseShared:     false,
			wantResultShared:      true,
			wantRejectionGuidance: false,
		},
		{
			name:             "unmarked tool does not auto-run even if policy flipped to auto",
			wouldAutoExecute: false,
			includeQuestion:  true,
			policyChecker: mapPolicyChecker{
				origin: {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantToolStatus:        conversation.StatusRejected,
			wantToolResult:        toolCallRejectedByUserResult,
			wantToolUseShared:     false,
			wantResultShared:      true,
			wantRejectionGuidance: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

			blocks := []conversation.ContentBlock{{
				Type:             conversation.BlockTypeToolUse,
				ID:               "tool-use-1",
				Name:             "jira__get_issue",
				Input:            json.RawMessage(`{}`),
				Status:           conversation.StatusPending,
				Shared:           new(false),
				WouldAutoExecute: tc.wouldAutoExecute,
			}}
			if tc.includeQuestion {
				blocks = append(blocks, conversation.ContentBlock{
					Type: conversation.BlockTypeToolUse,
					ID:   "q-1",
					Name: "AskUserQuestion",
					Input: json.RawMessage(`{
						"question": "Which channel should I post in?",
						"options": [{"label": "UX Design"}, {"label": "Design team"}]
					}`),
					Status:          conversation.StatusPending,
					UserInteraction: llm.UserInteractionSelect,
					Shared:          new(false),
				})
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

			// Professional runs multiplayer channels but not remote MCP.
			level := enterprise.LevelEnterprise
			if tc.unlicensed {
				level = enterprise.LevelProfessional
			}
			mockAPI := &plugintest.API{}
			enterprisetest.StubLicense(mockAPI, level)
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			botsService.SetBotsForTesting([]*bots.Bot{bot})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", testRequesterID).Maybe().Return(&model.User{Id: testRequesterID, Username: "user"}, nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			contextBuilder := loadedStateBuilder(t)
			if tc.unlicensed {
				contextBuilder = newChannelFollowUpTestBuilderWithLicense(t, []llm.Tool{loadedStateTool()}, &channelFollowUpTestConfig{}, false)
			}

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:          mmClient,
				contextBuilder:    contextBuilder,
				bots:              botsService,
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
				licenseChecker:    licenseChecker,
				configProvider:    &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
				toolPolicyChecker: tc.policyChecker,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			acceptedIDs := []string{}
			var answers map[string]mmtools.UserInteractionAnswer
			if tc.includeQuestion {
				// Only the question is accepted: the marked tool must be
				// resolved by policy, not by the user's selection.
				acceptedIDs = []string{"q-1"}
				answers = map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"UX Design"}}}
			}
			require.NoError(t, c.HandleToolCall(context.Background(), testRequesterID, approvalPost, channel, acceptedIDs, answers))
			streamingService.waitForStreaming()

			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			require.Len(t, turns, 4)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[2].Content, &updatedBlocks))
			assert.Equal(t, tc.wantToolStatus, updatedBlocks[0].Status)
			assert.Equal(t, tc.wouldAutoExecute, updatedBlocks[0].WouldAutoExecute,
				"WouldAutoExecute must stay on the block so follow-up guidance can distinguish policy denial from a user rejection")
			require.NotNil(t, updatedBlocks[0].Shared)
			assert.Equal(t, tc.wantToolUseShared, *updatedBlocks[0].Shared)
			if tc.includeQuestion {
				assert.Equal(t, conversation.StatusSuccess, updatedBlocks[1].Status)
			}

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[3].Content, &resultBlocks))
			require.Len(t, resultBlocks, len(blocks))
			assert.Equal(t, tc.wantToolResult, resultBlocks[0].Content)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.Equal(t, tc.wantResultShared, *resultBlocks[0].Shared)
			assert.NotNil(t, resultBlocks[0].DecidedAt, "auto/rejected results are terminal")
			if tc.includeQuestion {
				assert.NotNil(t, resultBlocks[1].DecidedAt, "answer result is terminal")
			}

			require.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			assert.Contains(t, requestToolResults(lm.requests[0]), tc.wantToolResult)
			if tc.wantRejectionGuidance {
				requireRejectionGuidanceIsFinalUserPost(t, lm.requests[0].Posts)
			} else {
				assert.NotContains(t, completionRequestText(lm.requests[0]), toolCallRejectedByUserResult,
					"policy denial and auto-exec must not blame the user or receive rejection guidance")
			}
		})
	}
}
