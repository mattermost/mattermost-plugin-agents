// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	policyToolOrigin    = "https://mcp.example.com"
	policyToolName      = "get_weather"
	policyDMChannelID   = "ardmchannel-id"
	policySecretChannel = "arsecret-channel-id"
)

var policyWeatherCall = llm.ToolCall{
	ID:           "tc-weather",
	Name:         policyToolName,
	Arguments:    json.RawMessage(`{"city":"NYC"}`),
	ServerOrigin: policyToolOrigin,
}

// policyEnv is an auto-reply test environment with a remote MCP weather tool
// that counts its executions and the agent's DM with autoReplyUserID.
type policyEnv struct {
	*autoReplyTestEnv
	dm         *model.Channel
	executions *atomic.Int32
}

func policyBotConfig() llm.BotConfig {
	cfg := autoReplyBotConfig()
	cfg.AutoEnableNewMCPTools = true
	return cfg
}

func setupPolicyEnv(t *testing.T, botConfigs []llm.BotConfig, llmResponses ...*llm.TextStreamResult) *policyEnv {
	t.Helper()
	env := setupAutoReplyTestEnv(t, botConfigs, llmResponses...)

	executions := &atomic.Int32{}
	env.mcpMgr.tools = []llm.Tool{{
		Name:         policyToolName,
		Description:  "Gets the weather",
		ServerOrigin: policyToolOrigin,
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			executions.Add(1)
			return "72F and sunny", nil
		},
	}}

	dm := &model.Channel{Id: policyDMChannelID, Type: model.ChannelTypeDirect, Name: autoReplyBotUserID + "__" + autoReplyUserID}
	env.mmClient.channels[dm.Id] = dm
	env.mmClient.posts = map[string]*model.Post{}
	env.streaming.finished = make(chan string, 8)

	return &policyEnv{autoReplyTestEnv: env, dm: dm, executions: executions}
}

func (env *policyEnv) waitForStream(t *testing.T) {
	t.Helper()
	select {
	case <-env.streaming.finished:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "response stream never finished")
	}
}

func (env *policyEnv) llmRequests() []llm.CompletionRequest {
	env.fakeLLM.mu.Lock()
	defer env.fakeLLM.mu.Unlock()
	return append([]llm.CompletionRequest(nil), env.fakeLLM.requests...)
}

// seedRun persists a conversation owned by autoReplyUserID in channel whose
// latest run was started by initiator and answered with botPost. toolUse is
// the content of botPost's assistant turn; toolResults, when set, become a
// tool_result turn after it. rootPost, when set, roots the thread instead of
// initiator.
func (env *policyEnv) seedRun(t *testing.T, channel *model.Channel, rootPost, initiator *model.Post, toolUse, toolResults []conversation.ContentBlock) *model.Post {
	t.Helper()
	if rootPost == nil {
		rootPost = initiator
	}
	initiator.ChannelId = channel.Id
	rootPost.ChannelId = channel.Id
	if initiator.Id != rootPost.Id {
		initiator.RootId = rootPost.Id
	}
	env.mmClient.posts[rootPost.Id] = rootPost
	env.mmClient.posts[initiator.Id] = initiator

	channelID := channel.Id
	rootID := rootPost.Id
	conv := &store.Conversation{
		ID:           model.NewId(),
		UserID:       autoReplyUserID,
		BotID:        autoReplyBotUserID,
		ChannelID:    &channelID,
		RootPostID:   &rootID,
		Operation:    "conversation",
		SystemPrompt: "system",
	}
	require.NoError(t, env.convStore.CreateConversation(conv))

	botPost := &model.Post{Id: model.NewId(), UserId: autoReplyBotUserID, ChannelId: channel.Id, RootId: rootID}
	botPost.AddProp(streaming.ConversationIDProp, conv.ID)
	botPost.AddProp(streaming.RespondingToProp, initiator.Id)
	env.mmClient.posts[botPost.Id] = botPost
	env.setThread(rootID, rootPost, initiator, botPost)

	userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: initiator.Message}})
	require.NoError(t, err)
	assistantContent, err := json.Marshal(toolUse)
	require.NoError(t, err)
	initiatorID := initiator.Id
	botPostID := botPost.Id
	turns := []store.Turn{
		{ID: model.NewId(), ConversationID: conv.ID, PostID: &initiatorID, Role: "user", Content: userContent, Sequence: 1},
		{ID: model.NewId(), ConversationID: conv.ID, PostID: &botPostID, Role: "assistant", Content: assistantContent, Sequence: 2},
	}
	if toolResults != nil {
		resultContent, marshalErr := json.Marshal(toolResults)
		require.NoError(t, marshalErr)
		turns = append(turns, store.Turn{ID: model.NewId(), ConversationID: conv.ID, Role: "tool_result", Content: resultContent, Sequence: 3})
	}
	for i := range turns {
		require.NoError(t, env.convStore.CreateTurn(&turns[i]))
	}
	return botPost
}

func pendingWeatherBlock() []conversation.ContentBlock {
	return []conversation.ContentBlock{{
		Type:         conversation.BlockTypeToolUse,
		ID:           policyWeatherCall.ID,
		Name:         policyWeatherCall.Name,
		ServerOrigin: policyWeatherCall.ServerOrigin,
		Input:        policyWeatherCall.Arguments,
		Status:       conversation.StatusPending,
		Shared:       new(false),
	}}
}

