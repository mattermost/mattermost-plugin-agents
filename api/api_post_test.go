// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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
