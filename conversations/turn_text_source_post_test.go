// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChannelUserTurnAnchorsToSourcePost pins the anchor every channel entry
// point that writes a user turn has to set: the turn records the ID of the
// post the channel member authored, and holds that post's text. Each case
// drives the production write path and inspects the turn it stored.
func TestChannelUserTurnAnchorsToSourcePost(t *testing.T) {
	tests := []struct {
		name string
		// run triggers the entry point and returns the post the user turn is
		// anchored to.
		run func(t *testing.T, env *autoReplyTestEnv) *model.Post
	}{
		{
			name: "explicit agent mention",
			run: func(t *testing.T, env *autoReplyTestEnv) *model.Post {
				post := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
				env.conversations.MessageHasBeenPosted(nil, post)
				return post
			},
		},
		{
			name: "channel auto-reply",
			run: func(t *testing.T, env *autoReplyTestEnv) *model.Post {
				env.settings.set(autoreply.Setting{
					ChannelID: autoReplyChannelID,
					BotID:     autoReplyBotUserID,
					Mode:      autoreply.ModeRootPosts,
				})
				post := env.rootPost(autoReplyUserID, "help me")
				env.conversations.MessageHasBeenPosted(nil, post)
				return post
			},
		},
		{
			name: "loop in agent",
			run: func(t *testing.T, env *autoReplyTestEnv) *model.Post {
				post := env.threadReply(autoReplyUserID, "help me", true)
				bot := env.botService.GetBotByID(autoReplyBotUserID)
				require.NotNil(t, bot)
				require.NoError(t, env.conversations.HandleLoopInAgent(
					context.Background(), autoReplyUserID, bot, post, env.channel))
				return post
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAutoReplyTestEnv(t,
				[]llm.BotConfig{autoReplyBotConfig()},
				dmMakeTextStream("canned reply"),
			)

			post := tt.run(t, env)

			turn := singleConversationUserTurn(t, env)
			require.NotNil(t, turn.PostID,
				"the user turn records the post it was written from")
			assert.Equal(t, post.Id, *turn.PostID,
				"the user turn records the ID of the post the channel member authored, not of a post derived from it")

			blocks, err := conversation.UnmarshalBlocks(turn.Content)
			require.NoError(t, err)
			assert.NotEmpty(t, conversation.TextContent(blocks),
				"the user turn holds the text of the post it is anchored to")
		})
	}
}

// singleConversationUserTurn asserts exactly one conversation exists and
// returns its opening user turn.
func singleConversationUserTurn(t *testing.T, env *autoReplyTestEnv) store.Turn {
	t.Helper()

	convs := allConversations(env.convStore)
	require.Len(t, convs, 1, "expected exactly one conversation")
	turns := env.convStore.turnsFor(convs[0].ID)
	require.NotEmpty(t, turns)
	require.Equal(t, "user", turns[0].Role)
	return turns[0]
}