func executedWeatherBlocks() ([]conversation.ContentBlock, []conversation.ContentBlock) {
	toolUse := pendingWeatherBlock()
	toolUse[0].Status = conversation.StatusSuccess
	return toolUse, []conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: policyWeatherCall.ID,
		Content:   "72F and sunny",
		Status:    conversation.StatusSuccess,
		Shared:    new(false),
	}}
}

func humanPost(message string) *model.Post {
	return &model.Post{Id: model.NewId(), UserId: autoReplyUserID, Message: message, CreateAt: 100}
}

func botActivatePost(message string) *model.Post {
	post := &model.Post{Id: model.NewId(), UserId: autoReplyForeignBotID, Message: message, CreateAt: 200}
	post.AddProp(conversations.ActivateAIProp, "true")
	return post
}

// policyEnvState is the stored conversation state a denied turn must leave as
// it was.
type policyEnvState struct {
	conversations map[string]store.Conversation
	turns         map[string][]store.Turn
}

func (env *policyEnv) state() policyEnvState {
	convs, turns := env.convStore.snapshot()
	return policyEnvState{conversations: convs, turns: turns}
}

// requireNothingRan asserts a denied turn called no LLM, ran no tool, and
// left every stored conversation and turn as it was.
func requireNothingRan(t *testing.T, env *policyEnv, before policyEnvState) {
	t.Helper()
	assert.Empty(t, env.llmRequests(), "a denied turn must not call the LLM")
	assert.Zero(t, env.executions.Load(), "a denied turn must not run tools")
	assert.Equal(t, before, env.state(), "a denied turn must not change any conversation")
}

// TestDMMentionOfTheDMAgentIsADMTurn pins that @-mentioning an agent inside
// its own DM runs a DM turn with DM tool rules, while mentioning a different
// agent in that DM still runs a channel-style mention turn.
func TestDMMentionOfTheDMAgentIsADMTurn(t *testing.T) {
	tests := []struct {
		name           string
		message        string
		wantExecutions int32
	}{
		{name: "plain message", message: "what is the weather", wantExecutions: 1},
		{name: "message mentioning the DM's agent", message: "@" + autoReplyBotUsername + " what is the weather", wantExecutions: 1},
		{name: "message mentioning another agent", message: "@" + autoReplyBot2Username + " what is the weather", wantExecutions: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			second := autoReplySecondBotConfig()
			second.AutoEnableNewMCPTools = true
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig(), second},
				dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
				dmMakeTextStream("It's sunny"),
			)
			env.checker.setAutoRun(policyToolOrigin, policyToolName)

			post := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, Message: tc.message}
			env.setThread(post.Id, post)
			env.conversations.MessageHasBeenPosted(nil, post)
			env.waitForStream(t)

			assert.Equal(t, tc.wantExecutions, env.executions.Load())
		})
	}
}

// TestDMTurnInteractivityFollowsWhoStartedIt pins that a DM turn is
// interactive only when a person started it: a bot or plugin that posts in
// its DM with the agent gets no user-interaction tools, because nobody is
// there to answer them.
func TestDMTurnInteractivityFollowsWhoStartedIt(t *testing.T) {
	tests := []struct {
		name            string
		userID          string
		props           map[string]any
		wantInteractive bool
	}{
		{name: "a person", userID: autoReplyUserID, wantInteractive: true},
		{name: "a bot with activate_ai", userID: autoReplyForeignBotID, props: map[string]any{conversations.ActivateAIProp: true}},
		{name: "a plugin with activate_ai", userID: autoReplyUserID, props: map[string]any{conversations.FromPluginProp: "true", conversations.ActivateAIProp: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("answer"))
			dm := &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect, Name: autoReplyBotUserID + "__" + tc.userID}
			env.mmClient.channels[dm.Id] = dm

			post := &model.Post{Id: model.NewId(), UserId: tc.userID, ChannelId: dm.Id, Message: "what is the weather"}
			for key, value := range tc.props {
				post.AddProp(key, value)
			}
			env.setThread(post.Id, post)
			env.conversations.MessageHasBeenPosted(nil, post)
			env.waitForStream(t)

			requests := env.llmRequests()
			require.Len(t, requests, 1)
			assert.Equal(t, tc.wantInteractive, requests[0].Context.ToolCatalog.InteractiveUserPresent)
		})
	}
}

// TestBotRunWithToolCallingOffListsEachDisabledToolOnce pins that a channel
// run a bot started while channel tool calling is off tells the model about
// each unavailable tool once.
func TestBotRunWithToolCallingOffListsEachDisabledToolOnce(t *testing.T) {
	env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("answer"))
	env.toolProvider.tools = []llm.Tool{{
		Name:        "lookup_user",
		Description: "Looks up a user",
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return "", nil
		},
	}}
	post := env.rootPost(autoReplyForeignBotID, "@"+autoReplyBotUsername+" what is the weather")
	post.AddProp(conversations.ActivateAIProp, true)

	env.conversations.MessageHasBeenPosted(nil, post)
	env.waitForStream(t)

	requests := env.llmRequests()
	require.Len(t, requests, 1)
	names := make([]string, 0, len(requests[0].Context.DisabledTools))
	for _, info := range requests[0].Context.DisabledTools {
		names = append(names, info.Name)
	}
	assert.Contains(t, names, "lookup_user")
	assert.Len(t, names, len(slices.Compact(slices.Sorted(slices.Values(names)))), "disabled tools listed more than once: %v", names)
}

