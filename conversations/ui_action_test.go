// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/genui"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	genUIRootID  = "root45678901234567890abcde"
	genUIReplyID = "reply5678901234567890abcde"
	genUIOtherID = "other5678901234567890abcde"
)

const genUIDeployReply = "Pick where to deploy.\n\n```openui-lang\n" +
	"root = Stack([form])\n" +
	"form = Form(\"deploy\", [env, note], [Button(\"Deploy\"), Button(\"Cancel\")])\n" +
	"env = Select(\"env\", \"Environment\", [\"staging\", \"production\"])\n" +
	"note = TextArea(\"note\", \"Reason\")\n" +
	"```"

func deploySubmission() genui.Submission {
	return genui.Submission{Form: "deploy", Label: "Deploy", Fields: []genui.Field{
		{Name: "env", Label: "Environment", Value: "staging"},
		{Name: "note", Label: "Reason", Value: "Ship the fix"},
	}}
}

type genUIReplyOptions struct {
	agentOptedOut bool
	replyBlocks   []conversation.ContentBlock
	laterTurn     bool
	noReplyTurn   bool
}

// setupGenUIReply seeds a conversation whose latest turn is the agent reply
// genUIReplyID, rooted at genUIRootID, and returns the reply post.
func setupGenUIReply(t *testing.T, env *dmTestEnv, channel *model.Channel, opts genUIReplyOptions) (string, *model.Post) {
	t.Helper()
	env.config.enableGenerativeUI = true
	env.botService.SetBotsForTesting([]*bots.Bot{bots.NewBot(
		llm.BotConfig{
			ID: env.botID, Name: "ai", DisplayName: "AI", AutoEnableNewMCPTools: true, MCPDynamicToolLoading: true,
			Skills: llm.SkillsConfig{GenerativeUI: !opts.agentOptedOut},
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: env.botID, Username: "ai", DisplayName: "AI"},
		env.fakeLLM,
	)})
	env.mmClient.assignPostIDs = true
	env.mmClient.markPluginPosts = true
	env.mmClient.channels[channel.Id] = channel

	rootID := genUIRootID
	channelID := channel.Id
	created, err := env.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootID,
		Operation:    llm.OperationConversation,
		SystemPrompt: "You are helpful.",
		UserMessage:  "Help me deploy",
		UserPostID:   &rootID,
	})
	require.NoError(t, err)

	blocks := opts.replyBlocks
	if blocks == nil {
		blocks = []conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: genUIDeployReply}}
	}
	sequence := 2
	if !opts.noReplyTurn {
		content, err := json.Marshal(blocks)
		require.NoError(t, err)
		replyID := genUIReplyID
		require.NoError(t, env.convStore.CreateTurn(&store.Turn{
			ID: model.NewId(), ConversationID: created.ConversationID, PostID: &replyID,
			Role: "assistant", Content: content, Sequence: sequence,
		}))
		sequence++
	}
	if opts.laterTurn {
		laterID := genUIOtherID
		require.NoError(t, env.convStore.CreateTurn(&store.Turn{
			ID: model.NewId(), ConversationID: created.ConversationID, PostID: &laterID,
			Role: "user", Content: json.RawMessage(`[{"type":"text","text":"never mind"}]`), Sequence: sequence,
		}))
	}

	reply := &model.Post{Id: genUIReplyID, UserId: env.botID, ChannelId: channel.Id, RootId: genUIRootID, Message: genUIDeployReply}
	reply.AddProp(streaming.ConversationIDProp, created.ConversationID)
	env.mmClient.posts = map[string]*model.Post{
		genUIRootID:  {Id: genUIRootID, UserId: env.userID, ChannelId: channel.Id, Message: "Help me deploy"},
		genUIReplyID: reply,
	}
	return created.ConversationID, reply
}

func waitForStreams(t *testing.T, env *dmTestEnv, n int) {
	t.Helper()
	for range n {
		select {
		case <-env.streamService.finished:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "timed out waiting for the response stream")
		}
	}
}

func submissionPosts(env *dmTestEnv) []*model.Post {
	var out []*model.Post
	for _, p := range env.mmClient.createdPosts {
		if p.GetProp(conversations.GenerativeUISubmissionProp) != nil {
			out = append(out, p)
		}
	}
	return out
}

