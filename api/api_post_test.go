// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingStreamingService captures StopStreaming calls so tests can assert
// the API performed the local stop. The streaming methods are stubs because
// handleStop never invokes them.
type recordingStreamingService struct {
	stoppedPostIDs []string
}

func (s *recordingStreamingService) StreamToNewPost(_ context.Context, _, _ string, _ *llm.TextStreamResult, _ *model.Post, _ string) error {
	return nil
}

func (s *recordingStreamingService) StreamToNewDM(_ context.Context, _ string, _ *llm.TextStreamResult, _ string, _ *model.Post, _ string) error {
	return nil
}

func (s *recordingStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *recordingStreamingService) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *recordingStreamingService) StopStreaming(postID string) {
	s.stoppedPostIDs = append(s.stoppedPostIDs, postID)
}

func (s *recordingStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (s *recordingStreamingService) FinishStreaming(string) {}

var _ streaming.Service = (*recordingStreamingService)(nil)

// recordingStreamStopNotifier captures PublishStreamStop invocations.
type recordingStreamStopNotifier struct {
	publishedPostIDs []string
	err              error
}

func (n *recordingStreamStopNotifier) PublishStreamStop(postID string) error {
	n.publishedPostIDs = append(n.publishedPostIDs, postID)
	return n.err
}

var _ StreamStopClusterNotifier = (*recordingStreamStopNotifier)(nil)

// TestHandleStop exercises the /post/{id}/stop endpoint end-to-end and proves
// the per-node local stop and the cluster broadcast are both gated on the
// authorization branches that precede them. The cluster broadcast is the
// HA-without-sticky-sessions fix for MM-67491: a regression that publishes
// before authorization would let any user cancel any post on every node.
func TestHandleStop(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID         = "post12345678901234567890ab"
		channelID      = "chan12345678901234567890ab"
		conversationID = "conv12345678901234567890ab"
	)

	type setup struct {
		postUserID         string
		conversationOwner  string
		body               string
		omitNotifier       bool
		notifierErr        error
		omitConversationID bool
	}

	tests := []struct {
		name             string
		setup            setup
		expectedStatus   int
		expectStopCalled bool
		expectPublished  bool
	}{
		{
			name:             "happy path stops locally and broadcasts to peers",
			setup:            setup{postUserID: testBotUserID, conversationOwner: testUserID},
			expectedStatus:   http.StatusOK,
			expectStopCalled: true,
			expectPublished:  true,
		},
		{
			name:             "cluster publish error does not fail the request",
			setup:            setup{postUserID: testBotUserID, conversationOwner: testUserID, notifierErr: errors.New("simulated cluster failure")},
			expectedStatus:   http.StatusOK,
			expectStopCalled: true,
			expectPublished:  true,
		},
		{
			name:             "single-node deployment with no cluster notifier still stops locally",
			setup:            setup{postUserID: testBotUserID, conversationOwner: testUserID, omitNotifier: true},
			expectedStatus:   http.StatusOK,
			expectStopCalled: true,
			expectPublished:  false,
		},
		{
			name:             "post not owned by bot returns 400 without stopping or broadcasting",
			setup:            setup{postUserID: testUserID, conversationOwner: testUserID},
			expectedStatus:   http.StatusBadRequest,
			expectStopCalled: false,
			expectPublished:  false,
		},
		{
			name:             "non-owner cannot stop another user's stream and no broadcast fires",
			setup:            setup{postUserID: testBotUserID, conversationOwner: testOtherUserID},
			expectedStatus:   http.StatusForbidden,
			expectStopCalled: false,
			expectPublished:  false,
		},
		{
			name:             "non-empty body is rejected before any side effect",
			setup:            setup{postUserID: testBotUserID, conversationOwner: testUserID, body: `{"unexpected":"payload"}`},
			expectedStatus:   http.StatusBadRequest,
			expectStopCalled: false,
			expectPublished:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			streamingSvc := &recordingStreamingService{}
			notifier := &recordingStreamStopNotifier{err: test.setup.notifierErr}

			e.api.streamingService = streamingSvc
			if !test.setup.omitNotifier {
				e.api.streamStopNotifier = notifier
			} else {
				e.api.streamStopNotifier = nil
			}

			e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

			post := &model.Post{
				Id:        postID,
				UserId:    test.setup.postUserID,
				ChannelId: channelID,
			}
			if !test.setup.omitConversationID {
				post.AddProp(streaming.ConversationIDProp, conversationID)
				e.conversationStore.conversations[conversationID] = &store.Conversation{
					ID:     conversationID,
					UserID: test.setup.conversationOwner,
					BotID:  testBotUserID,
				}
			}

			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:     channelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "teamid",
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)

			var body io.Reader
			if test.setup.body != "" {
				body = strings.NewReader(test.setup.body)
			}
			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/stop", body)
			req.Header.Add("Mattermost-User-ID", testUserID)

			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, test.expectedStatus, rec.Result().StatusCode)

			if test.expectStopCalled {
				require.Equal(t, []string{postID}, streamingSvc.stoppedPostIDs,
					"local StopStreaming must run on the node serving an authorized request")
			} else {
				require.Empty(t, streamingSvc.stoppedPostIDs,
					"rejected stop requests must not cancel the stream")
			}

			if test.setup.omitNotifier {
				return
			}
			if test.expectPublished {
				require.Equal(t, []string{postID}, notifier.publishedPostIDs,
					"authorized stop must broadcast to peers for HA without sticky sessions")
			} else {
				require.Empty(t, notifier.publishedPostIDs,
					"rejected stop requests must not leak a peer-cancel broadcast")
			}
		})
	}
}