// TestToolApprovalUsesTheRunsActivation pins that what approving a tool call
// may run follows the post that started that run, not the thread root: a run
// a bot started with activate_ai cannot run tools that run would not have
// been offered, a run whose initiator is gone fails closed, failing to look
// the initiator up is an error, and a run with tool calling off (an automated
// invoker, or channel tool calling disabled) refuses the approval without
// changing it.
func TestToolApprovalUsesTheRunsActivation(t *testing.T) {
	errInitiatorLookup := errors.New("database unavailable")
	tests := []struct {
		name               string
		root               func() *model.Post
		initiator          func() *model.Post
		hideInitiator      bool
		initiatorErr       error
		channelToolCalling bool
		wantErr            error
		wantExecutions     int32
	}{
		{
			name:               "human run in a human thread",
			initiator:          func() *model.Post { return humanPost("what is the weather") },
			channelToolCalling: true,
			wantExecutions:     1,
		},
		{
			name:               "human run in a thread a bot started",
			root:               func() *model.Post { return botActivatePost("weather report please") },
			initiator:          func() *model.Post { return humanPost("what is the weather") },
			channelToolCalling: true,
			wantExecutions:     1,
		},
		{
			name:               "bot activate_ai run in a human thread",
			root:               func() *model.Post { return humanPost("hello") },
			initiator:          func() *model.Post { return botActivatePost("what is the weather") },
			channelToolCalling: true,
		},
		{
			name:               "run whose initiating post is gone",
			root:               func() *model.Post { return humanPost("hello") },
			initiator:          func() *model.Post { return humanPost("what is the weather") },
			hideInitiator:      true,
			channelToolCalling: true,
		},
		{
			name:               "run whose initiating post cannot be looked up",
			root:               func() *model.Post { return humanPost("hello") },
			initiator:          func() *model.Post { return humanPost("what is the weather") },
			initiatorErr:       errInitiatorLookup,
			channelToolCalling: true,
			wantErr:            errInitiatorLookup,
		},
		{
			name: "run a webhook started",
			initiator: func() *model.Post {
				post := humanPost("what is the weather")
				post.AddProp(conversations.FromWebhookProp, "true")
				return post
			},
			channelToolCalling: true,
			wantErr:            conversations.ErrToolCallingDisabled,
		},
		{
			name:      "human run with channel tool calling off",
			initiator: func() *model.Post { return humanPost("what is the weather") },
			wantErr:   conversations.ErrToolCallingDisabled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()})
			env.config.enableChannelMentionToolCalling = tc.channelToolCalling
			env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAsk)

			var root *model.Post
			if tc.root != nil {
				root = tc.root()
			}
			initiator := tc.initiator()
			botPost := env.seedRun(t, env.channel, root, initiator, pendingWeatherBlock(), nil)
			if tc.hideInitiator {
				delete(env.mmClient.posts, initiator.Id)
			}
			if tc.initiatorErr != nil {
				env.mmClient.postErrors = map[string]error{initiator.Id: tc.initiatorErr}
			}
			before := env.state()

			err := env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{policyWeatherCall.ID}, nil)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				requireNothingRan(t, env, before)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantExecutions, env.executions.Load())
		})
	}
}

const (
	policyThreadPostID = "arthread-post-id"

	analysisSourceDeniedEN = "I can't continue this conversation because you no longer have access to what it is about."
	genericLLMErrorEN      = "Sorry! An error occurred while accessing the LLM. See server logs for details."
)

// analysisSource is what a seeded analysis analyzed.
type analysisSource int

const (
	analyzedThread analysisSource = iota
	analyzedChannel
	analyzedNothingRecorded
)

