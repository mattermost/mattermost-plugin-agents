// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"errors"
	"net/http"
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

// TestDMTurnRefusesDeletedRoot pins that a DM reply on an active conversation
// whose root post was deleted runs nothing, records no user turn, and posts no
// placeholder reply in the deleted thread.
func TestDMTurnRefusesDeletedRoot(t *testing.T) {
	tests := []struct {
		name       string
		deleteRoot func(env *policyEnv, rootID string)
	}{
		{
			name: "root soft-deleted",
			deleteRoot: func(env *policyEnv, rootID string) {
				env.mmClient.posts[rootID] = &model.Post{Id: rootID, ChannelId: env.dm.Id, DeleteAt: model.GetMillis()}
			},
		},
		{
			name: "root permanently missing",
			deleteRoot: func(env *policyEnv, rootID string) {
				env.mmClient.gonePosts = map[string]bool{rootID: true}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("first answer"), dmMakeTextStream("second answer"))
			first := dmMessage(env, "what is the weather")
			env.conversations.MessageHasBeenPosted(nil, first)
			env.waitForStream(t)
			requestsBefore := len(env.llmRequests())
			postsBefore := len(env.mmClient.createdPosts)
			convs := allConversations(env.convStore)
			require.Len(t, convs, 1)
			turnsBefore := len(env.convStore.turnsFor(convs[0].ID))

			reply := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.dm.Id, RootId: first.Id, Message: "and tomorrow?"}
			env.setThread(first.Id, first, reply)
			tt.deleteRoot(env, first.Id)

			env.conversations.MessageHasBeenPosted(nil, reply)

			assert.Len(t, env.llmRequests(), requestsBefore, "a deleted thread must not reach the provider")
			assert.Len(t, env.convStore.turnsFor(convs[0].ID), turnsBefore, "no user turn is recorded")
			assert.Len(t, env.mmClient.createdPosts, postsBefore, "no placeholder reply is posted in a deleted thread")
		})
	}
}

// TestChannelMentionRefusesDeletedRoot pins that a channel mention replying to
// an active conversation whose root post was deleted records no user turn and
// never calls the provider.
func TestChannelMentionRefusesDeletedRoot(t *testing.T) {
	tests := []struct {
		name       string
		deleteRoot func(env *policyEnv, rootID string)
	}{
		{
			name: "root soft-deleted",
			deleteRoot: func(env *policyEnv, rootID string) {
				env.mmClient.posts[rootID] = &model.Post{Id: rootID, ChannelId: env.channel.Id, DeleteAt: model.GetMillis()}
			},
		},
		{
			name: "root permanently missing",
			deleteRoot: func(env *policyEnv, rootID string) {
				env.mmClient.gonePosts = map[string]bool{rootID: true}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupPolicyEnv(t, []llm.BotConfig{policyBotConfig()}, dmMakeTextStream("first answer"), dmMakeTextStream("second answer"))
			root := env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" hello")
			env.conversations.MessageHasBeenPosted(nil, root)
			env.waitForStream(t)
			requestsBefore := len(env.llmRequests())
			convs := allConversations(env.convStore)
			require.Len(t, convs, 1)
			turnsBefore := len(env.convStore.turnsFor(convs[0].ID))

			reply := &model.Post{Id: model.NewId(), UserId: autoReplyUserID, ChannelId: env.channel.Id, RootId: root.Id, Message: "@" + autoReplyBotUsername + " follow up"}
			env.setThread(root.Id, root, reply)
			tt.deleteRoot(env, root.Id)

			env.conversations.MessageHasBeenPosted(nil, reply)

			assert.Len(t, env.llmRequests(), requestsBefore, "a deleted thread must not reach the provider")
			assert.Len(t, env.convStore.turnsFor(convs[0].ID), turnsBefore, "no user turn is recorded")
		})
	}
}

