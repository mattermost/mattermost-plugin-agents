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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requestBoundLLM answers like a provider whose HTTP request is bound to the
// context it is called with: it starts answering once released, and a
// context canceled by then aborts the response before any output.
type requestBoundLLM struct {
	text    string
	release chan struct{}

	mu   sync.Mutex
	ctxs []context.Context
}

func (l *requestBoundLLM) ChatCompletion(ctx context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.mu.Lock()
	l.ctxs = append(l.ctxs, ctx)
	l.mu.Unlock()

	out := make(chan llm.TextStreamEvent)
	go func() {
		defer close(out)
		<-l.release
		if ctx.Err() != nil {
			return
		}
		for _, event := range channelAnalysisTextEvents(l.text) {
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &llm.TextStreamResult{Stream: out}, nil
}

func (l *requestBoundLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "", errors.New("not implemented")
}

func (l *requestBoundLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (l *requestBoundLLM) InputTokenLimit() int  { return 100000 }
func (l *requestBoundLLM) OutputTokenLimit() int { return 8192 }

func (l *requestBoundLLM) recorded() []context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]context.Context(nil), l.ctxs...)
}

// TestAnalysisRoutesSurviveRequestCancellation pins that the analysis routes
// generate their answer on a context that outlives the HTTP request. Servers
// that propagate the request context to plugins cancel it as soon as the
// handler returns, while the answer is still being generated and streamed
// into the analysis DM; an LLM call bound to the request is aborted then, and
// the DM is left without an answer.
func TestAnalysisRoutesSurviveRequestCancellation(t *testing.T) {
	prevMode := gin.Mode()
	prevWriter := gin.DefaultWriter
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	t.Cleanup(func() {
		gin.SetMode(prevMode)
		gin.DefaultWriter = prevWriter
	})

	const sessionID = "session45678901234567890ab"
	threadPost := &model.Post{Id: "postid", ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello"}
	channelPost := &model.Post{Id: "post-1", ChannelId: testChannelID, UserId: testOtherUserID, Message: "hello", CreateAt: 2}

	tests := []struct {
		name     string
		path     string
		body     string
		mcpTools []llm.Tool
		setup    func(e *TestEnvironment, mmClient *mmapimocks.MockClient)
	}{
		{
			name: "channel analysis",
			path: "/channel/" + testChannelID + "/analyze",
			body: `{"analysis_type":"custom","prompt":"summarize this channel"}`,
			mcpTools: []llm.Tool{
				{Name: "mattermost__read_channel", ServerOrigin: mcp.EmbeddedClientKey},
				{Name: "mattermost__get_channel_info", ServerOrigin: mcp.EmbeddedClientKey},
			},
		},
		{
			name: "channel interval summary",
			path: "/channel/" + testChannelID + "/interval",
			body: `{"start_time":1,"end_time":0,"preset_prompt":"summarize_range"}`,
			setup: func(_ *TestEnvironment, mmClient *mmapimocks.MockClient) {
				mmClient.On("GetPostsSince", testChannelID, int64(1)).Return(&model.PostList{
					Order: []string{"post-1"},
					Posts: map[string]*model.Post{"post-1": channelPost},
				}, nil)
				mmClient.On("GetUser", testOtherUserID).Return(&model.User{Id: testOtherUserID, Username: "author"}, nil)
			},
		},
		{
			name: "thread analysis",
			path: "/post/postid/analyze",
			body: `{"analysis_type":"summarize_thread"}`,
			setup: func(e *TestEnvironment, mmClient *mmapimocks.MockClient) {
				e.mockAPI.On("GetPost", "postid").Return(threadPost, nil)
				mmClient.On("GetPostThread", "postid").Return(&model.PostList{
					Order: []string{"postid"},
					Posts: map[string]*model.Post{"postid": threadPost},
				}, nil)
				mmClient.On("GetUser", testOtherUserID).Return(&model.User{Id: testOtherUserID, Username: "author"}, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mmapimocks.NewMockClient(t)
			e, _, _ := setupNoToolsAPI(t, &noToolsTestMCPProvider{tools: tc.mcpTools}, mmClient)
			defer e.Cleanup(t)

			provider := &requestBoundLLM{text: "Analysis answer.", release: make(chan struct{})}
			e.setBots(bots.NewBot(
				llm.BotConfig{ID: testBotUserID, Name: "matty", DisplayName: "Matty"},
				llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
				&model.Bot{UserId: testBotUserID, Username: "matty", DisplayName: "Matty"},
				provider,
			))

			notifier := &signalingNotifier{
				Service: notifications.NewService(&deliveryRecordingClient{viewingKVClient: newViewingKVClient()}, i18n.Init(), true),
				ended:   make(chan notifications.StreamEnd, 1),
			}
			streamingService := streaming.NewMMPostStreamService(&analysisStreamClient{}, i18n.Init())
			streamingService.SetNotifier(notifier)
			e.api.streamingService = streamingService

			e.mockAPI.On("GetChannel", testChannelID).Return(&model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}, nil)
			e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, testChannelID, model.PermissionReadChannel).Return(true)
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil)
			if tc.setup != nil {
				tc.setup(e, mmClient)
			}

			reqCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			request := httptest.NewRequestWithContext(reqCtx, http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Add("Mattermost-User-ID", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: sessionID}, recorder, request)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode, recorder.Body.String())

			// What core does once the handler has returned.
			cancelRequest()
			close(provider.release)

			var end notifications.StreamEnd
			select {
			case end = <-notifier.ended:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "the analysis never finished")
			}
			// requestBoundLLM answers nothing on a context canceled by then, so
			// a completed answer shows the request's cancel did not reach it.
			assert.Equal(t, notifications.OutcomeCompleted, end.Outcome, "canceling the request must not cancel the LLM call it started")

			llmCtxs := provider.recorded()
			require.Len(t, llmCtxs, 1)
			assert.Equal(t, sessionID, auth.SessionIDFromContext(llmCtxs[0]), "the LLM call keeps the requester's session for tool and file policy checks")
			assert.Eventually(t, func() bool { return llmCtxs[0].Err() != nil }, 5*time.Second, 10*time.Millisecond, "the run's context ends with its stream")
		})
	}
}
