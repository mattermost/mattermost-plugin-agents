// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func assertInitialResponsePlaceholder(t *testing.T, post *model.Post, botID, requesterID, channelID, rootID, respondingToID string) {
	t.Helper()
	require.Equal(t, "custom_llmbot", post.Type)
	require.Empty(t, post.Message)
	require.Equal(t, botID, post.UserId)
	require.Equal(t, channelID, post.ChannelId)
	require.Equal(t, rootID, post.RootId)
	require.Equal(t, respondingToID, post.GetProp(streaming.RespondingToProp))
	require.Equal(t, requesterID, post.GetProp(streaming.LLMRequesterUserIDProp))
	require.Equal(t, "true", post.GetProp(streaming.UnsafeLinksPostProp))
	require.Empty(t, post.GetProp(streaming.ConversationIDProp))
}

func assertConversationAttachedBeforeLLM(t *testing.T, client *fakeMMClient) {
	t.Helper()
	require.NotEmpty(t, client.updatedPosts, "conversation_id must be persisted before the LLM is invoked")
	require.NotEmpty(t, client.updatedPosts[0].GetProp(streaming.ConversationIDProp))
}

func assertResponseProgressEvents(t *testing.T, client *fakeMMClient, channelID string) {
	t.Helper()
	expectedPhases := []string{
		"checking_mcp",
		"loading_conversation",
		"preparing_request",
		"connecting_provider",
	}
	require.Len(t, client.websocketEvents, len(expectedPhases))
	for i, expectedPhase := range expectedPhases {
		event := client.websocketEvents[i]
		require.Equal(t, "postupdate", event.event)
		require.Equal(t, "progress", event.payload["control"])
		require.Equal(t, expectedPhase, event.payload["progress_phase"])
		require.Equal(t, i+1, event.payload["progress_seq"])
		require.Equal(t, channelID, event.broadcast.ChannelId)
		require.True(t, event.broadcast.ReliableClusterSend)
	}
}

func TestMessagePostedCreatesChannelPlaceholderBeforeMCPDiscovery(t *testing.T) {
	tests := []struct {
		name           string
		configure      func(*autoReplyTestEnv)
		post           func(*autoReplyTestEnv) *model.Post
		requesterID    string
		rootID         string
		respondingToID string
	}{
		{
			name: "explicit mention",
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
			},
			requesterID:    autoReplyUserID,
			rootID:         autoReplyRootID,
			respondingToID: autoReplyRootID,
		},
		{
			name: "auto reply",
			configure: func(env *autoReplyTestEnv) {
				env.settings.set(autoreply.Setting{
					ChannelID: autoReplyChannelID,
					BotID:     autoReplyBotUserID,
					Mode:      autoreply.ModeRootPosts,
				})
			},
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "help me")
			},
			requesterID:    autoReplyUserID,
			rootID:         autoReplyRootID,
			respondingToID: autoReplyRootID,
		},
		{
			name: "thread mention",
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "@"+autoReplyBotUsername+" help me", false)
			},
			requesterID:    autoReplyUserID,
			rootID:         autoReplyRootID,
			respondingToID: autoReplyReplyID,
		},
		{
			name: "bot activate ai mention",
			post: func(env *autoReplyTestEnv) *model.Post {
				post := env.rootPost(autoReplyForeignBotID, "@"+autoReplyBotUsername+" help me")
				post.AddProp(conversations.ActivateAIProp, true)
				return post
			},
			requesterID:    autoReplyForeignBotID,
			rootID:         autoReplyRootID,
			respondingToID: autoReplyRootID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()}, dmMakeTextStream("done"))
			if tt.configure != nil {
				tt.configure(env)
			}

			mcpInvoked := false
			env.mcpMgr.onGetTools = func() {
				mcpInvoked = true
				require.Len(t, env.mmClient.createdPosts, 1)
				assertInitialResponsePlaceholder(
					t,
					env.mmClient.createdPosts[0],
					autoReplyBotUserID,
					tt.requesterID,
					autoReplyChannelID,
					tt.rootID,
					tt.respondingToID,
				)
				require.Empty(t, allConversations(env.convStore), "placeholder must precede conversation persistence")
			}
			env.mmClient.onUpdatePost = func(*model.Post) {
				require.True(t, mcpInvoked)
			}

			llmInvoked := false
			env.fakeLLM.onChat = func() {
				llmInvoked = true
				assertConversationAttachedBeforeLLM(t, env.mmClient)
			}

			env.conversations.MessageHasBeenPosted(nil, tt.post(env))

			require.True(t, mcpInvoked)
			require.True(t, llmInvoked)
			assertResponseProgressEvents(t, env.mmClient, autoReplyChannelID)
		})
	}
}