// TestHandleStopLogsClusterPublishErrors verifies handleStop logs publish
// failures so operators can see why a peer-cancel did not propagate. Without
// this, a silently-failing PublishStreamStop would make the original bug
// reappear with no diagnostic trail.
func TestHandleStopLogsClusterPublishErrors(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID         = "post12345678901234567890ab"
		channelID      = "chan12345678901234567890ab"
		conversationID = "conv12345678901234567890ab"
	)

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.streamingService = &recordingStreamingService{}
	e.api.streamStopNotifier = &recordingStreamStopNotifier{err: errors.New("cluster broker down")}

	e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

	post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID}
	post.AddProp(streaming.ConversationIDProp, conversationID)
	e.conversationStore.conversations[conversationID] = &store.Conversation{
		ID:     conversationID,
		UserID: testUserID,
		BotID:  testBotUserID,
	}

	e.mockAPI.On("GetPost", postID).Return(post, nil)
	e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: "teamid",
	}, nil)
	e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)

	req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/stop", nil)
	req.Header.Add("Mattermost-User-ID", testUserID)

	rec := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, rec, req)

	require.Equal(t, http.StatusOK, rec.Result().StatusCode)

	foundLog := false
	for _, call := range e.mockAPI.Calls {
		if call.Method != "LogError" || len(call.Arguments) == 0 {
			continue
		}
		msg, ok := call.Arguments[0].(string)
		if ok && strings.Contains(msg, "Failed to publish stream stop cluster event") {
			foundLog = true
			break
		}
	}
	require.True(t, foundLog, "cluster publish failures must be logged so operators can diagnose dropped peer-cancels")
}

// TestAskUserResponseHTTPStatus pins the sentinel-error → HTTP status mapping
// of the ask_user_response endpoint (C5). The behavior behind each sentinel is
// covered by the conversations-layer HandleAskUserResponse table.
func TestAskUserResponseHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid answer", err: conversations.ErrInvalidAskAnswer, want: http.StatusBadRequest},
		{name: "wrapped invalid answer", err: fmt.Errorf("%w: no option selected", conversations.ErrInvalidAskAnswer), want: http.StatusBadRequest},
		{name: "not the asked target", err: conversations.ErrNotAskTarget, want: http.StatusForbidden},
		{name: "conversation gone", err: conversations.ErrAskConversationGone, want: http.StatusNotFound},
		{name: "already answered", err: conversations.ErrAskNotPending, want: http.StatusConflict},
		{name: "unexpected error", err: errors.New("db down"), want: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, askUserResponseHTTPStatus(tc.err))
		})
	}
}

