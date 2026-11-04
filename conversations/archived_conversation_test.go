// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// archiveAll archives every stored conversation, as the archive route does,
// and returns each one's user turn count.
func archiveAll(t *testing.T, env *policyEnv) map[string]int {
	t.Helper()
	turnCounts := map[string]int{}
	for _, conv := range allConversations(env.convStore) {
		env.convStore.archive(conv.ID)
		turnCounts[conv.ID] = userTurnCount(env, conv.ID)
	}
	return turnCounts
}

// userTurnCount counts the conversation's user turns: the history a resumed
// turn must keep (regenerate replaces the response it regenerates).
func userTurnCount(env *policyEnv, convID string) int {
	n := 0
	for _, turn := range env.convStore.turnsFor(convID) {
		if turn.Role == "user" {
			n++
		}
	}
	return n
}

// TestArchivedConversationEntryPoints pins that every entry point resuming a
// turn on an existing conversation still works after the conversation was
// archived (its posts stay visible), restoring the same conversation with its
// history, and that a turn its gates refuse leaves the conversation archived.
func TestArchivedConversationEntryPoints(t *testing.T) {
	for _, entry := range turnEntryPoints {
		t.Run(entry.name, func(t *testing.T) {
			t.Run("an accepted turn restores the archived conversation", func(t *testing.T) {
				env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
				start := entry.seed(t, env)
				turnCounts := archiveAll(t, env)
				if len(turnCounts) == 0 {
					t.Skip("this entry point starts a new conversation")
				}

				require.NoError(t, start())
				if !entry.noAnswer {
					env.waitForStream(t)
				}

				after := allConversations(env.convStore)
				require.Len(t, after, len(turnCounts), "the archived conversation must be continued, not replaced")
				for _, conv := range after {
					assert.Zero(t, conv.DeleteAt, "the conversation must be active again")
					assert.GreaterOrEqual(t, userTurnCount(env, conv.ID), turnCounts[conv.ID], "history is kept")
				}
			})

			t.Run("a refused turn leaves the conversation archived", func(t *testing.T) {
				cfg := policyBotConfig()
				cfg.UserAccessLevel = llm.UserAccessLevelBlock
				cfg.UserIDs = []string{autoReplyUserID, autoReplyForeignBotID}
				env := setupInvariantEnv(t, cfg, dmMakeTextStream("answer"))
				start := entry.seed(t, env)
				if len(archiveAll(t, env)) == 0 {
					t.Skip("this entry point starts a new conversation")
				}
				before := env.state()

				err := start()

				if entry.returnsErr {
					require.ErrorIs(t, err, bots.ErrUsageRestriction)
				}
				requireNothingRan(t, env, before)
			})
		})
	}
}

// TestDMReplyInArchivedAnalysisThread pins that a reply in an archived thread
// restores its conversation only once the turn passes its gates: a reply the
// analysis-source gate refuses must not un-archive the conversation.
func TestDMReplyInArchivedAnalysisThread(t *testing.T) {
	tests := []struct {
		name         string
		unreadable   bool
		wantRestored bool
	}{
		{name: "accepted reply restores the conversation", wantRestored: true},
		{name: "reply refused for lost source access stays archived", unreadable: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("follow-up answer"))
			env.mmClient.posts[policyThreadPostID] = &model.Post{Id: policyThreadPostID, ChannelId: policySecretChannel, UserId: autoReplyOtherUserID, Message: "secret plans"}
			if tc.unreadable {
				env.mmClient.unreadableChannels = map[string]bool{policySecretChannel: true}
			}
			analysisPost := env.seedAnalysis(t, analyzedThread, true)
			convID, _ := analysisPost.GetProp(streaming.ConversationIDProp).(string)
			env.convStore.archive(convID)
			archivedAt := env.convStore.deleteAt(convID)

			reply := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, RootId: analysisPost.Id, Message: "what should we do next?"}
			env.setThread(analysisPost.Id, analysisPost, reply)
			env.conversations.MessageHasBeenPosted(nil, reply)

			if tc.wantRestored {
				env.waitForStream(t)
				assert.Zero(t, env.convStore.deleteAt(convID), "an accepted reply continues the archived conversation")
				assert.Len(t, allConversations(env.convStore), 1, "no new conversation is started")
				return
			}
			assert.Empty(t, env.llmRequests())
			assert.Equal(t, archivedAt, env.convStore.deleteAt(convID), "a refused reply must not un-archive the conversation")
		})
	}
}

// TestArchiveDuringAcceptedDMTurn pins that a DM reply whose conversation the
// owner archives after the turn was accepted, but before the request is
// built, still answers, and that the run does not restore the conversation.
func TestArchiveDuringAcceptedDMTurn(t *testing.T) {
	env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("answer"))
	var archivedConvID string
	// The response placeholder is tagged with its conversation after the turn
	// is accepted and before processDMRequest loads the conversation.
	env.mmClient.onUpdatePost = func(post *model.Post) {
		convID, _ := post.GetProp(streaming.ConversationIDProp).(string)
		if convID != "" && archivedConvID == "" {
			archivedConvID = convID
			env.convStore.archive(convID)
		}
	}

	env.conversations.MessageHasBeenPosted(nil, dmMessage(env, "what is the weather"))
	env.waitForStream(t)

	require.NotEmpty(t, archivedConvID, "the conversation must have been archived mid-turn")
	assert.Len(t, env.llmRequests(), 1, "the accepted turn must still reach the LLM")
	assert.NotZero(t, env.convStore.deleteAt(archivedConvID), "the running turn must not restore the conversation")
}