func TestMessagePostedCreatesDMPlaceholderBeforeMCPDiscovery(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("done"))

	mcpInvoked := false
	env.mcpMgr.onGetTools = func() {
		mcpInvoked = true
		require.Len(t, env.mmClient.createdPosts, 1)
		assertInitialResponsePlaceholder(
			t,
			env.mmClient.createdPosts[0],
			env.botID,
			env.userID,
			env.channelID,
			"post1",
			"post1",
		)
		require.Empty(t, allConversations(env.convStore), "placeholder must precede conversation persistence")
	}
	env.mmClient.onUpdatePost = func(*model.Post) {
		require.True(t, mcpInvoked)
	}

	llmInvoked := false
	env.fakeLLM.onChat = func() {
		llmInvoked = true
		assertConversationAttachedBeforeLLM(t, env.mmClient)
	}

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "help me",
	})

	require.True(t, mcpInvoked)
	require.True(t, llmInvoked)
	assertResponseProgressEvents(t, env.mmClient, env.channelID)
}

func TestMessagePostedDoesNotCreatePlaceholderWhenRejected(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T) *fakeMMClient
	}{
		{
			name: "non-trigger channel post",
			run: func(t *testing.T) *fakeMMClient {
				env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()})
				env.conversations.MessageHasBeenPosted(nil, env.rootPost(autoReplyUserID, "not a mention"))
				return env.mmClient
			},
		},
		{
			name: "channel usage restriction",
			run: func(t *testing.T) *fakeMMClient {
				cfg := autoReplyBotConfig()
				cfg.ChannelAccessLevel = llm.ChannelAccessLevelNone
				env := setupAutoReplyTestEnv(t, []llm.BotConfig{cfg})
				env.conversations.MessageHasBeenPosted(nil, env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help"))
				return env.mmClient
			},
		},
		{
			name: "DM user usage restriction",
			run: func(t *testing.T) *fakeMMClient {
				env := setupDMTestEnv(t)
				env.botService.SetBotsForTesting([]*bots.Bot{
					bots.NewBot(
						llm.BotConfig{
							ID:                 env.botID,
							Name:               "ai",
							DisplayName:        "AI",
							ChannelAccessLevel: llm.ChannelAccessLevelAll,
							UserAccessLevel:    llm.UserAccessLevelNone,
						},
						llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
						&model.Bot{UserId: env.botID, Username: "ai", DisplayName: "AI"},
						env.fakeLLM,
					),
				})
				env.conversations.MessageHasBeenPosted(nil, &model.Post{
					Id:        "post1",
					UserId:    env.userID,
					ChannelId: env.channelID,
					Message:   "help",
				})
				return env.mmClient
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, tt.run(t).createdPosts)
		})
	}
}

func TestMessagePostedUpdatesPlaceholderOnSetupFailure(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T) *fakeMMClient
	}{
		{
			name: "channel thread setup fails",
			run: func(t *testing.T) *fakeMMClient {
				env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()})
				post := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help")
				env.mmClient.postThreads = nil
				env.conversations.MessageHasBeenPosted(nil, post)
				return env.mmClient
			},
		},
		{
			name: "DM provider setup fails",
			run: func(t *testing.T) *fakeMMClient {
				env := setupDMTestEnv(t)
				env.conversations.MessageHasBeenPosted(nil, &model.Post{
					Id:        "post1",
					UserId:    env.userID,
					ChannelId: env.channelID,
					Message:   "help",
				})
				return env.mmClient
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.run(t)
			require.Len(t, client.createdPosts, 1)
			require.Empty(t, client.createdPosts[0].Message)
			require.GreaterOrEqual(t, len(client.updatedPosts), 2)
			require.Contains(t, client.updatedPosts[len(client.updatedPosts)-1].Message, "An error occurred")
		})
	}
}

func TestMessagePostedUpdatesPlaceholderWhenConversationSetupFails(t *testing.T) {
	env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()})
	env.convStore.lookupErr = errors.New("conversation lookup failed")

	env.conversations.MessageHasBeenPosted(nil, env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help"))

	require.Len(t, env.mmClient.createdPosts, 1)
	require.Len(t, env.mmClient.updatedPosts, 1)
	require.Empty(t, env.mmClient.updatedPosts[0].GetProp(streaming.ConversationIDProp))
	require.Contains(t, env.mmClient.updatedPosts[0].Message, "An error occurred")
}