// TestHandleAskUserResponse drives POST /post/{postid}/ask_user_response
// through the full router: session auth, JSON binding, the conversations-layer
// target check, and the C5 status mapping all fire exactly as a real client
// would see them. The happy-path rows record the answer and write the C6/C7
// tool_result turn; the follow-up stream itself is covered by the
// conversations-layer tests (here the anchor turn has no post, so the resume
// is skipped after the answer is recorded).
func TestHandleAskUserResponse(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		cardPostID  = "card12345678901234567890ab"
		dmChannelID = "dmch12345678901234567890ab"
		convID      = "conv12345678901234567890ab"
		toolUseID   = "ask-tool-use-1"
	)

	makeBlocks := func(t *testing.T, status string) json.RawMessage {
		t.Helper()
		blocks := []conversation.ContentBlock{{
			Type:           conversation.BlockTypeToolUse,
			ID:             toolUseID,
			Name:           "AskAnotherUser",
			Input:          json.RawMessage(`{"username":"target","question":"Which environment?"}`),
			Status:         status,
			DeferredResult: true,
		}}
		content, err := json.Marshal(blocks)
		require.NoError(t, err)
		return content
	}

	tests := []struct {
		name             string
		omitSession      bool
		body             string
		notACard         bool
		targetID         string // defaults to testUserID (the caller)
		seedConv         bool
		blockStatus      string // seeds an assistant turn when non-empty
		expectedStatus   int
		expectedBody     string
		expectResultTurn bool
	}{
		{
			name:           "missing session header is unauthorized",
			omitSession:    true,
			body:           `{"action":"decline"}`,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "malformed body is a bad request",
			body:           `{`,
			seedConv:       true,
			blockStatus:    conversation.StatusWaiting,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "non-target caller is forbidden",
			body:           `{"action":"answer","free_form":"hi"}`,
			targetID:       testOtherUserID,
			seedConv:       true,
			blockStatus:    conversation.StatusWaiting,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "unknown action is a bad request",
			body:           `{"action":"maybe"}`,
			seedConv:       true,
			blockStatus:    conversation.StatusWaiting,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "non-card post is a bad request",
			body:           `{"action":"decline"}`,
			notACard:       true,
			seedConv:       true,
			blockStatus:    conversation.StatusWaiting,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "missing conversation is not found",
			body:           `{"action":"decline"}`,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "already answered question conflicts",
			body:           `{"action":"decline"}`,
			seedConv:       true,
			blockStatus:    conversation.StatusSuccess,
			expectedStatus: http.StatusConflict,
		},
		{
			name:             "decline records the refusal",
			body:             `{"action":"decline"}`,
			seedConv:         true,
			blockStatus:      conversation.StatusWaiting,
			expectedStatus:   http.StatusOK,
			expectedBody:     `"status":"declined"`,
			expectResultTurn: true,
		},
		{
			name:             "answer records the response",
			body:             `{"action":"answer","free_form":"Use staging"}`,
			seedConv:         true,
			blockStatus:      conversation.StatusWaiting,
			expectedStatus:   http.StatusOK,
			expectedBody:     `"status":"answered"`,
			expectResultTurn: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

			cardPost := &model.Post{
				Id:        cardPostID,
				UserId:    testBotUserID,
				ChannelId: dmChannelID,
				Type:      conversations.AskUserPostType,
			}
			if test.notACard {
				cardPost.Type = ""
			}
			target := testUserID
			if test.targetID != "" {
				target = test.targetID
			}
			cardPost.AddProp(conversations.AskUserTargetIDProp, target)
			cardPost.AddProp(conversations.AskUserConversationIDProp, convID)
			cardPost.AddProp(conversations.AskUserToolUseIDProp, toolUseID)

			// Rebuild the conversations service around a mock mmapi client and
			// an in-memory conversation store so the handler runs for real.
			mmClient := mmapimocks.NewMockClient(t)
			for i := 1; i <= 7; i++ {
				args := make([]interface{}, i)
				for j := range args {
					args[j] = mock.Anything
				}
				mmClient.On("LogError", args...).Maybe().Return()
				mmClient.On("LogDebug", args...).Maybe().Return()
			}
			mmClient.On("GetUser", testUserID).Maybe().Return(&model.User{Id: testUserID, Username: "target-user"}, nil)
			mmClient.On("GetPost", cardPostID).Maybe().Return(cardPost, nil)
			mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Maybe().Return(nil)
			mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)

			convStore := newMockConvServiceStore()
			svc := conversations.New(nil, mmClient, nil, nil, e.bots, nil, nil, nil, nil, e.config)
			svc.SetConversationService(conversation.NewService(convStore, nil, nil, e.bots))
			e.api.conversationsService = svc

			if test.seedConv {
				require.NoError(t, convStore.CreateConversation(&store.Conversation{
					ID:     convID,
					UserID: testOtherUserID, // conversation initiator, not the asked target
					BotID:  testBotUserID,
				}))
			}
			if test.blockStatus != "" {
				require.NoError(t, convStore.CreateTurn(&store.Turn{
					ID:             "assistant-turn",
					ConversationID: convID,
					Role:           "assistant",
					Content:        makeBlocks(t, test.blockStatus),
					Sequence:       1,
				}))
			}

			e.mockAPI.On("GetPost", cardPostID).Return(cardPost, nil).Maybe()
			e.mockAPI.On("GetChannel", dmChannelID).Return(&model.Channel{
				Id:   dmChannelID,
				Name: testBotUserID + "__" + testUserID,
				Type: model.ChannelTypeDirect,
			}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, dmChannelID, model.PermissionReadChannel).Return(true).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/post/"+cardPostID+"/ask_user_response", strings.NewReader(test.body))
			if !test.omitSession {
				req.Header.Add("Mattermost-User-ID", testUserID)
			}

			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)
			resp := rec.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode)

			if test.expectedBody != "" {
				bodyBytes, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Contains(t, string(bodyBytes), test.expectedBody)
			}

			turns := convStore.turns[convID]
			if test.expectResultTurn {
				require.Len(t, turns, 2, "an accepted answer must append the tool_result turn")
				require.Equal(t, "tool_result", turns[1].Role)
			} else if test.blockStatus != "" {
				require.Len(t, turns, 1, "rejected requests must not write result turns")
			}
		})
	}
}

