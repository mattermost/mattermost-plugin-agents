// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/meetings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
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

// TestToolApprovalAuditRecords proves the tool approval endpoints enrich the
// middleware-created audit record with the objects of the human decision: the
// approval post, its channel, the agent whose tool calls are being resolved,
// and the accepted tool-use block IDs. Without these parameters an auditor
// cannot link the authorization to the server-level records the approved
// tools produce afterwards.
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
		expectedAcceptedIDs []string
	}{
		{
			name:                "tool_call reaching the service records the decision objects",
			endpoint:            "/post/" + postID + "/tool_call",
			event:               AuditEventToolCallApproval,
			body:                `{"accepted_tool_ids":["tool-use-1","tool-use-2"]}`,
			expectedStatus:      http.StatusBadRequest,
			expectedAcceptedIDs: []string{"tool-use-1", "tool-use-2"},
		},
		{
			name:                "tool_call requester mismatch records the decision objects on the 403 fail",
			endpoint:            "/post/" + postID + "/tool_call",
			event:               AuditEventToolCallApproval,
			conversationOwner:   testOtherUserID,
			body:                `{"accepted_tool_ids":["tool-use-1"]}`,
			expectedStatus:      http.StatusForbidden,
			expectedAcceptedIDs: []string{"tool-use-1"},
		},
		{
			name:                "tool_result reaching the service records the decision objects",
			endpoint:            "/post/" + postID + "/tool_result",
			event:               AuditEventToolResultApproval,
			body:                `{"accepted_tool_ids":["tool-use-1"]}`,
			expectedStatus:      http.StatusBadRequest,
			expectedAcceptedIDs: []string{"tool-use-1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			records := e.CaptureAuditRecords()
			convStore := e.useConversations()

			e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(&model.License{SkuShortName: "advanced"})

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channelID}
			if test.conversationOwner == "" {
				post.AddProp(streaming.LLMRequesterUserIDProp, testUserID)
			} else {
				post.AddProp(streaming.ConversationIDProp, conversationID)
				require.NoError(t, convStore.CreateConversation(&store.Conversation{
					ID:     conversationID,
					UserID: test.conversationOwner,
					BotID:  testBotUserID,
				}))
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

			assert.Equal(t, testBotUserID, rec.EventData.Parameters[audit.KeyAgentID])
			assert.Equal(t, test.expectedAcceptedIDs, rec.EventData.Parameters["accepted_tool_ids"])

			assert.Equal(t, model.AuditStatusFail, rec.Status)
			assert.Equal(t, test.expectedStatus, rec.Error.Code)
		})
	}
}

// TestHandleTranscribeFileDeniedByFilePolicy proves a session-scoped file
// policy denial is returned as HTTP 403 rather than a generic 500. The
// meetings service surfaces mmapi.ErrFileActionForbidden unchanged from the
// policy-gated GetFileInfo; collapsing that sentinel into 500 would hide an
// authorization failure as a server error. Unrelated lookup failures must
// still be 500 so a blanket 403 mapping cannot hide real backend problems.
func TestHandleTranscribeFileDeniedByFilePolicy(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID    = "post12345678901234567890ab"
		channelID = "chan12345678901234567890ab"
	)

	tests := []struct {
		name           string
		allowed        bool
		getFileInfoErr error
		wantStatus     int
		wantAdminRead  bool
	}{
		{
			name:          "file policy denial is forbidden and does not read file metadata",
			allowed:       false,
			wantStatus:    http.StatusForbidden,
			wantAdminRead: false,
		},
		{
			name:           "unrelated file lookup failure stays an internal error",
			allowed:        true,
			getFileInfoErr: errors.New("file store down"),
			wantStatus:     http.StatusInternalServerError,
			wantAdminRead:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			fileID := model.NewId()
			sessionID := model.NewId()

			e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

			e.mockAPI.On("GetPost", postID).Return(&model.Post{
				Id:        postID,
				UserId:    testUserID,
				ChannelId: channelID,
				FileIds:   []string{fileID},
			}, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:     channelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "teamid",
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID}, nil)

			mmClient := mocks.NewMockClient(t)
			mmClient.On(
				"HasPermissionToFileAction",
				sessionID,
				fileID,
				model.AccessControlPolicyActionDownloadFileAttachment,
			).Return(test.allowed)

			adminReadCalled := false
			if test.allowed {
				mmClient.EXPECT().GetFileInfo(fileID).
					Run(func(string) { adminReadCalled = true }).
					Return((*model.FileInfo)(nil), test.getFileInfoErr)
			} else {
				mmClient.EXPECT().GetFileInfo(fileID).
					Run(func(string) { adminReadCalled = true }).
					Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil).
					Maybe()
			}
			mmClient.EXPECT().GetFile(fileID).
				Run(func(string) { adminReadCalled = true }).
				Return(io.NopCloser(strings.NewReader("sensitive recording contents")), nil).
				Maybe()

			e.api.meetingsService = meetings.NewService(
				e.client,
				mmClient,
				nil,
				nil,
				e.bots,
				nil,
				nil,
				nil,
				nil,
				enterprise.NewLicenseChecker(e.client),
			)

			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/transcribe/file/"+fileID, nil)
			req.Header.Add("Mattermost-User-Id", testUserID)
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, rec, req)

			require.Equal(t, test.wantStatus, rec.Result().StatusCode)
			require.Equal(t, test.wantAdminRead, adminReadCalled,
				"admin GetFileInfo/GetFile must run only after the caller's file policy allows access")
			assert.NotContains(t, rec.Body.String(), "sensitive recording contents")
		})
	}
}