func TestHandleUIActionRunsTheSubmissionAsATypedTurn(t *testing.T) {
	tests := []struct {
		name    string
		channel func(env *dmTestEnv) *model.Channel
		thread  bool
	}{
		{name: "direct message", channel: func(env *dmTestEnv) *model.Channel { return env.channel }},
		{name: "channel thread", thread: true, channel: func(*dmTestEnv) *model.Channel {
			return &model.Channel{Id: "chan45678901234567890abcde", Type: model.ChannelTypeOpen, TeamId: "team1"}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("Deploying to staging."))
			env.streamService.finished = make(chan string, 4)
			channel := tc.channel(env)
			convID, reply := setupGenUIReply(t, env, channel, genUIReplyOptions{})
			if tc.thread {
				env.mmClient.users[env.botID] = &model.User{Id: env.botID, Username: "ai", IsBot: true}
				root := &model.Post{Id: genUIRootID, UserId: env.userID, ChannelId: channel.Id, Message: "@ai help me deploy"}
				thread := &model.PostList{
					Order: []string{genUIRootID, genUIReplyID},
					Posts: map[string]*model.Post{genUIRootID: root, genUIReplyID: reply},
				}
				env.mmClient.postThreads = map[string]*model.PostList{genUIRootID: thread}
				env.mmClient.createPostErr = func(post *model.Post) error {
					thread.AddPost(post)
					thread.AddOrder(post.Id)
					return nil
				}
			}

			result, err := env.conversations.HandleUIAction(context.Background(), env.userID, reply, channel, conversations.UIActionRequest{
				Submission:     deploySubmission(),
				IdempotencyKey: "key-00000001",
			})
			require.NoError(t, err)
			waitForStreams(t, env, 1)

			posts := submissionPosts(env)
			require.Len(t, posts, 1)
			submission := posts[0]
			assert.Equal(t, result.PostID, submission.Id)
			assert.Equal(t, env.userID, submission.UserId, "the submission is posted as the requester")
			assert.Equal(t, channel.Id, submission.ChannelId)
			assert.Equal(t, genUIRootID, submission.RootId)
			assert.Equal(t, format.GenerativeUISubmission(deploySubmission()), submission.Message)
			record, ok := submission.GetProp(conversations.GenerativeUISubmissionProp).(map[string]any)
			require.True(t, ok)
			assert.Equal(t, genUIReplyID, record["source_post_id"])
			assert.Equal(t, "deploy", record["form"])

			turns := env.convStore.turnsFor(convID)
			var userTurn *store.Turn
			for i := range turns {
				if turns[i].Role == "user" && turns[i].PostID != nil && *turns[i].PostID == submission.Id {
					userTurn = &turns[i]
				}
			}
			require.NotNil(t, userTurn, "the submission becomes a user turn of the same conversation")
			blocks, err := conversation.UnmarshalBlocks(userTurn.Content)
			require.NoError(t, err)
			require.NotEmpty(t, blocks)
			assert.Equal(t, submission.Message, blocks[0].Text, "the model sees exactly what the thread shows")

			env.fakeLLM.mu.Lock()
			defer env.fakeLLM.mu.Unlock()
			require.Len(t, env.fakeLLM.requests, 1)
			req := env.fakeLLM.requests[0]
			last := req.Posts[len(req.Posts)-1]
			assert.Equal(t, llm.PostRoleUser, last.Role)
			assert.Equal(t, submission.Message, last.Message)
			assert.True(t, req.Context.ToolCatalog.InteractiveUserPresent,
				"the plugin-created submission post is the requester acting, not automation")
		})
	}
}

