// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/require"
)

// analysisStreamClient is the Mattermost side of the real streaming service:
// DMs are created in memory.
type analysisStreamClient struct {
	mu  sync.Mutex
	dms []*model.Post
}

func (c *analysisStreamClient) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
}
func (c *analysisStreamClient) UpdatePost(*model.Post) error { return nil }
func (c *analysisStreamClient) CreatePost(*model.Post) error { return nil }
func (c *analysisStreamClient) DM(senderID, receiverID string, post *model.Post) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	post.Id = model.NewId()
	post.UserId = senderID
	post.ChannelId = model.GetDMNameFromIds(senderID, receiverID)
	c.dms = append(c.dms, post.Clone())
	return nil
}
func (c *analysisStreamClient) GetUser(userID string) (*model.User, error) {
	return &model.User{Id: userID, Locale: "en"}, nil
}
func (c *analysisStreamClient) GetChannel(channelID string) (*model.Channel, error) {
	return &model.Channel{Id: channelID, Type: model.ChannelTypeDirect}, nil
}
func (c *analysisStreamClient) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.SetDefaults()
	return cfg
}
func (c *analysisStreamClient) KVSet(string, any) error { return nil }
func (c *analysisStreamClient) LogError(string, ...any) {}
func (c *analysisStreamClient) LogWarn(string, ...any)  {}
func (c *analysisStreamClient) LogDebug(string, ...any) {}

// deliveryRecordingClient records the deliveries the notifications service
// asks the server for.
type deliveryRecordingClient struct {
	*viewingKVClient
	mu         sync.Mutex
	deliveries []*model.DeliverPostNotificationsOptions
	postIDs    []string
}

func (c *deliveryRecordingClient) DeliverPostNotifications(postID string, options *model.DeliverPostNotificationsOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.postIDs = append(c.postIDs, postID)
	c.deliveries = append(c.deliveries, options)
	return nil
}

type signalingNotifier struct {
	*notifications.Service
	ended chan notifications.StreamEnd
}

func (n *signalingNotifier) Notify(ctx context.Context, end notifications.StreamEnd) {
	n.Service.Notify(ctx, end)
	n.ended <- end
}

// heldStreamLLM records the context of each completion and holds its stream
// open until that context ends, like a provider mid-response.
type heldStreamLLM struct {
	channelAnalysisSequenceLLM
	ctxs chan context.Context
}

func (l *heldStreamLLM) ChatCompletion(ctx context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.ctxs <- ctx
	stream := make(chan llm.TextStreamEvent)
	go func() {
		<-ctx.Done()
		close(stream)
	}()
	return &llm.TextStreamResult{Stream: stream}, nil
}

// TestChannelAnalysisStopEndsTheAnalysis stops a channel analysis mid-round
// through the real streaming service: the run producing the stream must end
// too, not stay blocked on a stream nobody reads.
func TestChannelAnalysisStopEndsTheAnalysis(t *testing.T) {
	prevMode := gin.Mode()
	prevWriter := gin.DefaultWriter
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	t.Cleanup(func() {
		gin.SetMode(prevMode)
		gin.DefaultWriter = prevWriter
	})

	e, _, _ := setupChannelAnalysisAPI(t, false)
	defer e.Cleanup(t)
	lm := &heldStreamLLM{ctxs: make(chan context.Context, 1)}
	e.setBots(bots.NewBot(
		llm.BotConfig{ID: testBotUserID, Name: "matty", DisplayName: "Matty", AutoEnableNewMCPTools: true},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
		lm,
	))

	streamClient := &analysisStreamClient{}
	streamingService := streaming.NewMMPostStreamService(streamClient, i18n.Init())
	e.api.streamingService = streamingService

	request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/analyze?botUsername=matty", strings.NewReader(`{"analysis_type":"summarize_channel","days":1}`))
	request.Header.Add("Mattermost-User-ID", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var runCtx context.Context
	select {
	case runCtx = <-lm.ctxs:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the analysis never called the LLM")
	}
	streamClient.mu.Lock()
	require.Len(t, streamClient.dms, 1)
	dmID := streamClient.dms[0].Id
	streamClient.mu.Unlock()

	streamingService.StopStreaming(dmID)
	select {
	case <-runCtx.Done():
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the analysis kept running after the requester stopped it")
	}
	require.True(t, streamingService.Shutdown(5*time.Second), "the stopped run finished")
}

// TestChannelAnalysisReleasesItsDMWhenTheAnalysisEnds runs the channel
// analysis route through the real streaming service: the analysis DM is
// created deferred and released to the requester's DM once it is complete.
func TestChannelAnalysisReleasesItsDMWhenTheAnalysisEnds(t *testing.T) {
	prevMode := gin.Mode()
	prevWriter := gin.DefaultWriter
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	t.Cleanup(func() {
		gin.SetMode(prevMode)
		gin.DefaultWriter = prevWriter
	})

	e, _, fakeLLM := setupChannelAnalysisAPI(t, false)
	defer e.Cleanup(t)
	// Text only: the tool-call round is covered elsewhere, and the real
	// streaming service sanitizes tool call arguments the tool runner also reads.
	fakeLLM.calls = [][]llm.TextStreamEvent{channelAnalysisTextEvents("Channel summary.")}

	streamClient := &analysisStreamClient{}
	deliveries := &deliveryRecordingClient{viewingKVClient: newViewingKVClient()}
	notifier := &signalingNotifier{
		Service: notifications.NewService(deliveries, i18n.Init(), true),
		ended:   make(chan notifications.StreamEnd, 2),
	}
	streamingService := streaming.NewMMPostStreamService(streamClient, i18n.Init())
	streamingService.SetNotifier(notifier)
	e.api.streamingService = streamingService

	request := httptest.NewRequest(http.MethodPost, "/channel/"+testChannelID+"/analyze?botUsername=matty", strings.NewReader(`{"analysis_type":"summarize_channel","days":1}`))
	request.Header.Add("Mattermost-User-ID", testUserID)
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

	var end notifications.StreamEnd
	select {
	case end = <-notifier.ended:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "analysis never notified")
	}
	select {
	case extra := <-notifier.ended:
		require.FailNow(t, "unexpected second notification", "%+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	streamClient.mu.Lock()
	require.Len(t, streamClient.dms, 1)
	dm := streamClient.dms[0]
	streamClient.mu.Unlock()
	require.True(t, dm.HasDeferredNotification(), "the analysis DM is created deferred")

	require.Equal(t, dm.Id, end.PostID)
	require.Equal(t, testUserID, end.RequesterID)
	require.Equal(t, notifications.OutcomeCompleted, end.Outcome)
	require.Equal(t, notifications.DeliveryEveryone, notifications.Decide(end))

	deliveries.mu.Lock()
	defer deliveries.mu.Unlock()
	require.Equal(t, []string{dm.Id}, deliveries.postIDs)
	require.Equal(t, []*model.DeliverPostNotificationsOptions{nil}, deliveries.deliveries, "the analysis is released, not scoped")
}