// stubWakeScheduler satisfies conversations.WakeScheduler for wait-control
// route tests, which exercise authorization before any scheduling happens.
type stubWakeScheduler struct{ canceled []string }

func (s *stubWakeScheduler) ScheduleOnce(string, time.Time, any) (*cluster.JobOnce, error) {
	return nil, nil
}
func (s *stubWakeScheduler) Cancel(key string) { s.canceled = append(s.canceled, key) }

// TestWaitControlAuthorization pins that the wait check-now / cancel routes
// reject a requester who did not schedule the wait (403) and a request against
// a post with no pending wait (409), through the real route + service wiring.
func TestWaitControlAuthorization(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const ownerID = "owneruser0123456789012345a"
	markerKey := "wait_thread_postid_" + testBotUserID
	markerBytes := func(requester string) []byte {
		raw, err := json.Marshal(map[string]any{
			"conversation_id":   "conv-wait",
			"scheduled_at":      1000,
			"response_post_id":  "postid",
			"requester_user_id": requester,
			"resume_at":         600000,
			"duration_minutes":  10,
		})
		require.NoError(t, err)
		return raw
	}

	tests := []struct {
		name       string
		endpoint   string
		marker     []byte
		wantStatus int
	}{
		{name: "non-owner cannot cancel", endpoint: "/post/postid/wait/cancel", marker: markerBytes(ownerID), wantStatus: http.StatusForbidden},
		{name: "non-owner cannot check now", endpoint: "/post/postid/wait/check_now", marker: markerBytes(ownerID), wantStatus: http.StatusForbidden},
		{name: "cancel with no pending wait is a conflict", endpoint: "/post/postid/wait/cancel", marker: nil, wantStatus: http.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.setupTestBot(llm.BotConfig{Name: "permtest"})
			e.useConversations()
			e.api.conversationsService.SetWakeScheduler(&stubWakeScheduler{}, nil)

			post := &model.Post{Id: "postid", UserId: testBotUserID, ChannelId: "channelid"}
			e.mockAPI.On("GetPost", "postid").Return(post, nil)
			e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{Id: "channelid", Type: model.ChannelTypeOpen, TeamId: "teamid"}, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything, mock.Anything).Maybe()
			e.mockAPI.On("KVGet", markerKey).Return(tt.marker, nil)

			request := httptest.NewRequest(http.MethodPost, tt.endpoint, nil)
			request.Header.Add("Mattermost-User-ID", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, tt.wantStatus, recorder.Result().StatusCode)
		})
	}
}

// useConversations routes e's requests through a conversations service over
// the plugin API mocks, sharing an in-memory conversation store with the API,
// and returns that store.
func (e *TestEnvironment) useConversations() *mockConvServiceStore {
	client := mmapi.NewClient(e.client)
	convStore := newMockConvServiceStore()
	convService := conversation.NewService(convStore, e.api.prompts, client, e.bots)
	svc := conversations.New(e.api.prompts, client, e.api.streamingService, e.api.contextBuilder, e.bots, nil, enterprise.NewLicenseChecker(e.client), nil, nil, e.config, askForEveryTool)
	svc.SetConversationService(convService)
	e.api.conversationsService = svc
	e.api.SetConversationService(convService)
	return convStore
}

