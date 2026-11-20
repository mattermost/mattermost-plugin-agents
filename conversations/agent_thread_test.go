// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testThreadOperation = "test_thread"

// recordingOperationTools offers one auto-run tool and records its calls.
type recordingOperationTools struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingOperationTools) OperationTools(_ context.Context, bot *bots.Bot, _ *llm.Context) []llm.Tool {
	botUserID := bot.GetMMBot().UserId
	return []llm.Tool{{
		Name:        "op_tool",
		Description: "operation tool",
		Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
		AutoExecute: true,
		Resolver: func(_ context.Context, llmCtx *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.calls = append(r.calls, botUserID+":"+llmCtx.RequestingUser.Id)
			return "done", nil
		},
	}}
}

func (r *recordingOperationTools) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func requestToolNames(t *testing.T, env *dmTestEnv, index int) []string {
	t.Helper()
	env.fakeLLM.mu.Lock()
	defer env.fakeLLM.mu.Unlock()
	require.Greater(t, len(env.fakeLLM.requests), index)
	return visibleToolNames(env.fakeLLM.requests[index].Context)
}

func requestSkillsEnabled(t *testing.T, env *dmTestEnv, index int) bool {
	t.Helper()
	env.fakeLLM.mu.Lock()
	defer env.fakeLLM.mu.Unlock()
	require.Greater(t, len(env.fakeLLM.requests), index)
	return env.fakeLLM.requests[index].Context.ToolCatalog.SkillsEnabled
}

func userTurnTexts(t *testing.T, env *dmTestEnv, convID string) []string {
	t.Helper()
	var texts []string
	for _, turn := range env.convStore.turnsFor(convID) {
		if turn.Role != "user" {
			continue
		}
		var blocks []conversation.ContentBlock
		require.NoError(t, json.Unmarshal(turn.Content, &blocks))
		for _, block := range blocks {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

func TestStartAgentThread(t *testing.T) {
	tests := []struct {
		name            string
		postAsUser      bool
		wantRootAuthor  func(env *dmTestEnv) string
		wantFirstTurn   string
		wantInteractive bool
	}{
		{
			name:            "posted as the user, the root is the first turn and the user is present",
			postAsUser:      true,
			wantRootAuthor:  func(env *dmTestEnv) string { return env.userID },
			wantFirstTurn:   "Help me set this up",
			wantInteractive: true,
		},
		{
			name:           "posted by the agent, a hidden instruction is the first turn and the turn is unattended",
			wantRootAuthor: func(env *dmTestEnv) string { return env.botID },
			wantFirstTurn:  "Run your scheduled check-in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, bot := setupSearchTestEnv(t, nil, nil,
				dmMakeToolCallStream([]llm.ToolCall{{ID: "call_op", Name: "op_tool", Arguments: json.RawMessage(`{}`)}}),
				dmMakeTextStream("Started."),
			)
			tools := &recordingOperationTools{}
			env.conversations.RegisterOperation(testThreadOperation, tools)

			thread, err := env.conversations.StartAgentThread(context.Background(), conversations.AgentThreadRequest{
				Bot:         bot,
				User:        env.user,
				Operation:   testThreadOperation,
				Message:     "Help me set this up",
				PostAsUser:  tt.postAsUser,
				Instruction: "Run your scheduled check-in",
				RootProps:   map[string]any{"thread_kind": "test"},
				SystemPrompt: func(*llm.Context) (string, error) {
					return "thread system prompt", nil
				},
			})
			require.NoError(t, err)
			waitForStreamFinished(t, env)

			root := thread.RootPost
			assert.Equal(t, tt.wantRootAuthor(env), root.UserId)
			assert.Equal(t, env.channelID, root.ChannelId)
			assert.Equal(t, "Help me set this up", root.Message)
			assert.Equal(t, "test", root.GetProp("thread_kind"))

			conv := searchConversation(t, env, root.Id)
			assert.Equal(t, testThreadOperation, conv.Operation)
			assert.Equal(t, "thread system prompt", conv.SystemPrompt)
			assert.Equal(t, []string{tt.wantFirstTurn}, userTurnTexts(t, env, conv.ID))

			var response *model.Post
			for _, post := range env.mmClient.createdPosts {
				if post.UserId == env.botID && post.RootId == root.Id {
					response = post
				}
			}
			require.NotNil(t, response, "the agent answers in the thread")

			assert.Contains(t, requestToolNames(t, env, 0), "op_tool", "the operation's tools are offered on the first turn")
			assert.Equal(t, 1, tools.callCount(), "the operation's tool ran for the requester")
			assert.False(t, requestSkillsEnabled(t, env, 0), "user skills are not offered on an operation's first turn")

			env.fakeLLM.mu.Lock()
			interactive := env.fakeLLM.requests[0].Context.ToolCatalog.InteractiveUserPresent
			env.fakeLLM.mu.Unlock()
			assert.Equal(t, tt.wantInteractive, interactive)
		})
	}
}

func TestStartAgentThreadReplyContinuesWithOperationTools(t *testing.T) {
	env, bot := setupSearchTestEnv(t, nil, nil,
		dmMakeTextStream("What should I call myself?"),
		dmMakeTextStream("Got it."),
	)
	env.conversations.RegisterOperation(testThreadOperation, &recordingOperationTools{})

	thread, err := env.conversations.StartAgentThread(context.Background(), conversations.AgentThreadRequest{
		Bot:        bot,
		User:       env.user,
		Operation:  testThreadOperation,
		Message:    "Help me set this up",
		PostAsUser: true,
	})
	require.NoError(t, err)
	waitForStreamFinished(t, env)
	env.mmClient.posts = map[string]*model.Post{thread.RootPost.Id: thread.RootPost}

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        model.NewId(),
		UserId:    env.userID,
		ChannelId: env.channelID,
		RootId:    thread.RootPost.Id,
		Message:   "Call yourself Helper",
	})
	waitForStreamFinished(t, env)

	conv := searchConversation(t, env, thread.RootPost.Id)
	assert.Equal(t, []string{"Help me set this up", "Call yourself Helper"}, userTurnTexts(t, env, conv.ID))
	assert.Contains(t, requestToolNames(t, env, 1), "op_tool", "replies keep the operation's tools")
	assert.False(t, requestSkillsEnabled(t, env, 0), "no skills on the first turn")
	assert.False(t, requestSkillsEnabled(t, env, 1), "no skills on replies")
}