// seedAnalysis persists a DM conversation that analyzed source, as
// produced by the thread or channel analysis routes, and returns its
// analysis post. Without a conversation the post only records who requested
// it, like legacy analysis posts.
func (env *policyEnv) seedAnalysis(t *testing.T, source analysisSource, withConversation bool) *model.Post {
	t.Helper()
	analysisPost := &model.Post{Id: model.NewId(), UserId: autoReplyBotUserID, ChannelId: env.dm.Id, Message: "summary of secret plans"}
	operation := llm.OperationChannelSummary
	switch source {
	case analyzedThread:
		analysisPost.AddProp(conversations.ThreadIDProp, policyThreadPostID)
		analysisPost.AddProp(conversations.AnalysisTypeProp, "summarize_thread")
		operation = llm.OperationThreadAnalysis
	case analyzedChannel:
		analysisPost.AddProp(conversations.AnalyzedChannelIDProp, policySecretChannel)
		analysisPost.AddProp(conversations.AnalysisTypeProp, "summarize_channel")
	case analyzedNothingRecorded:
		analysisPost.AddProp(conversations.AnalysisTypeProp, "summarize_channel")
	}
	env.mmClient.posts[analysisPost.Id] = analysisPost
	if !withConversation {
		analysisPost.AddProp(streaming.LLMRequesterUserIDProp, autoReplyUserID)
		return analysisPost
	}

	channelID := env.dm.Id
	analysisID := analysisPost.Id
	conv := &store.Conversation{
		ID:           model.NewId(),
		UserID:       autoReplyUserID,
		BotID:        autoReplyBotUserID,
		ChannelID:    &channelID,
		RootPostID:   &analysisID,
		Operation:    operation,
		SystemPrompt: "system",
	}
	require.NoError(t, env.convStore.CreateConversation(conv))
	analysisPost.AddProp(streaming.ConversationIDProp, conv.ID)
	userContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "summarize"}})
	require.NoError(t, err)
	summaryContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: analysisPost.Message}})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurn(&store.Turn{ID: model.NewId(), ConversationID: conv.ID, Role: "user", Content: userContent, Sequence: 1}))
	require.NoError(t, env.convStore.CreateTurn(&store.Turn{ID: model.NewId(), ConversationID: conv.ID, PostID: &analysisID, Role: "assistant", Content: summaryContent, Sequence: 2}))
	return analysisPost
}

// seedAnalysisFollowUpAnswer records a follow-up question in analysisPost's
// thread and the agent's answer to it, and returns the answer.
func (env *policyEnv) seedAnalysisFollowUpAnswer(t *testing.T, analysisPost *model.Post) *model.Post {
	t.Helper()
	convID, _ := analysisPost.GetProp(streaming.ConversationIDProp).(string)
	question := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, RootId: analysisPost.Id, Message: "what should we do next?"}
	answer := &model.Post{Id: model.NewId(), UserId: autoReplyBotUserID, ChannelId: env.dm.Id, RootId: analysisPost.Id, Message: "plan it"}
	answer.AddProp(streaming.ConversationIDProp, convID)
	answer.AddProp(streaming.RespondingToProp, question.Id)
	env.mmClient.posts[question.Id] = question
	env.mmClient.posts[answer.Id] = answer

	questionContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: question.Message}})
	require.NoError(t, err)
	answerContent, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: answer.Message}})
	require.NoError(t, err)
	questionID, answerID := question.Id, answer.Id
	require.NoError(t, env.convStore.CreateTurn(&store.Turn{ID: model.NewId(), ConversationID: convID, PostID: &questionID, Role: "user", Content: questionContent, Sequence: 3}))
	require.NoError(t, env.convStore.CreateTurn(&store.Turn{ID: model.NewId(), ConversationID: convID, PostID: &answerID, Role: "assistant", Content: answerContent, Sequence: 4}))
	return answer
}

// seedLegacyAnalysisFollowUp records the ordinary conversation that the first
// reply to legacy analysisPost creates.
func (env *policyEnv) seedLegacyAnalysisFollowUp(t *testing.T, analysisPost *model.Post) {
	t.Helper()
	channelID := env.dm.Id
	rootID := analysisPost.Id
	conv := &store.Conversation{
		ID:           model.NewId(),
		UserID:       autoReplyUserID,
		BotID:        autoReplyBotUserID,
		ChannelID:    &channelID,
		RootPostID:   &rootID,
		Operation:    llm.OperationConversation,
		SystemPrompt: "system",
	}
	require.NoError(t, env.convStore.CreateConversation(conv))
	content, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "first question"}})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurn(&store.Turn{ID: model.NewId(), ConversationID: conv.ID, Role: "user", Content: content, Sequence: 1}))
}