// TestPostResumeRoutesUseThePostsAgent pins that the routes resuming a run on
// an agent post (regenerate, tool approval, tool result sharing) refuse a
// requester who may no longer use the agent that wrote the post, not the
// default agent; that stopping your own run never needs the agent; that an
// agent's channel restrictions do not apply in its own DM; and that posts no
// agent wrote, and runs someone else requested, are refused.
func TestPostResumeRoutesUseThePostsAgent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID        = "post12345678901234567890ab"
		channelID     = "chan12345678901234567890ab"
		convID        = "conv12345678901234567890ab"
		postBotUserID = "postbotid12345678901234567"
		sentinel      = "SENTINEL-ANSWER-CONTENT"
	)

	routes := []struct {
		endpoint string
		body     string
		event    string
		// runsAgent is set for routes that run the post's agent again.
		runsAgent bool
	}{
		{endpoint: "regenerate", runsAgent: true},
		{endpoint: "stop"},
		{endpoint: "tool_call", body: `{"accepted_tool_ids":["tool-use-1"],"tool_answers":{"tool-use-1":{"text":"` + sentinel + `"}}}`, event: AuditEventToolCallApproval, runsAgent: true},
		{endpoint: "tool_result", body: `{"accepted_tool_ids":["tool-use-1"]}`, event: AuditEventToolResultApproval, runsAgent: true},
	}

	unrestricted := func(cfg *llm.BotConfig) {}
	blockRequester := func(cfg *llm.BotConfig) {
		cfg.UserAccessLevel = llm.UserAccessLevelBlock
		cfg.UserIDs = []string{testUserID}
	}

	tests := []struct {
		name           string
		postAuthor     string
		defaultAgent   func(*llm.BotConfig)
		postAgent      func(*llm.BotConfig)
		notOwner       bool
		wantForbidden  bool
		wantBadRequest bool
	}{
		{name: "post's agent blocks the requester", postAuthor: postBotUserID, defaultAgent: unrestricted, postAgent: blockRequester, wantForbidden: true},
		{name: "only the default agent blocks the requester", postAuthor: postBotUserID, defaultAgent: blockRequester, postAgent: unrestricted},
		{name: "post's agent may be used in no channel", postAuthor: postBotUserID, defaultAgent: unrestricted, postAgent: func(cfg *llm.BotConfig) {
			cfg.ChannelAccessLevel = llm.ChannelAccessLevelNone
		}},
		{name: "post's agent may be used only in other channels", postAuthor: postBotUserID, defaultAgent: unrestricted, postAgent: func(cfg *llm.BotConfig) {
			cfg.ChannelAccessLevel = llm.ChannelAccessLevelAllow
			cfg.ChannelIDs = []string{"otherchannel00000000000000"}
		}},
		{name: "post no agent wrote", postAuthor: testUserID, defaultAgent: unrestricted, postAgent: unrestricted, wantBadRequest: true},
		{name: "run someone else requested", postAuthor: postBotUserID, defaultAgent: unrestricted, postAgent: unrestricted, notOwner: true},
	}

	for _, route := range routes {
		for _, test := range tests {
			t.Run(route.endpoint+"/"+test.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				records := e.CaptureAuditRecords()
				e.api.streamingService = &recordingStreamingService{}
				convStore := e.useConversations()

				defaultCfg := llm.BotConfig{ID: testBotUserID, Name: "defaultbot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll}
				test.defaultAgent(&defaultCfg)
				postCfg := llm.BotConfig{ID: postBotUserID, Name: "postbot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll}
				test.postAgent(&postCfg)
				e.setBots(
					bots.NewBot(defaultCfg, llm.ServiceConfig{}, &model.Bot{UserId: testBotUserID, Username: "defaultbot"}, nil),
					bots.NewBot(postCfg, llm.ServiceConfig{}, &model.Bot{UserId: postBotUserID, Username: "postbot"}, nil),
				)

				owner := testUserID
				if test.notOwner {
					owner = testOtherUserID
				}
				require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: owner, BotID: postBotUserID, Operation: "conversation"}))
				post := &model.Post{Id: postID, UserId: test.postAuthor, ChannelId: channelID}
				post.AddProp(streaming.ConversationIDProp, convID)
				e.mockAPI.On("GetPost", postID).Return(post, nil)
				e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
					Id:   channelID,
					Name: postBotUserID + "__" + testUserID,
					Type: model.ChannelTypeDirect,
				}, nil)
				e.mockAPI.On("HasPermissionToChannel", testUserID, channelID, model.PermissionReadChannel).Return(true)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Locale: "en"}, nil).Maybe()

				var body io.Reader
				if route.body != "" {
					body = strings.NewReader(route.body)
				}
				req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/"+route.endpoint, body)
				req.Header.Add("Mattermost-User-Id", testUserID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, req)

				status := recorder.Result().StatusCode
				wantFailStatus := 0
				switch {
				case test.wantBadRequest:
					wantFailStatus = http.StatusBadRequest
				case test.notOwner, test.wantForbidden && route.runsAgent:
					wantFailStatus = http.StatusForbidden
				}
				if wantFailStatus != 0 {
					require.Equal(t, wantFailStatus, status)
				} else {
					require.NotEqual(t, http.StatusForbidden, status)
				}

				if route.event == "" {
					assert.Empty(t, *records, "an unaudited route must not emit a record")
					return
				}
				require.Len(t, *records, 1, "exactly one audit record must be emitted")
				rec := (*records)[0]
				assert.Equal(t, route.event, rec.EventName)
				assert.Equal(t, testUserID, rec.Actor.UserId)
				assert.Equal(t, postID, rec.EventData.Parameters[audit.KeyPostID])
				assert.Equal(t, channelID, rec.EventData.Parameters[audit.KeyChannelID])
				if test.postAuthor == postBotUserID {
					assert.Equal(t, postBotUserID, rec.EventData.Parameters[audit.KeyAgentID])
				}
				if wantFailStatus != 0 {
					assert.Equal(t, model.AuditStatusFail, rec.Status)
					assert.Equal(t, wantFailStatus, rec.Error.Code)
				}
				marshaled, err := json.Marshal(rec)
				require.NoError(t, err)
				assert.NotContains(t, string(marshaled), sentinel)
			})
		}
	}
}

