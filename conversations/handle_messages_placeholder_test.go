// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type placeholderStreamingService struct {
	mu             sync.Mutex
	streamedPost   *model.Post
	streamedLocale string
	streamed       chan struct{}
}

func (s *placeholderStreamingService) StreamToNewPost(context.Context, string, string, *llm.TextStreamResult, *model.Post, string) error {
	return nil
}

func (s *placeholderStreamingService) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}

func (s *placeholderStreamingService) StreamToPost(_ context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string) {
	s.mu.Lock()
	s.streamedPost = post.Clone()
	s.streamedLocale = userLocale
	s.mu.Unlock()

	_, _ = stream.ReadAll()
	close(s.streamed)
}

func (s *placeholderStreamingService) StopStreaming(string) {}

func (s *placeholderStreamingService) GetStreamingContext(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (s *placeholderStreamingService) FinishStreaming(string) {}

func newLatencyTestBot() *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty"},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		nil,
	)
}

func TestRespondToPostCreatesPlaceholderBeforeStartupCompletes(t *testing.T) {
	locale := "en"
	mmClient := mmapimocks.NewMockClient(t)
	streamingService := &placeholderStreamingService{streamed: make(chan struct{})}
	conversationService := &Conversations{
		mmClient:         mmClient,
		streamingService: streamingService,
		i18n:             i18n.Init(),
	}

	postCreated := make(chan struct{})
	var createdPost *model.Post
	mmClient.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(nil).Run(func(args mock.Arguments) {
		post := args.Get(0).(*model.Post)
		post.Id = "response-post-id"
		createdPost = post.Clone()
		close(postCreated)
	}).Once()
	mmClient.On("GetConfig").Return(&model.Config{
		LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &locale},
	}).Maybe()

	startupEntered := make(chan struct{})
	releaseStartup := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		errCh <- conversationService.respondToPost(
			newLatencyTestBot(),
			&model.User{Id: "user-id", Locale: "es"},
			&model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect},
			&model.Post{ChannelId: "channel-id", RootId: "root-id"},
			"original-post-id",
			func() (*llm.TextStreamResult, error) {
				close(startupEntered)
				<-releaseStartup
				return llm.NewStreamFromString("hello"), nil
			},
		)
	}()

	select {
	case <-startupEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup phase never began")
	}

	select {
	case <-postCreated:
	default:
		t.Fatal("placeholder post was not created before startup work completed")
	}

	require.NotNil(t, createdPost)
	require.Equal(t, "custom_llmbot", createdPost.Type)
	require.Equal(t, "", createdPost.Message)
	require.Equal(t, "user-id", createdPost.GetProp(streaming.LLMRequesterUserID))
	require.Equal(t, "original-post-id", createdPost.GetProp(streaming.RespondingToProp))

	close(releaseStartup)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("respondToPost did not return")
	}

	select {
	case <-streamingService.streamed:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming did not begin for existing placeholder post")
	}

	streamingService.mu.Lock()
	defer streamingService.mu.Unlock()
	require.Equal(t, "es", streamingService.streamedLocale)
	require.NotNil(t, streamingService.streamedPost)
	require.Equal(t, "response-post-id", streamingService.streamedPost.Id)
}

func TestRespondToPostReplacesPlaceholderOnStartupError(t *testing.T) {
	locale := "en"
	mmClient := mmapimocks.NewMockClient(t)
	streamingService := &placeholderStreamingService{streamed: make(chan struct{})}
	conversationService := &Conversations{
		mmClient:         mmClient,
		streamingService: streamingService,
		i18n:             i18n.Init(),
	}

	postCreated := make(chan struct{})
	postUpdated := make(chan struct{})
	var updatedPost *model.Post

	mmClient.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(nil).Run(func(args mock.Arguments) {
		post := args.Get(0).(*model.Post)
		post.Id = "response-post-id"
		close(postCreated)
	}).Once()
	mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Return(nil).Run(func(args mock.Arguments) {
		updatedPost = args.Get(0).(*model.Post).Clone()
		close(postUpdated)
	}).Once()
	mmClient.On("GetConfig").Return(&model.Config{
		LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &locale},
	}).Maybe()

	err := conversationService.respondToPost(
		newLatencyTestBot(),
		&model.User{Id: "user-id", Locale: "en"},
		&model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect},
		&model.Post{ChannelId: "channel-id", RootId: "root-id"},
		"original-post-id",
		func() (*llm.TextStreamResult, error) {
			return nil, errors.New("startup failed")
		},
	)
	require.Error(t, err)

	select {
	case <-postCreated:
	case <-time.After(2 * time.Second):
		t.Fatal("placeholder post was not created")
	}

	select {
	case <-postUpdated:
	case <-time.After(2 * time.Second):
		t.Fatal("placeholder post was not updated after startup error")
	}

	require.NotNil(t, updatedPost)
	require.Contains(t, updatedPost.Message, "Sorry! An error occurred while accessing the LLM.")
}