// TestAnalysisFollowUpRechecksSourceAccess pins that continuing or
// regenerating a thread or channel analysis re-checks that the user can still
// read what it analyzed, including when another agent is mentioned in the
// analysis thread. Losing access, the content being deleted, or the analysis
// not recording what it analyzed all deny without replaying the content to
// the LLM; failing to look the content up is an error, not a denial.
func TestAnalysisFollowUpRechecksSourceAccess(t *testing.T) {
	tests := []struct {
		name   string
		source analysisSource
		// noConversation seeds a legacy analysis post with no conversation.
		noConversation bool
		// repliedBefore, with noConversation, seeds the conversation an
		// earlier reply to the legacy analysis post created.
		repliedBefore bool
		// mentionOtherAgent asks the follow-up of a second agent by
		// mentioning it in the analysis thread.
		mentionOtherAgent bool
		unreadable        bool
		threadGone        bool
		lookupFails       bool
		// regenerate regenerates the analysis post, or with
		// regenerateFollowUp, the agent's answer to a follow-up, instead of
		// asking a follow-up.
		regenerate         bool
		regenerateFollowUp bool
		wantAnswered       bool
		wantPlaceholder    string
		wantErr            error
	}{
		{name: "thread follow-up while the thread is readable", source: analyzedThread, wantAnswered: true},
		{name: "thread follow-up after losing access to the thread", source: analyzedThread, unreadable: true, wantPlaceholder: analysisSourceDeniedEN},
		{name: "thread follow-up after the thread was deleted", source: analyzedThread, threadGone: true, wantPlaceholder: analysisSourceDeniedEN},
		{name: "thread follow-up when the thread cannot be looked up", source: analyzedThread, lookupFails: true, wantPlaceholder: genericLLMErrorEN},
		{name: "thread regenerate after losing access to the thread", source: analyzedThread, unreadable: true, regenerate: true, wantErr: conversations.ErrAnalysisSourceUnavailable},
		{name: "thread regenerate when the thread cannot be looked up", source: analyzedThread, lookupFails: true, regenerate: true},
		{name: "legacy thread regenerate after losing access to the thread", source: analyzedThread, noConversation: true, unreadable: true, regenerate: true, wantErr: conversations.ErrAnalysisSourceUnavailable},
		{name: "legacy thread follow-up after losing access to the thread", source: analyzedThread, noConversation: true, unreadable: true, wantPlaceholder: analysisSourceDeniedEN},
		{name: "later legacy thread follow-up while the thread is readable", source: analyzedThread, noConversation: true, repliedBefore: true, wantAnswered: true},
		{name: "later legacy thread follow-up after losing access to the thread", source: analyzedThread, noConversation: true, repliedBefore: true, unreadable: true, wantPlaceholder: analysisSourceDeniedEN},
		{name: "another agent asked in a thread analysis while the thread is readable", source: analyzedThread, mentionOtherAgent: true, wantAnswered: true},
		{name: "another agent asked in a thread analysis after losing access to the thread", source: analyzedThread, mentionOtherAgent: true, unreadable: true},
		{name: "channel follow-up while the channel is readable", source: analyzedChannel, wantAnswered: true},
		{name: "channel follow-up after losing access to the channel", source: analyzedChannel, unreadable: true, wantPlaceholder: analysisSourceDeniedEN},
		{name: "follow-up of an analysis that records no source", source: analyzedNothingRecorded, wantPlaceholder: analysisSourceDeniedEN},
		{name: "channel regenerate of the analysis", source: analyzedChannel, regenerate: true, wantErr: conversations.ErrNotRegenerable},
		{name: "channel regenerate of a follow-up answer after losing access", source: analyzedChannel, unreadable: true, regenerate: true, regenerateFollowUp: true, wantErr: conversations.ErrAnalysisSourceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig(), autoReplySecondBotConfig()}, dmMakeTextStream("follow-up answer"))
			if !tc.threadGone {
				env.mmClient.posts[policyThreadPostID] = &model.Post{Id: policyThreadPostID, ChannelId: policySecretChannel, UserId: autoReplyOtherUserID, Message: "secret plans"}
			}
			if tc.lookupFails {
				env.mmClient.postErrors = map[string]error{policyThreadPostID: errors.New("database unavailable")}
			}
			if tc.unreadable {
				env.mmClient.unreadableChannels = map[string]bool{policySecretChannel: true}
			}
			analysisPost := env.seedAnalysis(t, tc.source, !tc.noConversation)
			if tc.repliedBefore {
				env.seedLegacyAnalysisFollowUp(t, analysisPost)
			}

			if tc.regenerate {
				target := analysisPost
				if tc.regenerateFollowUp {
					target = env.seedAnalysisFollowUpAnswer(t, analysisPost)
				}
				err := env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, target, env.dm)
				require.Error(t, err)
				if tc.wantErr != nil {
					assert.ErrorIs(t, err, tc.wantErr)
				} else {
					assert.NotErrorIs(t, err, conversations.ErrAnalysisSourceUnavailable, "a failed lookup is not a denial")
				}
				assert.Empty(t, env.llmRequests(), "the analyzed content must not be replayed to the LLM")
				return
			}

			reply := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, RootId: analysisPost.Id, Message: "what should we do next?"}
			if tc.mentionOtherAgent {
				reply.Message = "@" + autoReplyBot2Username + " " + reply.Message
			}
			env.setThread(analysisPost.Id, analysisPost, reply)
			env.conversations.MessageHasBeenPosted(nil, reply)

			if tc.wantAnswered {
				env.waitForStream(t)
				assert.Len(t, env.llmRequests(), 1)
				return
			}
			assert.Empty(t, env.llmRequests(), "the analyzed content must not be replayed to the LLM")
			if tc.wantPlaceholder == "" {
				return
			}
			require.NotEmpty(t, env.mmClient.updatedPosts, "the placeholder must explain why there is no answer")
			placeholder := env.mmClient.updatedPosts[len(env.mmClient.updatedPosts)-1]
			assert.Equal(t, tc.wantPlaceholder, placeholder.Message)
		})
	}
}

const policyTimeToolName = "get_time"

// turnEntryPoint starts or resumes a turn the way one production entry point
// does. Every run asks about the weather or the time, so the LLM's canned
// responses apply to each.
type turnEntryPoint struct {
	name string
	dm   bool
	// returnsErr is set for entry points that report a denial to their
	// caller; message hooks only decline.
	returnsErr bool
	// toolDecision is set for approvals, rejections, answers, and shares of
	// a pending tool call.
	toolDecision bool
	// noAnswer is set for entry points whose turn ends waiting on the
	// requester instead of streaming an answer.
	noAnswer bool
	// autoReply is set for channel auto-reply, which needs Enterprise
	// Advanced and so never runs without the remote MCP license.
	autoReply bool
	// requester is who the turn runs for, when not autoReplyUserID.
	requester string
	// seed prepares what the entry point acts on and returns the call that
	// starts the turn.
	seed func(t *testing.T, env *policyEnv) func() error
}

