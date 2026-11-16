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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type runEndRecorder struct {
	mu   sync.Mutex
	ends []notifications.StreamEnd
}

func (r *runEndRecorder) MarkDeferred(*model.Post) {}

func (r *runEndRecorder) Notify(_ context.Context, end notifications.StreamEnd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends = append(r.ends, end)
}

func (r *runEndRecorder) snapshot() []notifications.StreamEnd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notifications.StreamEnd(nil), r.ends...)
}

type toolFollowUpEnv struct {
	e         *TestEnvironment
	convStore *mockConvServiceStore
	notifier  *runEndRecorder
	post      *model.Post
	llm       *FakeLLM
}

// setupToolFollowUpEnv seeds a post with one tool batch and a bot whose LLM
// fails before any stream starts.
func setupToolFollowUpEnv(t *testing.T, channel *model.Channel, realStreaming bool, turns func(postID string) []store.Turn) *toolFollowUpEnv {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const (
		postID = "post12345678901234567890ab"
		convID = "conv12345678901234567890ab"
	)

	e := SetupTestEnvironment(t)
	t.Cleanup(func() { e.Cleanup(t) })
	var streamService *streaming.MMPostStreamService
	if realStreaming {
		streamService = streaming.NewMMPostStreamService(mmapi.NewClient(e.client), nil)
		e.api.streamingService = streamService
	} else {
		e.api.streamingService = &streamingContextRecorder{}
	}
	convStore := e.useConversations()
	if streamService != nil {
		streamService.SetTurnStore(convStore)
	}
	notifier := &runEndRecorder{}
	e.api.conversationsService.SetNotifier(notifier)
	e.config.enableChannelMentionToolCalling = true
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.OverrideLicense(&model.License{SkuShortName: "advanced"})

	fakeLLM := NewFakeLLM("unused")
	fakeLLM.Error = errors.New("llm unavailable")
	e.setBots(bots.NewBot(
		llm.BotConfig{ID: testBotUserID, Name: "thebot", DisplayName: "The Bot", UserAccessLevel: llm.UserAccessLevelAll, ChannelAccessLevel: llm.ChannelAccessLevelAll},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: testBotUserID, Username: "thebot"},
		fakeLLM,
	))

	require.NoError(t, convStore.CreateConversation(&store.Conversation{ID: convID, UserID: testUserID, BotID: testBotUserID, Operation: "conversation"}))
	userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "look up MM-1"}})
	require.NoError(t, err)
	require.NoError(t, convStore.CreateTurnAutoSequence(&store.Turn{ID: "user-turn", ConversationID: convID, Role: "user", Content: userContent}))
	for _, turn := range turns(postID) {
		turn.ConversationID = convID
		require.NoError(t, convStore.CreateTurnAutoSequence(&turn))
	}

	post := &model.Post{Id: postID, UserId: testBotUserID, ChannelId: channel.Id}
	post.AddProp(streaming.ConversationIDProp, convID)
	post.AddProp(model.PostPropsDeferredNotification, true)

	e.mockAPI.On("GetPost", postID).Return(post, nil)
	e.mockAPI.On("GetChannel", channel.Id).Return(channel, nil)
	e.mockAPI.On("GetTeam", "teamid").Return(&model.Team{Id: "teamid", Name: "team"}, nil).Maybe()
	e.mockAPI.On("HasPermissionToChannel", testUserID, channel.Id, model.PermissionReadChannel).Return(true)
	e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID, Username: "requester", Locale: "en"}, nil).Maybe()
	e.mockAPI.On("KVGet", mock.Anything).Return([]byte(nil), (*model.AppError)(nil)).Maybe()

	return &toolFollowUpEnv{e: e, convStore: convStore, notifier: notifier, post: post, llm: fakeLLM}
}

func (env *toolFollowUpEnv) click(endpoint, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/post/"+env.post.Id+"/"+endpoint, strings.NewReader(body))
	req.Header.Add("Mattermost-User-Id", testUserID)
	recorder := httptest.NewRecorder()
	env.e.api.ServeHTTP(&plugin.Context{}, recorder, req)
	return recorder.Result().StatusCode
}

func pendingToolBatch(t *testing.T, status string) []conversation.ContentBlock {
	t.Helper()
	return []conversation.ContentBlock{{
		Type:   conversation.BlockTypeToolUse,
		ID:     "tool-use-1",
		Name:   "jira__get_issue",
		Input:  []byte(`{"issue_key":"MM-1"}`),
		Status: status,
		Shared: new(true),
	}}
}