// TestRegenerateAnalysisRechecksTheAnalyzedSource pins how the regenerate
// route answers when an analysis's source cannot be used: a thread that is
// gone or unreadable is 403, failing to look the thread up is 500, and a
// channel analysis or a post tagged no_regen, which regeneration cannot
// rebuild, is 400.
func TestRegenerateAnalysisRechecksTheAnalyzedSource(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID          = "analysis45678901234567890a"
		threadID        = "thread45678901234567890abc"
		threadChannelID = "threadchan5678901234567890"
		convID          = "conv12345678901234567890ab"
	)
	dmChannel := &model.Channel{Id: "dmchan345678901234567890ab", Type: model.ChannelTypeDirect, Name: testBotUserID + "__" + testUserID}

	tests := []struct {
		name            string
		channelAnalysis bool
		// meetingSummary seeds a meeting summary post, which records its
		// requester instead of a conversation.
		meetingSummary bool
		noRegen        bool
		threadErr      *model.AppError
		threadHidden   bool
		wantStatus     int
	}{
		{name: "thread was deleted", threadErr: model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound), wantStatus: http.StatusForbidden},
		{name: "thread lookup fails", threadErr: model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusInternalServerError), wantStatus: http.StatusInternalServerError},
		{name: "requester can no longer read the thread", threadHidden: true, wantStatus: http.StatusForbidden},
		{name: "channel analysis", channelAnalysis: true, noRegen: true, wantStatus: http.StatusBadRequest},
		{name: "channel analysis created before it was tagged no_regen", channelAnalysis: true, wantStatus: http.StatusBadRequest},
		{name: "meeting summary", meetingSummary: true, noRegen: true, wantStatus: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.streamingService = &recordingStreamingService{}
			convStore := e.useConversations()
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})

			post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: dmChannel.Id}
			operation := llm.OperationThreadAnalysis
			switch {
			case tc.meetingSummary:
				post.AddProp(streaming.LLMRequesterUserIDProp, testUserID)
			case tc.channelAnalysis:
				operation = llm.OperationChannelSummary
				post.AddProp(streaming.ConversationIDProp, convID)
				post.AddProp(conversations.AnalyzedChannelIDProp, threadChannelID)
				post.AddProp(conversations.AnalysisTypeProp, "summarize_channel")
			default:
				post.AddProp(streaming.ConversationIDProp, convID)
				post.AddProp(conversations.ThreadIDProp, threadID)
				post.AddProp(conversations.AnalysisTypeProp, "summarize_thread")
			}
			if tc.noRegen {
				post.AddProp(streaming.NoRegen, "true")
			}
			rootID := postID
			require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: testUserID, BotID: testBotUserID, RootPostID: &rootID, Operation: operation}))

			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetChannel", dmChannel.Id).Return(dmChannel, nil)
			e.mockAPI.On("HasPermissionToChannel", testUserID, dmChannel.Id, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Locale: "en"}, nil).Maybe()
			if tc.threadErr != nil {
				e.mockAPI.On("GetPost", threadID).Return(nil, tc.threadErr)
			} else {
				e.mockAPI.On("GetPost", threadID).Return(&model.Post{Id: threadID, ChannelId: threadChannelID}, nil).Maybe()
			}
			e.mockAPI.On("HasPermissionToChannel", testUserID, threadChannelID, model.PermissionReadChannel).Return(!tc.threadHidden).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/regenerate", nil)
			req.Header.Add("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tc.wantStatus, recorder.Result().StatusCode, recorder.Body.String())
		})
	}
}
