// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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

// Compile-time assertion that recordingStreamingService satisfies the full Service interface.
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

func TestHandleStopBroadcastsClusterEvent(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID         = "post12345678901234567890ab"
		channelID      = "chan12345678901234567890ab"
		userID         = "user12345678901234567890ab"
		botID          = "abcdefghijklmnopqrstuvwxyz"
		conversationID = "conv12345678901234567890ab"
	)

	tests := []struct {
		name           string
		notifierErr    error
		expectedStatus int
	}{
		{
			name:           "successful stop publishes to cluster",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "publish failure does not fail the request",
			notifierErr:    errors.New("simulated cluster failure"),
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			streamingSvc := &recordingStreamingService{}
			notifier := &recordingStreamStopNotifier{err: test.notifierErr}

			e.api.streamingService = streamingSvc
			e.api.streamStopNotifier = notifier

			e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

			post := &model.Post{
				Id:        postID,
				UserId:    botID,
				ChannelId: channelID,
			}
			post.AddProp(streaming.ConversationIDProp, conversationID)
			e.conversationStore.conversations[conversationID] = &store.Conversation{
				ID:     conversationID,
				UserID: userID,
				BotID:  botID,
			}

			e.mockAPI.On("GetPost", postID).Return(post, nil)
			e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
				Id:     channelID,
				Type:   model.ChannelTypeOpen,
				TeamId: "teamid",
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", userID, channelID, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/stop", nil)
			req.Header.Add("Mattermost-User-ID", userID)

			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, test.expectedStatus, rec.Result().StatusCode)
			require.Equal(t, []string{postID}, streamingSvc.stoppedPostIDs,
				"local StopStreaming must run on the node serving the request")
			require.Equal(t, []string{postID}, notifier.publishedPostIDs,
				"the stop request must be broadcast to peers so HA without sticky sessions still cancels the stream")
		})
	}
}

// TestHandleStopWithoutNotifier verifies that an API instance with no cluster
// notifier (single-node deployment) still performs the local stop without
// panicking on the nil notifier.
func TestHandleStopWithoutNotifier(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID         = "post12345678901234567890ab"
		channelID      = "chan12345678901234567890ab"
		userID         = "user12345678901234567890ab"
		botID          = "abcdefghijklmnopqrstuvwxyz"
		conversationID = "conv12345678901234567890ab"
	)

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	streamingSvc := &recordingStreamingService{}
	e.api.streamingService = streamingSvc
	e.api.streamStopNotifier = nil

	e.setupTestBot(llm.BotConfig{Name: "thebot", DisplayName: "The Bot"})

	post := &model.Post{
		Id:        postID,
		UserId:    botID,
		ChannelId: channelID,
	}
	post.AddProp(streaming.ConversationIDProp, conversationID)
	e.conversationStore.conversations[conversationID] = &store.Conversation{
		ID:     conversationID,
		UserID: userID,
		BotID:  botID,
	}

	e.mockAPI.On("GetPost", postID).Return(post, nil)
	e.mockAPI.On("GetChannel", channelID).Return(&model.Channel{
		Id:     channelID,
		Type:   model.ChannelTypeOpen,
		TeamId: "teamid",
	}, nil)
	e.mockAPI.On("HasPermissionToChannel", userID, channelID, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	req := httptest.NewRequest(http.MethodPost, "/post/"+postID+"/stop", nil)
	req.Header.Add("Mattermost-User-ID", userID)

	rec := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, rec, req)

	require.Equal(t, http.StatusOK, rec.Result().StatusCode)
	require.Equal(t, []string{postID}, streamingSvc.stoppedPostIDs)
}