// TestToolFollowUpFailingBeforeStreamingNotifiesTheRequester drives the share
// decision route with an LLM that fails before any stream starts. The run
// never reaches the streaming service, which notifies for runs it streams, so
// the follow-up itself must end the run for the requester.
func TestToolFollowUpFailingBeforeStreamingNotifiesTheRequester(t *testing.T) {
	openChannel := &model.Channel{Id: testChannelID, Type: model.ChannelTypeOpen, TeamId: "teamid"}
	toolResult := []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: "tool-use-1",
		Content:   "issue details",
		Status:    conversation.StatusSuccess,
		Shared:    new(true),
	}}
	env := setupToolFollowUpEnv(t, openChannel, false, func(postID string) []store.Turn {
		return []store.Turn{
			assistantToolTurn(t, postID, pendingToolBatch(t, conversation.StatusSuccess)),
			toolResultTurn(t, toolResult),
		}
	})

	require.NotEqual(t, http.StatusOK, env.click("tool_result", `{"accepted_tool_ids":["tool-use-1"]}`), "the failed follow-up is reported to the requester")

	ends := env.notifier.snapshot()
	require.Len(t, ends, 1, "the failed follow-up notifies exactly once")
	assert.Equal(t, env.post.Id, ends[0].PostID)
	assert.Equal(t, testUserID, ends[0].RequesterID)
	assert.Equal(t, notifications.OutcomeErrored, ends[0].Outcome)
	assert.True(t, ends[0].PostDeferred)
}

// TestApprovalFailingAfterTheDecisionIsClaimedNotifiesTheRequester fails the
// store write for the tool result turn, which happens after the click claimed
// the pending tool calls and before the follow-up starts. The claim cannot be
// taken twice, so the requester must be told and a repeat click is stale.
func TestApprovalFailingAfterTheDecisionIsClaimedNotifiesTheRequester(t *testing.T) {
	dmChannel := &model.Channel{Id: "dmchan345678901234567890ab", Type: model.ChannelTypeDirect, Name: testBotUserID + "__" + testUserID}
	env := setupToolFollowUpEnv(t, dmChannel, false, func(postID string) []store.Turn {
		return []store.Turn{assistantToolTurn(t, postID, pendingToolBatch(t, conversation.StatusPending))}
	})
	env.convStore.createTurnErr = func(turn *store.Turn) error {
		if turn.Role == "tool_result" {
			return errors.New("store unavailable")
		}
		return nil
	}

	body := `{"accepted_tool_ids":["not-a-pending-tool"]}`
	require.Equal(t, http.StatusInternalServerError, env.click("tool_call", body))

	ends := env.notifier.snapshot()
	require.Len(t, ends, 1, "the failed approval notifies exactly once")
	assert.Equal(t, env.post.Id, ends[0].PostID)
	assert.Equal(t, testUserID, ends[0].RequesterID)
	assert.Equal(t, notifications.OutcomeErrored, ends[0].Outcome)
	assert.True(t, ends[0].PostDeferred)

	env.convStore.createTurnErr = nil
	require.Equal(t, http.StatusBadRequest, env.click("tool_call", body), "a repeat click is stale: the decision was already claimed")
	assert.Len(t, env.notifier.snapshot(), 1, "a stale click does not notify again")
}

// TestStopBeforeTheFollowUpStreamsNotifiesNobody stops the run through the
// real stop route while the approval's follow-up is still waiting on the LLM,
// before any stream took over. The requester stopped it, so nobody is
// notified, as for a stop during streaming.
func TestStopBeforeTheFollowUpStreamsNotifiesNobody(t *testing.T) {
	dmChannel := &model.Channel{Id: "dmchan345678901234567890ab", Type: model.ChannelTypeDirect, Name: testBotUserID + "__" + testUserID}
	env := setupToolFollowUpEnv(t, dmChannel, true, func(postID string) []store.Turn {
		return []store.Turn{assistantToolTurn(t, postID, pendingToolBatch(t, conversation.StatusPending))}
	})
	inLLM := make(chan struct{})
	var once sync.Once
	env.llm.OnContext = func(ctx context.Context) {
		once.Do(func() { close(inLLM) })
		<-ctx.Done()
	}

	approved := make(chan int, 1)
	go func() {
		approved <- env.click("tool_call", `{"accepted_tool_ids":["not-a-pending-tool"]}`)
	}()
	select {
	case <-inLLM:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the follow-up never reached the LLM")
	}

	require.Equal(t, http.StatusOK, env.click("stop", ""))
	select {
	case status := <-approved:
		require.Equal(t, http.StatusInternalServerError, status)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the approval kept running after the stop")
	}

	ends := env.notifier.snapshot()
	require.Len(t, ends, 1)
	assert.Equal(t, notifications.OutcomeStoppedByUser, ends[0].Outcome)
	assert.Equal(t, notifications.DeliveryNone, notifications.Decide(ends[0]))
}
