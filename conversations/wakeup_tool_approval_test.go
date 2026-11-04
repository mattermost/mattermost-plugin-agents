// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm/llmtest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
)

const (
	wakeApprovalOrigin    = "https://github.example.com"
	wakeApprovalChannelID = "open-channel"
	wakeApprovalRootID    = "mention-post-id"
	wakeApprovalBotPostID = "bot-post-id"
	createPullRequestTool = "github__create_pull_request"
)

// channelWakeApprovalEnv is a channel thread in which the requester asked the
// agent to wait, wired with the production streaming service so every
// response the agent streams is persisted as the turn a click resolves.
type channelWakeApprovalEnv struct {
	convStore     *loadedStateFlowStore
	convService   *conversation.Service
	conv          *store.Conversation
	lm            *llmtest.ScriptedToolCallLLM
	kv            *kvFake
	c             *Conversations
	channel       *model.Channel
	createPRCalls atomic.Int32

	mu      sync.Mutex
	posts   map[string]*model.Post
	order   []string
	nextAt  int64
	wakeSeq int
}

func newChannelWakeApprovalEnv(t *testing.T) *channelWakeApprovalEnv {
	t.Helper()
	env := &channelWakeApprovalEnv{
		lm:      &llmtest.ScriptedToolCallLLM{},
		channel: &model.Channel{Id: wakeApprovalChannelID, TeamId: "team-id", Type: model.ChannelTypeOpen},
		posts:   map[string]*model.Post{},
	}

	createPR := channelFollowUpTestMCPTool(createPullRequestTool, wakeApprovalOrigin, "create a pull request")
	createPR.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
		env.createPRCalls.Add(1)
		return "created pull request #42", nil
	}
	tools := []llm.Tool{
		createPR,
		channelFollowUpTestMCPTool("github__get_pull_request", wakeApprovalOrigin, "get a pull request"),
	}

	env.convStore = newLoadedStateFlowStore()
	env.convService = conversation.NewService(env.convStore, nil, nil, nil)
	rootID := wakeApprovalRootID
	env.conv = &store.Conversation{
		ID:           "conv-id",
		UserID:       wakeUserID,
		BotID:        "bot-id",
		RootPostID:   &rootID,
		SystemPrompt: "system",
		Operation:    "conversation",
	}
	require.NoError(t, env.convStore.CreateConversation(env.conv))

	mmClient := mocks.NewMockClient(t)
	for _, method := range []string{"LogDebug", "LogWarn", "LogError"} {
		for args := []any{mock.Anything}; len(args) <= 11; args = append(args, mock.Anything) {
			mmClient.On(method, args...).Maybe().Return()
		}
	}
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})
	mmClient.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("UpdatePost", mock.Anything).Maybe().Return(nil)
	mmClient.On("GetChannel", wakeApprovalChannelID).Maybe().Return(env.channel, nil)
	mmClient.On("HasPermissionToChannel", wakeUserID, wakeApprovalChannelID, model.PermissionReadChannel).Maybe().Return(true)
	mmClient.On("GetUser", wakeUserID).Maybe().Return(&model.User{Id: wakeUserID, Username: "alice"}, nil)
	mmClient.On("GetUser", "bot-id").Maybe().Return(&model.User{Id: "bot-id", Username: "matty", IsBot: true}, nil)
	mmClient.EXPECT().GetPost(mock.Anything).RunAndReturn(func(id string) (*model.Post, error) {
		env.mu.Lock()
		defer env.mu.Unlock()
		if post, ok := env.posts[id]; ok {
			return post.Clone(), nil
		}
		return nil, model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound)
	}).Maybe()
	mmClient.EXPECT().GetPostThread(wakeApprovalRootID).RunAndReturn(func(string) (*model.PostList, error) {
		env.mu.Lock()
		defer env.mu.Unlock()
		list := model.NewPostList()
		for _, id := range env.order {
			list.AddPost(env.posts[id].Clone())
			list.AddOrder(id)
		}
		return list, nil
	}).Maybe()
	mmClient.On("CreatePost", mock.Anything).Maybe().Run(func(args mock.Arguments) {
		post := args.Get(0).(*model.Post)
		env.mu.Lock()
		post.Id = fmt.Sprintf("wake-post-%d", env.wakeSeq)
		env.wakeSeq++
		env.mu.Unlock()
		env.addPost(post)
	}).Return(nil)
	env.kv = newKVFake(mmClient)

	streamingService := streaming.NewMMPostStreamService(mmClient, i18n.Init())
	streamingService.SetTurnStore(env.convStore)

	env.c = &Conversations{
		mmClient:         mmClient,
		contextBuilder:   newChannelFollowUpTestBuilder(t, tools, &channelFollowUpTestConfig{}),
		bots:             passthroughBotsService(loadedStateBot(env.lm)),
		convService:      env.convService,
		streamingService: streamingService,
		licenseChecker:   enterprisetest.CheckerAt(enterprise.LevelEnterprise),
		configProvider:   &channelFollowUpTestConfig{enableChannelMentionToolCalling: true},
		toolPolicyChecker: mapPolicyChecker{
			wakeApprovalOrigin: {
				"create_pull_request": {policy: mcp.ToolPolicyAsk, enabled: true},
				"get_pull_request":    {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true},
			},
		},
	}

	mention := &model.Post{Id: wakeApprovalRootID, UserId: wakeUserID, ChannelId: wakeApprovalChannelID, Message: "@matty open the PR once CI is green"}
	env.addPost(mention)
	env.addTurn(t, "user", mention.Id, mention.Message)
	env.addBotPost(t, wakeApprovalBotPostID, "Waiting for CI.")
	return env
}