func TestOperationToolsOnlyInTheirOperation(t *testing.T) {
	env, _ := setupSearchTestEnv(t, nil, nil, dmMakeTextStream("Hello!"))
	env.conversations.RegisterOperation(testThreadOperation, &recordingOperationTools{})

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        model.NewId(),
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Hello bot",
	})
	waitForStreamFinished(t, env)

	assert.NotContains(t, requestToolNames(t, env, 0), "op_tool")
	assert.True(t, requestSkillsEnabled(t, env, 0), "ordinary conversations keep skills")
}

func TestStartAgentThreadRefusals(t *testing.T) {
	tests := []struct {
		name    string
		request func(env *dmTestEnv, bot *bots.Bot) conversations.AgentThreadRequest
		wantErr error
	}{
		{
			name: "a user who may not use the agent",
			request: func(env *dmTestEnv, _ *bots.Bot) conversations.AgentThreadRequest {
				restricted := bots.NewBot(
					llm.BotConfig{ID: env.botID, Name: "ai", DisplayName: "AI", UserAccessLevel: llm.UserAccessLevelBlock, UserIDs: []string{env.userID}},
					llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
					&model.Bot{UserId: env.botID, Username: "ai", DisplayName: "AI"},
					env.fakeLLM,
				)
				return conversations.AgentThreadRequest{Bot: restricted, User: env.user, Operation: testThreadOperation, Message: "hi", PostAsUser: true}
			},
			wantErr: bots.ErrUsageRestriction,
		},
		{
			name: "posting as the user outside their DM with the agent",
			request: func(env *dmTestEnv, bot *bots.Bot) conversations.AgentThreadRequest {
				channel := &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, Name: "town-square"}
				return conversations.AgentThreadRequest{Bot: bot, User: env.user, Channel: channel, Operation: testThreadOperation, Message: "hi", PostAsUser: true}
			},
			wantErr: conversations.ErrAgentThreadPostAsUserOutsideDM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, bot := setupSearchTestEnv(t, nil, nil)

			_, err := env.conversations.StartAgentThread(context.Background(), tt.request(env, bot))
			require.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, env.mmClient.dmPosts, "nothing is posted")
			assert.Empty(t, env.mmClient.createdPosts, "nothing is posted")
		})
	}
}