func policyBot(env *policyEnv) *bots.Bot {
	return env.botService.GetBotByID(autoReplyBotUserID)
}

func postMessage(env *policyEnv, post *model.Post) func() error {
	return func() error {
		env.conversations.MessageHasBeenPosted(nil, post)
		return nil
	}
}

func dmMessage(env *policyEnv, message string) *model.Post {
	post := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, Message: message}
	env.setThread(post.Id, post)
	return post
}

func pendingTimeBlock() []conversation.ContentBlock {
	return []conversation.ContentBlock{{
		Type:         conversation.BlockTypeToolUse,
		ID:           "tc-time",
		Name:         llm.NamespaceMCPToolName("mattermost", policyTimeToolName),
		ServerOrigin: mcp.EmbeddedClientKey,
		Input:        json.RawMessage(`{}`),
		Status:       conversation.StatusPending,
		Shared:       new(false),
	}}
}

func pendingQuestionBlock() []conversation.ContentBlock {
	return []conversation.ContentBlock{{
		Type:            conversation.BlockTypeToolUse,
		ID:              "q-1",
		Name:            "AskUserQuestion",
		Input:           json.RawMessage(`{"question":"Which city?","options":[{"label":"NYC"},{"label":"Paris"}]}`),
		Status:          conversation.StatusPending,
		UserInteraction: llm.UserInteractionSelect,
		Shared:          new(false),
	}}
}

func earlierAnswer() []conversation.ContentBlock {
	return []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: "earlier answer"}}
}

var turnEntryPoints = []turnEntryPoint{
	{
		name: "DM message",
		dm:   true,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			return postMessage(env, dmMessage(env, "what is the weather"))
		},
	},
	{
		name: "@agent in its own DM",
		dm:   true,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			return postMessage(env, dmMessage(env, "@"+autoReplyBotUsername+" what is the weather"))
		},
	},
	{
		name: "channel mention",
		seed: func(_ *testing.T, env *policyEnv) func() error {
			return postMessage(env, env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" what is the weather"))
		},
	},
	{
		name:      "channel auto-reply",
		autoReply: true,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			env.settings.set(autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeRootPosts})
			return postMessage(env, env.rootPost(autoReplyUserID, "what is the weather"))
		},
	},
	{
		name:       "loop-in from the mention reminder",
		returnsErr: true,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			reply := env.threadReply(autoReplyUserID, "what is the weather", true)
			return func() error {
				return env.conversations.HandleLoopInAgent(context.Background(), autoReplyUserID, policyBot(env), reply, env.channel)
			}
		},
	},
	{
		name:      "bot activate_ai mention",
		requester: autoReplyForeignBotID,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			post := env.rootPost(autoReplyForeignBotID, "@"+autoReplyBotUsername+" what is the weather")
			post.AddProp(conversations.ActivateAIProp, true)
			return postMessage(env, post)
		},
	},
	{
		name:       "regenerate in a DM",
		dm:         true,
		returnsErr: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.dm, nil, humanPost("what is the weather"), earlierAnswer(), nil)
			return func() error {
				return env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.dm)
			}
		},
	},
	{
		name:       "regenerate in a channel",
		returnsErr: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), earlierAnswer(), nil)
			return func() error {
				return env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.channel)
			}
		},
	},
	{
		name:         "tool approve in a DM",
		dm:           true,
		returnsErr:   true,
		toolDecision: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.dm, nil, humanPost("what time is it"), pendingTimeBlock(), nil)
			return func() error {
				return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.dm, []string{"tc-time"}, nil)
			}
		},
	},
	{
		name:         "tool approve in a channel",
		returnsErr:   true,
		toolDecision: true,
		noAnswer:     true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.channel, nil, humanPost("what time is it"), pendingTimeBlock(), nil)
			return func() error {
				return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{"tc-time"}, nil)
			}
		},
	},
	{
		name:         "tool reject in a channel",
		returnsErr:   true,
		toolDecision: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), pendingWeatherBlock(), nil)
			return func() error {
				return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{}, nil)
			}
		},
	},
	{
		name:         "ask-user answer",
		returnsErr:   true,
		toolDecision: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), pendingQuestionBlock(), nil)
			answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"NYC"}}}
			return func() error {
				return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{"q-1"}, answers)
			}
		},
	},
	{
		name:         "WouldAutoExecute auto-resume",
		returnsErr:   true,
		toolDecision: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			marked := pendingWeatherBlock()
			marked[0].WouldAutoExecute = true
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), append(marked, pendingQuestionBlock()...), nil)
			answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"NYC"}}}
			return func() error {
				return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{"q-1"}, answers)
			}
		},
	},
	{
		name:         "share follow-up",
		returnsErr:   true,
		toolDecision: true,
		seed: func(t *testing.T, env *policyEnv) func() error {
			toolUse, toolResults := executedWeatherBlocks()
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), toolUse, toolResults)
			return func() error {
				return env.conversations.HandleToolResult(context.Background(), autoReplyUserID, botPost, env.channel, []string{policyWeatherCall.ID})
			}
		},
	},
	{
		name: "async wake",
		seed: func(t *testing.T, env *policyEnv) func() error {
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), earlierAnswer(), nil)
			scheduler := &capturingWakeScheduler{}
			env.conversations.SetWakeScheduler(scheduler, nil)
			require.NoError(t, env.conversations.ScheduleWake(botPost.Id, autoReplyUserID, time.Minute))
			require.Len(t, scheduler.jobs, 1)
			return func() error {
				return env.conversations.ResumeConversation(context.Background(), scheduler.jobs[0])
			}
		},
	},
	{
		name:       "search",
		dm:         true,
		returnsErr: true,
		seed: func(_ *testing.T, env *policyEnv) func() error {
			return func() error {
				_, err := env.conversations.HandleSearch(context.Background(), policyBot(env), env.mmClient.users[autoReplyUserID], conversations.SearchRequest{Query: "what is the weather"})
				return err
			}
		},
	},
}