func (e *channelWakeApprovalEnv) addPost(post *model.Post) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextAt += 1000
	post.CreateAt, post.UpdateAt = e.nextAt, e.nextAt
	if post.Id != wakeApprovalRootID {
		post.RootId = wakeApprovalRootID
	}
	e.posts[post.Id] = post
	e.order = append(e.order, post.Id)
}

func (e *channelWakeApprovalEnv) post(id string) *model.Post {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.posts[id].Clone()
}

func (e *channelWakeApprovalEnv) addTurn(t *testing.T, role, postID, text string) {
	t.Helper()
	content, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: text}})
	require.NoError(t, err)
	turn := &store.Turn{ID: model.NewId(), ConversationID: e.conv.ID, Role: role, Content: content}
	if postID != "" {
		turn.PostID = new(postID)
	}
	require.NoError(t, e.convStore.CreateTurnAutoSequence(turn))
}

// addBotPost records a finished agent reply that called wait_for_async_work.
func (e *channelWakeApprovalEnv) addBotPost(t *testing.T, postID, text string) {
	t.Helper()
	post := &model.Post{Id: postID, UserId: "bot-id", ChannelId: wakeApprovalChannelID, Message: text}
	post.AddProp(streaming.ConversationIDProp, e.conv.ID)
	e.addPost(post)
	e.addTurn(t, "assistant", postID, text)
}

// addPriorWake records a wake that already fired from waitPostID and whose
// reply post waited again; it returns that reply post's ID.
func (e *channelWakeApprovalEnv) addPriorWake(t *testing.T) string {
	t.Helper()
	require.NoError(t, e.convService.AppendSyntheticUserTurn(e.conv.ID, conversation.SyntheticKindWake, mmtools.WakeUserMessage))
	e.mu.Lock()
	postID := fmt.Sprintf("prior-wake-post-%d", len(e.order))
	e.mu.Unlock()
	e.addBotPost(t, postID, "Still waiting for CI.")
	return postID
}

// fireWake resumes the wait recorded on waitPostID and returns the wake's
// reply post once its response is persisted.
func (e *channelWakeApprovalEnv) fireWake(t *testing.T, waitPostID string) *model.Post {
	t.Helper()
	waitPost := e.post(waitPostID)
	job := newWakeJob(waitPostID, e.conv.ID, wakeUserID, wakeScheduledAt)
	raw, err := json.Marshal(job.wakeMarker)
	require.NoError(t, err)
	e.kv.data[responsePostMarkerKey(waitPost)] = raw

	e.mu.Lock()
	wakePostID := fmt.Sprintf("wake-post-%d", e.wakeSeq)
	e.mu.Unlock()
	require.NoError(t, e.c.ResumeConversation(context.Background(), job))
	e.waitForAnchor(t, wakePostID, "")
	return e.post(wakePostID)
}

// waitForAnchor waits until postID's assistant turn exists and is not
// previousAnchorID, i.e. the stream onto postID has been finalized.
func (e *channelWakeApprovalEnv) waitForAnchor(t *testing.T, postID, previousAnchorID string) *store.Turn {
	t.Helper()
	var anchor *store.Turn
	require.Eventually(t, func() bool {
		turn, err := e.convStore.GetTurnByPostID(postID)
		if err != nil || turn == nil || turn.ID == previousAnchorID {
			return false
		}
		anchor = turn
		return true
	}, 10*time.Second, 10*time.Millisecond, "the stream onto %s must finalize", postID)
	return anchor
}

func (e *channelWakeApprovalEnv) toolUseBlock(t *testing.T, postID, toolUseID string) conversation.ContentBlock {
	t.Helper()
	turns, err := e.convStore.GetTurnsForConversation(e.conv.ID)
	require.NoError(t, err)
	for _, turn := range turns {
		blocks, err := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, err)
		for _, block := range blocks {
			if block.Type == conversation.BlockTypeToolUse && block.ID == toolUseID {
				return block
			}
		}
	}
	require.FailNow(t, "tool_use block not found", "post %s, tool use %s", postID, toolUseID)
	return conversation.ContentBlock{}
}

func (e *channelWakeApprovalEnv) toolResultContent(t *testing.T, toolUseID string) string {
	t.Helper()
	turns, err := e.convStore.GetTurnsForConversation(e.conv.ID)
	require.NoError(t, err)
	for _, turn := range turns {
		blocks, err := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, err)
		for _, block := range blocks {
			if block.Type == conversation.BlockTypeToolResult && block.ToolUseID == toolUseID {
				return block.Content
			}
		}
	}
	require.FailNow(t, "tool_result block not found", "tool use %s", toolUseID)
	return ""
}

