// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
)

// sweepClient adapts the Mattermost client mock to the recovery sweep.
type sweepClient struct{ mmapi.Client }

func (sweepClient) LogInfo(string, ...any) {}

// TestApprovalRunOnDeadNodeIsRecovered runs an approved tool call on a real
// store and streaming service while its node "dies" (stops heartbeating). The
// recovery sweep must mark the post interrupted, regenerate must be refused
// while the run is live and allowed afterwards, and the run's late results
// must not overwrite the sweep's verdict.
func TestApprovalRunOnDeadNodeIsRecovered(t *testing.T) {
	const (
		botID      = "bot-id"
		userID     = "user12345678901234567890ab"
		userPostID = "user-post-id"
		botPostID  = "bot-post-id"
		message    = "Let me look that up."
	)
	ctx := context.Background()
	s, db := newPostgresTestStoreWithDB(t)

	anthropic := &fakeAnthropic{}
	server := httptest.NewServer(anthropic)
	defer server.Close()
	lm, err := bifrost.New(bifrost.Config{
		Provider:         schemas.Anthropic,
		APIKey:           "test-key",
		APIURL:           server.URL,
		DefaultModel:     "claude-sonnet-4-6",
		StreamingTimeout: 10 * time.Second,
	})
	require.NoError(t, err)
	defer lm.Shutdown()

	running := make(chan struct{}, 1)
	release := make(chan struct{})
	tool := channelFollowUpTestMCPTool("jira__get_issue", "https://jira.example.com", "fetch a Jira issue")
	// The tool ignores cancellation, like a slow remote call.
	tool.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
		running <- struct{}{}
		<-release
		return "MM-1 is done", nil
	}

	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: botID + "__" + userID}
	var postsMu sync.Mutex
	posts := map[string]*model.Post{
		userPostID: {Id: userPostID, UserId: userID, ChannelId: channel.Id, Message: "@matty status of MM-1?"},
	}

	mmClient := mocks.NewMockClient(t)
	for _, method := range []string{"LogDebug", "LogWarn", "LogError"} {
		for args := []any{mock.Anything}; len(args) <= 11; args = append(args, mock.Anything) {
			mmClient.On(method, args...).Maybe().Return()
		}
	}
	mmClient.On("GetUser", userID).Maybe().Return(&model.User{Id: userID, Username: "alice", Locale: "en"}, nil)
	mmClient.On("GetUser", botID).Maybe().Return(&model.User{Id: botID, Username: "matty", IsBot: true}, nil)
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("KVSet", mock.Anything, mock.Anything).Maybe().Return(nil)
	allowPostRunReservation(mmClient)
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})
	mmClient.On("GetChannel", channel.Id).Maybe().Return(channel, nil)
	mmClient.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mmClient.EXPECT().GetPost(mock.Anything).RunAndReturn(func(id string) (*model.Post, error) {
		postsMu.Lock()
		defer postsMu.Unlock()
		if post, ok := posts[id]; ok {
			return post.Clone(), nil
		}
		return nil, model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound)
	}).Maybe()
	mmClient.On("UpdatePost", mock.Anything).Maybe().Run(func(args mock.Arguments) {
		postsMu.Lock()
		defer postsMu.Unlock()
		post := args.Get(0).(*model.Post)
		posts[post.Id] = post.Clone()
	}).Return(nil)
	storedPost := func() *model.Post {
		postsMu.Lock()
		defer postsMu.Unlock()
		return posts[botPostID].Clone()
	}

	streamingService := streaming.NewMMPostStreamService(mmClient, i18n.Init())
	streamingService.SetTurnStore(s)

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, true)
	licenseChecker := toolLicenseChecker(t, true)
	botsService := bots.New(mockAPI, pluginapi.NewClient(mockAPI, nil), licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bots.NewBot(
		llm.BotConfig{
			ID:                    botID,
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
		},
		llm.ServiceConfig{DefaultModel: "claude-sonnet-4-6", Type: llm.ServiceTypeAnthropic},
		&model.Bot{UserId: botID, Username: "matty", DisplayName: "Matty"},
		lm,
	)})

	builderAPI := &plugintest.API{}
	mockLicenseState(builderAPI, true)
	for _, method := range []string{"LogDebug", "LogWarn", "LogError"} {
		for args := []any{mock.Anything}; len(args) <= 11; args = append(args, mock.Anything) {
			builderAPI.On(method, args...).Maybe().Return()
		}
	}
	contextBuilder := llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(builderAPI, nil),
		&channelFollowUpTestToolProvider{},
		&channelFollowUpTestMCPToolProvider{tools: []llm.Tool{tool}},
		&channelFollowUpTestConfig{},
	)

	convService := conversation.NewService(s, nil, nil, nil)
	c := &Conversations{
		mmClient:          mmClient,
		contextBuilder:    contextBuilder,
		bots:              botsService,
		convService:       convService,
		streamingService:  streamingService,
		licenseChecker:    licenseChecker,
		configProvider:    &channelFollowUpTestConfig{},
		toolPolicyChecker: mapPolicyChecker{},
	}
	// A dead node never heartbeats again; an hour keeps this one quiet.
	c.SetToolRunHeartbeatInterval(time.Hour)

	created, err := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channel.Id,
		RootPostID:   userPostID,
		Operation:    "conversation",
		SystemPrompt: "You are a helpful assistant.",
		UserMessage:  "@matty status of MM-1?",
		UserPostID:   new(userPostID),
	})
	require.NoError(t, err)
	conv := created.Conversation

	botPost := &model.Post{Id: botPostID, UserId: botID, ChannelId: channel.Id, Message: message}
	botPost.AddProp(streaming.ConversationIDProp, conv.ID)
	botPost.AddProp(streaming.RespondingToProp, userPostID)
	posts[botPostID] = botPost.Clone()
	content, err := json.Marshal([]conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: message},
		{
			Type: conversation.BlockTypeToolUse, ID: "toolu_1", Name: tool.Name, ServerOrigin: tool.ServerOrigin,
			Input: json.RawMessage(`{}`), Status: conversation.StatusPending,
		},
	})
	require.NoError(t, err)
	require.NoError(t, s.CreateTurnAutoSequence(&store.Turn{
		ID:             model.NewId(),
		ConversationID: conv.ID,
		PostID:         new(botPostID),
		Role:           "assistant",
		Content:        content,
	}))
	anchor := func() store.Turn {
		turn, lookupErr := s.GetTurnByPostID(botPostID)
		require.NoError(t, lookupErr)
		require.NotNil(t, turn)
		return *turn
	}
	anchorID := anchor().ID

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.HandleToolCall(ctx, userID, botPost.Clone(), channel, []string{"toolu_1"}, nil)
	}()
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "approved tool never started")
	}

	require.Equal(t, store.TurnStatusStreaming, anchor().Status, "the claim marks the turn streaming")
	err = c.HandleRegenerate(ctx, userID, storedPost(), channel)
	require.ErrorIs(t, err, streaming.ErrAlreadyStreamingToPost, "regenerate must not start a second run over a live approval run")

	// The node died a while ago: its last heartbeat is past the threshold.
	_, err = db.Exec(`UPDATE LLM_Turns SET UpdatedAt = UpdatedAt - $1 WHERE ID = $2`, 2*streaming.StaleStreamThreshold.Milliseconds(), anchorID)
	require.NoError(t, err)

	recovered, err := streaming.RecoverAbandonedStreams(ctx, s, sweepClient{mmClient}, streaming.DefaultRecoveryBatchSize)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	assert.Equal(t, botPostID, recovered[0].PostID)
	assert.Equal(t, store.TurnStatusInterrupted, anchor().Status)
	swept := storedPost()
	assert.Equal(t, streaming.InterruptedReasonLost, swept.GetProp(streaming.InterruptedProp))
	assert.Equal(t, message, swept.Message, "the sweep keeps the post message")

	// The tool finishes after the sweep declared the run lost.
	close(release)
	select {
	case err = <-errCh:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "HandleToolCall did not return")
	}
	require.ErrorIs(t, err, ErrToolRunInterrupted)

	final := anchor()
	assert.Equal(t, anchorID, final.ID)
	assert.Equal(t, store.TurnStatusInterrupted, final.Status, "a late completion must not overwrite the sweep's verdict")
	blocks, err := conversation.UnmarshalBlocks(final.Content)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, conversation.StatusInterrupted, blocks[1].Status, "the call is recorded as interrupted, neither resolved nor offered for approval again")
	turns, err := s.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	for _, turn := range turns {
		assert.NotEqual(t, "tool_result", turn.Role, "no results are saved for a lost run")
	}
	assert.Equal(t, conversation.ApprovalStageDone, conversation.ComputePostApprovalState(turns, botPostID))
	assert.Empty(t, anthropic.requests(), "no follow-up is streamed for a lost run")
	assert.Equal(t, streaming.InterruptedReasonLost, storedPost().GetProp(streaming.InterruptedProp))

	require.NoError(t, c.HandleRegenerate(ctx, userID, storedPost(), channel), "regenerate recovers an interrupted approval post")
	require.Eventually(t, func() bool {
		current, lookupErr := s.GetTurnByPostID(botPostID)
		return lookupErr == nil && current != nil && current.ID != anchorID && current.Status == store.TurnStatusComplete
	}, 10*time.Second, 20*time.Millisecond, "the regenerated response must finalize")
	requests := anthropic.requests()
	require.Len(t, requests, 1)
	assert.Empty(t, llmtest.AnthropicRequestViolations(requests[0]), "Anthropic would reject the regenerate request:\n%s", requests[0])
	assert.Nil(t, storedPost().GetProp(streaming.InterruptedProp), "the regenerated post is no longer interrupted")
	assert.Contains(t, string(requests[0]), conversation.InterruptedToolResult, "the regenerate request must tell the model the call's outcome is unknown")

	turns, err = s.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	var historyCalls int
	for _, turn := range turns {
		if turn.PostID != nil {
			continue
		}
		historyBlocks, blockErr := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, blockErr)
		for _, b := range historyBlocks {
			if b.Type == conversation.BlockTypeToolUse && b.Status == conversation.StatusInterrupted {
				historyCalls++
			}
		}
	}
	assert.Equal(t, 1, historyCalls, "the scrub keeps the interrupted call as history")

	regeneratedID := anchor().ID
	require.NoError(t, c.HandleRegenerate(ctx, userID, storedPost(), channel), "the regenerated response can be regenerated again")
	require.Eventually(t, func() bool {
		current, lookupErr := s.GetTurnByPostID(botPostID)
		return lookupErr == nil && current != nil && current.ID != regeneratedID && current.Status == store.TurnStatusComplete
	}, 10*time.Second, 20*time.Millisecond, "the second regenerate must finalize")
	requests = anthropic.requests()
	require.Len(t, requests, 2)
	assert.Contains(t, string(requests[1]), conversation.InterruptedToolResult, "a later regenerate still tells the model the call's outcome is unknown")
	assert.Empty(t, llmtest.AnthropicRequestViolations(requests[1]), "Anthropic would reject the second regenerate request:\n%s", requests[1])
}