// TestRegenerateAndToolDecisionRefuseDeletedRoot pins that regenerate and
// tool decisions refuse an active conversation whose root post was deleted,
// even though root deletion leaves the conversation row untouched.
func TestRegenerateAndToolDecisionRefuseDeletedRoot(t *testing.T) {
	entries := []struct {
		name string
		call func(t *testing.T, env *policyEnv) func() error
	}{
		{
			name: "regenerate",
			call: func(t *testing.T, env *policyEnv) func() error {
				botPost := env.seedRun(t, env.dm, nil, humanPost("what is the weather"), earlierAnswer(), nil)
				return func() error {
					return env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.dm)
				}
			},
		},
		{
			name: "tool approve",
			call: func(t *testing.T, env *policyEnv) func() error {
				botPost := env.seedRun(t, env.dm, nil, humanPost("what time is it"), pendingTimeBlock(), nil)
				return func() error {
					return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.dm, []string{"tc-time"}, nil)
				}
			},
		},
	}

	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
			start := entry.call(t, env)
			convs := allConversations(env.convStore)
			require.Len(t, convs, 1)
			require.NotNil(t, convs[0].RootPostID)
			env.mmClient.posts[*convs[0].RootPostID] = &model.Post{Id: *convs[0].RootPostID, DeleteAt: model.GetMillis()}
			before := env.state()

			require.ErrorIs(t, start(), conversation.ErrRootPostDeleted)
			requireNothingRan(t, env, before)
		})
	}
}

// TestRefusedResumeLeavesConversationArchived pins that a stale tool click, a
// repeated share click, or a regenerate that fails after its gates passed
// leaves an archived conversation archived, so a pending wait is never
// resumed unattended.
func TestRefusedResumeLeavesConversationArchived(t *testing.T) {
	tests := []struct {
		name    string
		wantErr error
		call    func(t *testing.T, env *policyEnv) func() error
	}{
		{
			name:    "stale tool click",
			wantErr: conversations.ErrStaleToolClick,
			call: func(t *testing.T, env *policyEnv) func() error {
				botPost := env.seedRun(t, env.dm, nil, humanPost("what time is it"), earlierAnswer(), nil)
				return func() error {
					return env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.dm, []string{"tc-time"}, nil)
				}
			},
		},
		{
			name: "repeated share click",
			call: func(t *testing.T, env *policyEnv) func() error {
				toolUse, toolResults := executedWeatherBlocks()
				botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), toolUse, toolResults)
				share := func() error {
					return env.conversations.HandleToolResult(context.Background(), autoReplyUserID, botPost, env.channel, []string{policyWeatherCall.ID})
				}
				require.NoError(t, share())
				env.waitForStream(t)
				return share
			},
		},
		{
			name: "failed regenerate",
			call: func(t *testing.T, env *policyEnv) func() error {
				botPost := env.seedRun(t, env.dm, nil, humanPost("what is the weather"), earlierAnswer(), nil)
				delete(botPost.Props, streaming.RespondingToProp)
				return func() error {
					return env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.dm)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
			resume := tt.call(t, env)
			require.NotEmpty(t, archiveAll(t, env))
			before := env.state()

			err := resume()

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			for _, conv := range allConversations(env.convStore) {
				assert.NotZero(t, conv.DeleteAt, "a refused resume must not un-archive the conversation")
			}
			assert.Equal(t, before, env.state())
		})
	}
}

// TestDMReplyInArchivedThreadNotRecorded pins that a reply whose user turn
// cannot be recorded leaves the archived conversation archived.
func TestDMReplyInArchivedThreadNotRecorded(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Follow-up response"))
	rootPostID := "root1"
	env.mmClient.posts = map[string]*model.Post{rootPostID: {Id: rootPostID}}
	channelID := env.channelID
	created, err := env.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful.",
		UserMessage:  "First message",
	})
	require.NoError(t, err)
	env.convStore.archive(created.ConversationID)
	archivedAt := env.convStore.deleteAt(created.ConversationID)
	env.convStore.appendErr = errors.New("db down")

	reply := &model.Post{Id: "post2", UserId: env.userID, ChannelId: env.channelID, RootId: rootPostID, Message: "Follow up"}
	_, err = env.conversations.CreateOrGetDMConversation(env.botID, env.user, env.channel, reply, nil)

	require.ErrorContains(t, err, "db down")
	assert.Equal(t, archivedAt, env.convStore.deleteAt(created.ConversationID), "an unrecorded reply must not un-archive the conversation")
}