type capturingWakeScheduler struct {
	jobs []conversations.WakeJob
}

func (s *capturingWakeScheduler) ScheduleOnce(_ string, _ time.Time, props any) (*cluster.JobOnce, error) {
	s.jobs = append(s.jobs, props.(conversations.WakeJob))
	return nil, nil
}

func (s *capturingWakeScheduler) Cancel(string) {}

// scopedPolicyBotConfig is policyBotConfig in scoped channel mode with the
// weather and embedded tools enabled.
func scopedPolicyBotConfig() llm.BotConfig {
	cfg := policyBotConfig()
	cfg.AutoEnableNewMCPTools = false
	cfg.UseServiceAccountAuth = true
	cfg.ScopedChannelMode = true
	cfg.EnabledMCPTools = []llm.EnabledMCPTool{
		{ServerOrigin: policyToolOrigin, ToolName: llm.MCPServerToolWildcard},
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: llm.MCPServerToolWildcard},
	}
	return cfg
}

// scopedPolicyRecorder observes a policy environment's scoped sessions and
// the audit records and tool runs of its turns.
type scopedPolicyRecorder struct {
	minter   *scopedTestMinter
	toolRuns atomic.Int32

	mu      sync.Mutex
	records []*model.AuditRecord
}

func (r *scopedPolicyRecorder) auditRecords(event string) []*model.AuditRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*model.AuditRecord
	for _, rec := range r.records {
		if rec.EventName == event {
			out = append(out, rec)
		}
	}
	return out
}

// enableScopedSessions lets env's agents mint scoped sessions and counts every
// tool run.
func (env *policyEnv) enableScopedSessions(t *testing.T) *scopedPolicyRecorder {
	t.Helper()
	rec := &scopedPolicyRecorder{minter: &scopedTestMinter{}}
	env.mockAPI.On("GetServerVersion").Return(scopedsession.MinServerVersion).Maybe()
	env.mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	env.mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()
	env.mockAPI.On("LogAuditRec", mock.Anything).Run(func(args mock.Arguments) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.records = append(rec.records, args.Get(0).(*model.AuditRecord))
	}).Maybe().Return()

	client := pluginapi.NewClient(env.mockAPI, nil)
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(rec.minter, scopedsession.NewRegistry(nil))
	env.mcpMgr.scoped = manager

	for i := range env.mcpMgr.tools {
		tool := &env.mcpMgr.tools[i]
		if tool.ServerOrigin == mcp.EmbeddedClientKey {
			tool.ReadOnly = true
		}
		resolve := tool.Resolver
		tool.Resolver = func(ctx context.Context, llmCtx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
			rec.toolRuns.Add(1)
			return resolve(ctx, llmCtx, args)
		}
	}
	return rec
}

// setupInvariantEnv is a policy environment with channel tool calling on, an
// embedded time tool and search tool beside the remote weather tool, and DMs
// for search created in the agent's DM.
func setupInvariantEnv(t *testing.T, cfg llm.BotConfig, llmResponses ...*llm.TextStreamResult) *policyEnv {
	t.Helper()
	env := setupPolicyEnv(t, []llm.BotConfig{cfg}, llmResponses...)
	env.config.enableChannelMentionToolCalling = true
	env.mcpMgr.tools = append(env.mcpMgr.tools,
		embeddedTestTool(policyTimeToolName, "noon"),
		embeddedTestTool("search_posts", "no results"),
	)
	env.checker.setPolicy(mcp.EmbeddedClientKey, policyTimeToolName, mcp.ToolPolicyAsk)
	env.mmClient.dmChannelID = env.dm.Id
	return env
}