// shareAndFollowUp shares the wake post's tool results and returns the
// completion request of the follow-up that sharing streams.
func (e *channelWakeApprovalEnv) shareAndFollowUp(t *testing.T, wakePost *model.Post, toolUseID string) llm.CompletionRequest {
	t.Helper()
	anchor, err := e.convStore.GetTurnByPostID(wakePost.Id)
	require.NoError(t, err)
	before := len(e.lm.Recorded())
	require.NoError(t, e.c.HandleToolResult(context.Background(), wakeUserID, wakePost, e.channel, []string{toolUseID}))
	e.waitForAnchor(t, wakePost.Id, anchor.ID)
	requests := e.lm.Recorded()
	require.Len(t, requests, before+1, "sharing must stream exactly one follow-up")
	return requests[before]
}

// TestApproveToolCallOnWakePost reproduces a channel wake whose resumed turn
// loads an MCP tool that needs approval and calls it: the requester's approval
// on the wake post must execute the call, and the follow-up after sharing must
// still offer the tools of the requester's run, however many wakes deep.
func TestApproveToolCallOnWakePost(t *testing.T) {
	tests := []struct {
		name       string
		priorWakes int
	}{
		{name: "first wake", priorWakes: 0},
		{name: "consecutive wakes", priorWakes: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newChannelWakeApprovalEnv(t)
			waitPostID := wakeApprovalBotPostID
			for range tt.priorWakes {
				waitPostID = env.addPriorWake(t)
			}
			env.lm.SetScript([][]llm.ToolCall{
				{{ID: "load-1", Name: mcp.LoadToolName, Arguments: json.RawMessage(`{"name":"` + createPullRequestTool + `"}`)}},
				{{ID: "pr-1", Name: createPullRequestTool, Arguments: json.RawMessage(`{}`)}},
			})

			wakePost := env.fireWake(t, waitPostID)
			require.Equal(t, conversation.StatusPending, env.toolUseBlock(t, wakePost.Id, "pr-1").Status, "the wake must stop on the call awaiting approval")

			require.NoError(t, env.c.HandleToolCall(context.Background(), wakeUserID, wakePost, env.channel, []string{"pr-1"}, nil))
			require.Equal(t, "created pull request #42", env.toolResultContent(t, "pr-1"))
			require.Equal(t, int32(1), env.createPRCalls.Load(), "the approved call must execute")
			require.Equal(t, conversation.StatusSuccess, env.toolUseBlock(t, wakePost.Id, "pr-1").Status)

			followUp := env.shareAndFollowUp(t, wakePost, "pr-1")
			require.Contains(t, channelFollowUpSearchToolNames(t, followUp.Context.Tools, "pull request"), createPullRequestTool,
				"the follow-up must offer the requester's tools, not only auto_run_everywhere ones")
		})
	}
}

// TestShareToolResultOnWakePostKeepsRequesterTools covers the share click on
// its own: a wake post whose approved call already ran must stream its
// follow-up with the requester's tool catalog.
func TestShareToolResultOnWakePostKeepsRequesterTools(t *testing.T) {
	env := newChannelWakeApprovalEnv(t)
	require.NoError(t, env.convService.AppendSyntheticUserTurn(env.conv.ID, conversation.SyntheticKindWake, mmtools.WakeUserMessage))

	wakePost := &model.Post{Id: "wake-post-executed", UserId: "bot-id", ChannelId: wakeApprovalChannelID, Message: "Opening the PR."}
	wakePost.AddProp(streaming.ConversationIDProp, env.conv.ID)
	env.addPost(wakePost)
	toolUse, err := json.Marshal([]conversation.ContentBlock{{
		Type:         conversation.BlockTypeToolUse,
		ID:           "pr-1",
		Name:         createPullRequestTool,
		ServerOrigin: wakeApprovalOrigin,
		Input:        json.RawMessage(`{}`),
		Status:       conversation.StatusSuccess,
		Shared:       new(false),
	}})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: env.conv.ID, PostID: new(wakePost.Id), Role: "assistant", Content: toolUse,
	}))
	toolResult, err := json.Marshal([]conversation.ContentBlock{{
		Type:      conversation.BlockTypeToolResult,
		ToolUseID: "pr-1",
		Content:   "created pull request #42",
		Status:    conversation.StatusSuccess,
		Shared:    new(false),
	}})
	require.NoError(t, err)
	require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
		ID: model.NewId(), ConversationID: env.conv.ID, Role: "tool_result", Content: toolResult,
	}))

	followUp := env.shareAndFollowUp(t, env.post(wakePost.Id), "pr-1")
	require.Contains(t, channelFollowUpSearchToolNames(t, followUp.Context.Tools, "pull request"), createPullRequestTool,
		"the follow-up must offer the requester's tools, not only auto_run_everywhere ones")
}