// TestChannelToolFollowUpRefusesDeletedRoot pins that buildToolFollowUpRequest
// fails closed with ErrRootPostDeleted instead of silently falling back to
// turns-only context when the channel thread root is deleted after entry.
func TestChannelToolFollowUpRefusesDeletedRoot(t *testing.T) {
	tests := []struct {
		name           string
		deleteOnThread func(env *policyEnv, rootID string)
		wantAnyErr     bool
	}{
		{
			name: "thread lookup returns 404",
			deleteOnThread: func(env *policyEnv, rootID string) {
				env.mmClient.postThreadErrors = map[string]error{
					rootID: model.NewAppError("GetPostThread", "app.post.get.app_error", nil, "", http.StatusNotFound),
				}
			},
		},
		{
			name: "thread lookup errors and root post is soft-deleted",
			deleteOnThread: func(env *policyEnv, rootID string) {
				env.mmClient.postThreadErrors = map[string]error{rootID: errors.New("thread unavailable")}
				env.mmClient.posts[rootID] = &model.Post{Id: rootID, ChannelId: env.channel.Id, DeleteAt: model.GetMillis()}
			},
		},
		{
			name: "thread lookup errors and root post lookup fails",
			deleteOnThread: func(env *policyEnv, rootID string) {
				env.mmClient.postThreadErrors = map[string]error{rootID: errors.New("thread unavailable")}
				env.mmClient.postErrors = map[string]error{rootID: errors.New("post lookup failed")}
			},
			wantAnyErr: true,
		},
		{
			name: "thread lookup returns soft-deleted root post",
			deleteOnThread: func(env *policyEnv, rootID string) {
				deletedRoot := &model.Post{Id: rootID, ChannelId: env.channel.Id, UserId: autoReplyUserID, DeleteAt: model.GetMillis()}
				env.setThread(rootID, deletedRoot)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
			toolUse, toolResults := executedWeatherBlocks()
			botPost := env.seedRun(t, env.channel, nil, humanPost("what is the weather"), toolUse, toolResults)
			convs := allConversations(env.convStore)
			require.Len(t, convs, 1)
			require.NotNil(t, convs[0].RootPostID)
			rootID := *convs[0].RootPostID

			// Delete the root once the run reservation is claimed (after beginToolDecision's entry check).
			env.mmClient.onKVReserve = func(string) {
				tt.deleteOnThread(env, rootID)
			}

			err := env.conversations.HandleToolResult(context.Background(), autoReplyUserID, botPost, env.channel, []string{policyWeatherCall.ID})

			if tt.wantAnyErr {
				require.Error(t, err)
			} else {
				require.ErrorIs(t, err, conversation.ErrRootPostDeleted)
			}
			assert.Empty(t, env.llmRequests(), "a deleted thread must not reach the provider")
		})
	}
}

// TestRegenerateDeletedRootHandling pins that a root lookup failure before
// BuildRegenerateRequest leaves saved response turns intact, and that an
// active conversation does not fail post-run inside activateTurnConversation.
func TestRegenerateDeletedRootHandling(t *testing.T) {
	tests := []struct {
		name     string
		failRoot func(env *policyEnv, rootID string)
		wantErr  error
	}{
		{
			name: "root deleted before BuildRegenerateRequest preserves response turns",
			failRoot: func(env *policyEnv, rootID string) {
				env.mmClient.posts[rootID] = &model.Post{Id: rootID, ChannelId: env.dm.Id, DeleteAt: model.GetMillis()}
			},
			wantErr: conversation.ErrRootPostDeleted,
		},
		{
			name: "root lookup error before BuildRegenerateRequest preserves response turns",
			failRoot: func(env *policyEnv, rootID string) {
				env.mmClient.postErrors = map[string]error{rootID: errors.New("lookup failed")}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupInvariantEnv(t, policyBotConfig(), dmMakeTextStream("answer"))
			botPost := env.seedRun(t, env.dm, nil, humanPost("what is the weather"), earlierAnswer(), nil)
			convs := allConversations(env.convStore)
			require.Len(t, convs, 1)
			require.NotNil(t, convs[0].RootPostID)
			rootID := *convs[0].RootPostID
			before := env.state()

			// Fail the root lookup once reservePostRun succeeds (after entry gates, before BuildRegenerateRequest).
			env.mmClient.onKVReserve = func(string) {
				tt.failRoot(env, rootID)
			}

			err := env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.dm)

			require.Error(t, err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			requireNothingRan(t, env, before)
		})
	}
}