// TestToolApprovalAuditRecords proves the tool approval endpoints enrich the
// middleware-created audit record with the objects of the human decision: the
// approval post, its channel, the accepted tool-use block IDs, and — once the
// service layer is reached — the agent whose tool calls are being resolved.
// Without these parameters an auditor cannot link the authorization to the
// server-level records the approved tools produce afterwards.
func TestToolApprovalAuditRecords(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID         = "post12345678901234567890ab"
		channelID      = "chan12345678901234567890ab"
		conversationID = "conv12345678901234567890ab"
	)

	tests := []struct {
		name     string
		endpoint string
		event    string
		// conversationOwner seeds a conversation entity owned by that user.
		// When empty, ownership passes via the legacy requester prop and the
		// service rejects the missing conversation_id with a controlled 400
		// after the handler and service enrichment already ran.
		conversationOwner   string
		body                string
		expectedStatus      int
		expectedAcceptedIDs []string // nil asserts the parameter is absent
		expectAgentID       bool
	}{
		{
			name:                "tool_call reaching the service records the decision objects",
			endpoint:            "/post/" + postID + "/tool_call",
			event:               AuditEventToolCallApproval,
			body:                `{"accepted_tool_ids":["tool-use-1","tool-use-2"]}`,
			expectedStatus:      http.StatusBadRequest,
			expectedAcceptedIDs: []string{"tool-use-1", "tool-use-2"},
			expectAgentID:       true,
		},
		{
			name:              "tool_call requester mismatch records post and channel on the 403 fail",
			endpoint:          "/post/" + postID + "/tool_call",
			event:             AuditEventToolCallApproval,
			conversationOwner: testOtherUserID,
			body:              `{"accepted_tool_ids":["tool-use-1"]}`,
			expectedStatus:    http.StatusForbidden,
		},
		{
			name:                "tool_result reaching the service records the decision objects",
			endpoint:            "/post/" + postID + "/tool_result",
			event:               AuditEventToolResultApproval,
			body:                `{"accepted_tool_ids":["tool-use-1"]}`,
			expectedStatus:      http.StatusBadRequest,
			expectedAcceptedIDs: []string{"tool-use-1"},
			expectAgentID:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			records := e.CaptureAuditRecords()

			e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(&model.License{SkuShortName: "advanced"})

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID}
			if test.conversationOwner == "" {
				post.AddProp(streaming.LLMRequesterUserIDProp, testUserID)
			} else {
				post.AddProp(streaming.ConversationIDProp, conversationID)
				e.conversationStore.conversations[conversationID] = &store.Conversation{
					ID:     conversationID,
					UserID: test.conversationOwner,
					BotID:  testBotUserID,
				}
			}

			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:   channelID,
				Name: testBotUserID + "__" + testUserID,
				Type: model.ChannelTypeDirect,
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)

			req := httptest.NewRequest(http.MethodPost, test.endpoint, strings.NewReader(test.body))
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, test.expectedStatus, recorder.Result().StatusCode)

			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, test.event, rec.EventName)
			assert.Equal(t, testUserID, rec.Actor.UserId)
			assert.Equal(t, postID, rec.EventData.Parameters[audit.KeyPostID])
			assert.Equal(t, channelID, rec.EventData.Parameters[audit.KeyChannelID])

			if test.expectedAcceptedIDs != nil {
				assert.Equal(t, test.expectedAcceptedIDs, rec.EventData.Parameters["accepted_tool_ids"])
			} else {
				assert.NotContains(t, rec.EventData.Parameters, "accepted_tool_ids",
					"a request denied before body binding must not claim accepted tool IDs")
			}

			if test.expectAgentID {
				assert.Equal(t, testBotUserID, rec.EventData.Parameters[audit.KeyAgentID],
					"service-layer enrichment must reach the same record via the request context")
			}

			assert.Equal(t, model.AuditStatusFail, rec.Status)
			assert.Equal(t, test.expectedStatus, rec.Error.Code)
		})
	}
}