// TestTurnEntryPointPolicyInvariants pins that every entry point that starts
// or resumes a turn applies the same turn policy: the requester's access to
// the agent, the multiplayer license for channel turns, the remote MCP
// license, channel tool calling, and DM-only auto-run tool policy. A turn
// that is denied leaves the conversation as it was.
func TestTurnEntryPointPolicyInvariants(t *testing.T) {
	for _, entry := range turnEntryPoints {
		t.Run(entry.name, func(t *testing.T) {
			t.Run("an agent that blocks the requester runs nothing", func(t *testing.T) {
				cfg := policyBotConfig()
				cfg.UserAccessLevel = llm.UserAccessLevelBlock
				cfg.UserIDs = []string{autoReplyUserID, autoReplyForeignBotID}
				env := setupInvariantEnv(t, cfg, dmMakeTextStream("answer"))
				start := entry.seed(t, env)
				before := env.state()

				err := start()

				if entry.returnsErr {
					require.ErrorIs(t, err, bots.ErrUsageRestriction)
				}
				requireNothingRan(t, env, before)
				assert.Empty(t, env.mmClient.createdPosts, "a blocked requester gets no reply")
				assert.Empty(t, env.mmClient.dmPosts, "a blocked requester gets no DM")
				assert.Zero(t, env.mmClient.directChannelRequests, "a blocked requester gets no DM channel")
			})

			t.Run("only DM turns run without the multiplayer license", func(t *testing.T) {
				env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
				env.overrideLicense(nil)
				start := entry.seed(t, env)
				before := env.state()

				err := start()

				if !entry.dm {
					if entry.returnsErr {
						licErr, ok := errors.AsType[*enterprise.LicenseError](err)
						require.True(t, ok, "want a license error, got %v", err)
						assert.Equal(t, enterprise.CapMultiplayerChannels, licErr.Capability)
					}
					requireNothingRan(t, env, before)
					return
				}
				require.NoError(t, err)
				env.waitForStream(t)
				assert.NotEmpty(t, env.llmRequests())
			})

			t.Run("a DM-only auto-run tool auto-runs only in DM turns", func(t *testing.T) {
				env := setupInvariantEnv(t, policyBotConfig(),
					dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
					dmMakeTextStream("It's sunny"),
				)
				env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAutoRunInDM)

				require.NoError(t, entry.seed(t, env)())
				if entry.noAnswer {
					assert.Empty(t, env.llmRequests())
					assert.Zero(t, env.executions.Load())
					return
				}
				env.waitForStream(t)

				require.NotEmpty(t, env.llmRequests())
				want := int32(0)
				if entry.dm {
					want = 1
				}
				assert.Equal(t, want, env.executions.Load())
			})

			t.Run("an unlicensed remote MCP tool never executes", func(t *testing.T) {
				env := setupInvariantEnv(t, policyBotConfig(),
					dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
					dmMakeTextStream("It's sunny"),
				)
				env.overrideLicense(&model.License{SkuShortName: model.LicenseShortSkuProfessional})
				env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAutoRunEverywhere)
				start := entry.seed(t, env)
				before := env.state()

				err := start()

				if entry.autoReply || errors.Is(err, conversations.ErrRemoteMCPNotLicensed) {
					requireNothingRan(t, env, before)
					return
				}
				require.NoError(t, err)
				if !entry.noAnswer {
					env.waitForStream(t)
				}
				assert.Zero(t, env.executions.Load())
			})

			if entry.dm {
				return
			}
			t.Run("channel tool calling off runs nothing", func(t *testing.T) {
				env := setupInvariantEnv(t, policyBotConfig(),
					dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
					dmMakeTextStream("It's sunny"),
				)
				env.config.enableChannelMentionToolCalling = false
				env.checker.setPolicy(policyToolOrigin, policyToolName, mcp.ToolPolicyAutoRunEverywhere)
				start := entry.seed(t, env)
				before := env.state()

				err := start()

				if entry.toolDecision {
					require.ErrorIs(t, err, conversations.ErrToolCallingDisabled)
					requireNothingRan(t, env, before)
					return
				}
				require.NoError(t, err)
				env.waitForStream(t)
				assert.Zero(t, env.executions.Load())
			})

			t.Run("a scoped agent runs every tool on sessions it revokes", func(t *testing.T) {
				env := setupInvariantEnv(t, scopedPolicyBotConfig(),
					dmMakeToolCallStream([]llm.ToolCall{policyWeatherCall}),
					dmMakeTextStream("It's sunny"),
				)
				scoped := env.enableScopedSessions(t)

				require.NoError(t, entry.seed(t, env)())
				if !entry.noAnswer {
					env.waitForStream(t)
				}

				require.Eventually(t, func() bool {
					minted, revoked := scoped.minter.snapshot()
					return len(minted) > 0 && len(revoked) == len(minted)
				}, 5*time.Second, 10*time.Millisecond, "every session the turn mints is revoked when its run ends")
				assert.Zero(t, env.mcpMgr.unscopedCatalogs.Load(), "a scoped turn never builds the requester's catalog")

				calls := scoped.auditRecords(audit.EventAgentScopedToolCall)
				require.NotEmpty(t, calls, "the turn runs a tool")
				assert.Len(t, calls, int(scoped.toolRuns.Load()), "every tool run is audited")
				requester := cmp.Or(entry.requester, autoReplyUserID)
				for _, call := range calls {
					assert.Equal(t, requester, call.Actor.UserId, "the record names the requester")
					encoded, err := json.Marshal(call)
					require.NoError(t, err)
					assert.NotContains(t, string(encoded), "NYC", "tool arguments never enter audit records")
				}
			})
		})
	}
}