func TestHandleUIActionRefusals(t *testing.T) {
	pendingTool := []conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: genUIDeployReply},
		{Type: conversation.BlockTypeToolUse, ID: "tool-1", Name: "create_post", Status: conversation.StatusPending, Input: json.RawMessage(`{}`)},
	}

	tests := []struct {
		name       string
		opts       genUIReplyOptions
		userID     string
		disabled   bool
		cannotPost bool
		submission func() genui.Submission
		wantErr    error
	}{
		{name: "someone other than the requester", userID: genUIOtherID, wantErr: conversations.ErrNotRequester},
		{name: "requester cannot post in the channel", cannotPost: true, wantErr: conversations.ErrCannotPostInChannel},
		{name: "generative UI turned off server-wide", disabled: true, wantErr: conversations.ErrGenerativeUIDisabled},
		{name: "agent has not opted in to generative UI", opts: genUIReplyOptions{agentOptedOut: true}, wantErr: conversations.ErrGenerativeUIDisabled},
		{name: "reply still streaming", opts: genUIReplyOptions{noReplyTurn: true}, wantErr: conversations.ErrStaleUIAction},
		{name: "conversation moved on", opts: genUIReplyOptions{laterTurn: true}, wantErr: conversations.ErrStaleUIAction},
		{name: "reply waiting on a tool decision", opts: genUIReplyOptions{replyBlocks: pendingTool}, wantErr: conversations.ErrStaleUIAction},
		{name: "button the reply does not offer", submission: func() genui.Submission {
			s := deploySubmission()
			s.Label = "Delete production"
			return s
		}, wantErr: genui.ErrInvalidSubmission},
		{name: "block the reply does not have", submission: func() genui.Submission {
			s := deploySubmission()
			s.Block = 1
			return s
		}, wantErr: genui.ErrInvalidSubmission},
		{name: "malformed value", submission: func() genui.Submission {
			s := deploySubmission()
			s.Fields[0].Value = []any{"staging"}
			return s
		}, wantErr: genui.ErrInvalidSubmission},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("unused"))
			_, reply := setupGenUIReply(t, env, env.channel, tc.opts)
			env.config.enableGenerativeUI = !tc.disabled
			if tc.cannotPost {
				env.mmClient.unpostableChannels = map[string]bool{env.channel.Id: true}
			}
			userID := env.userID
			if tc.userID != "" {
				userID = tc.userID
			}
			submission := deploySubmission()
			if tc.submission != nil {
				submission = tc.submission()
			}

			_, err := env.conversations.HandleUIAction(context.Background(), userID, reply, env.channel, conversations.UIActionRequest{
				Submission:     submission,
				IdempotencyKey: "key-00000001",
			})
			require.ErrorIs(t, err, tc.wantErr)
			assert.Empty(t, env.mmClient.createdPosts, "a refused submission posts nothing")
			env.fakeLLM.mu.Lock()
			defer env.fakeLLM.mu.Unlock()
			assert.Empty(t, env.fakeLLM.requests, "a refused submission runs no turn")
		})
	}
}

func TestHandleUIActionSubmitsOnce(t *testing.T) {
	tests := []struct {
		name       string
		secondKey  string
		wantSecond error
	}{
		{name: "retry with the same idempotency key is a no-op", secondKey: "key-00000001"},
		{name: "second click is stale", secondKey: "key-00000002", wantSecond: conversations.ErrStaleUIAction},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("Deploying."), dmMakeTextStream("again"))
			env.streamService.finished = make(chan string, 4)
			_, reply := setupGenUIReply(t, env, env.channel, genUIReplyOptions{})

			first, err := env.conversations.HandleUIAction(context.Background(), env.userID, reply, env.channel, conversations.UIActionRequest{
				Submission:     deploySubmission(),
				IdempotencyKey: "key-00000001",
			})
			require.NoError(t, err)
			require.NotEmpty(t, first.PostID)
			waitForStreams(t, env, 1)

			second, err := env.conversations.HandleUIAction(context.Background(), env.userID, reply, env.channel, conversations.UIActionRequest{
				Submission:     deploySubmission(),
				IdempotencyKey: tc.secondKey,
			})
			if tc.wantSecond != nil {
				require.ErrorIs(t, err, tc.wantSecond)
			} else {
				require.NoError(t, err)
				assert.Empty(t, second.PostID)
			}
			assert.Len(t, submissionPosts(env), 1, "only one submission is posted")
			env.fakeLLM.mu.Lock()
			defer env.fakeLLM.mu.Unlock()
			assert.Len(t, env.fakeLLM.requests, 1, "only one turn runs")
		})
	}
}

func TestHandleUIActionRollsBackWhenTheTurnCannotStart(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Deploying."))
	env.streamService.finished = make(chan string, 4)
	_, reply := setupGenUIReply(t, env, env.channel, genUIReplyOptions{})

	failPlaceholder := true
	env.mmClient.createPostErr = func(post *model.Post) error {
		if failPlaceholder && post.UserId == env.botID {
			return errors.New("placeholder unavailable")
		}
		return nil
	}

	_, err := env.conversations.HandleUIAction(context.Background(), env.userID, reply, env.channel, conversations.UIActionRequest{
		Submission:     deploySubmission(),
		IdempotencyKey: "key-00000001",
	})
	require.Error(t, err)
	posts := submissionPosts(env)
	require.Len(t, posts, 1)
	assert.Equal(t, []string{posts[0].Id}, env.mmClient.deletedPostIDs, "the orphaned submission post is deleted")
	for key := range env.mmClient.kv {
		assert.False(t, strings.HasPrefix(key, "genui_submit_"), "the claim is released")
	}

	failPlaceholder = false
	result, err := env.conversations.HandleUIAction(context.Background(), env.userID, reply, env.channel, conversations.UIActionRequest{
		Submission:     deploySubmission(),
		IdempotencyKey: "key-00000002",
	})
	require.NoError(t, err, "the user can submit again")
	assert.NotEmpty(t, result.PostID)
	waitForStreams(t, env, 1)
}
