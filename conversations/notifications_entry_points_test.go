// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureWakeScheduler records scheduled wakes instead of running them.
type captureWakeScheduler struct {
	mu   sync.Mutex
	jobs []conversations.WakeJob
}

func (s *captureWakeScheduler) ScheduleOnce(_ string, _ time.Time, props any) (*cluster.JobOnce, error) {
	job, ok := props.(conversations.WakeJob)
	if !ok {
		return nil, errors.New("unexpected wake props")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
	return nil, nil
}

func (s *captureWakeScheduler) Cancel(string) {}

// setupEntryPointWiring is setupNotificationWiring on a supporting server
// with channel tool calling on, the embedded time and search tools beside the
// remote weather tool, and search DMs created in the agent's DM.
func setupEntryPointWiring(t *testing.T, llmResponses ...*llm.TextStreamResult) *notificationWiring {
	t.Helper()
	w := setupNotificationWiring(t, true, llmResponses...)
	w.config.enableChannelMentionToolCalling = true
	w.mcpMgr.tools = append(w.mcpMgr.tools,
		embeddedTestTool(policyTimeToolName, "noon"),
		embeddedTestTool("search_posts", "no results"),
	)
	w.checker.setPolicy(mcp.EmbeddedClientKey, policyTimeToolName, mcp.ToolPolicyAsk)
	w.mmClient.dmChannelID = w.dm.Id
	return w
}

// pausedRun seeds a run whose bot post is waiting for the requester, so the
// server still holds its notifications deferred.
func (w *notificationWiring) pausedRun(t *testing.T, channel *model.Channel, toolUse, toolResults []conversation.ContentBlock) *model.Post {
	t.Helper()
	botPost := w.seedRun(t, channel, nil, humanPost("what is the weather"), toolUse, toolResults)
	botPost.AddProp(streaming.LLMRequesterUserIDProp, autoReplyUserID)
	botPost.AddProp(model.PostPropsDeferredNotification, true)
	return botPost
}

func (w *notificationWiring) createdPost(postID string) *model.Post {
	for _, p := range w.mmClient.createdPosts {
		if p.Id == postID {
			return p
		}
	}
	return nil
}

// TestEntryPointRunsNotifyOnceWhenTheyEnd runs each entry point that starts
// or resumes a turn through the real streaming and notifications services:
// every run notifies once, when it ends, as its ending calls for.
func TestEntryPointRunsNotifyOnceWhenTheyEnd(t *testing.T) {
	release := fakeDelivery{}
	toRequester := fakeDelivery{userIDs: []string{autoReplyUserID}}
	errUnavailable := errors.New("server unavailable")

	tests := []struct {
		name  string
		setup func(w *notificationWiring)
		// start starts the run and returns the existing post it resumes on,
		// or "" when the run answers on a new response post.
		start       func(t *testing.T, w *notificationWiring) string
		wantOutcome notifications.Outcome
		// wantDeferred is whether a new response post was created deferred.
		wantDeferred bool
		wantDelivery []fakeDelivery // postID filled in from the run's post
		wantPushes   []string
	}{
		{
			name: "search",
			start: func(t *testing.T, w *notificationWiring) string {
				_, err := w.conversations.HandleSearch(context.Background(), policyBot(w.policyEnv), w.mmClient.users[autoReplyUserID], conversations.SearchRequest{Query: "what is the weather"})
				require.NoError(t, err)
				return ""
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "wake",
			start: func(t *testing.T, w *notificationWiring) string {
				botPost := w.seedRun(t, w.dm, nil, humanPost("watch the job"), earlierAnswer(), nil)
				scheduler := &captureWakeScheduler{}
				w.conversations.SetWakeScheduler(scheduler, nil)
				require.NoError(t, w.conversations.ScheduleWake(botPost.Id, autoReplyUserID, time.Minute))
				require.Len(t, scheduler.jobs, 1)
				require.NoError(t, w.conversations.ResumeConversation(context.Background(), scheduler.jobs[0]))
				return ""
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "channel auto-reply",
			start: func(_ *testing.T, w *notificationWiring) string {
				w.settings.set(autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeRootPosts})
				w.conversations.MessageHasBeenPosted(nil, w.rootPost(autoReplyUserID, "what is the weather"))
				return ""
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "loop-in from the mention reminder",
			start: func(t *testing.T, w *notificationWiring) string {
				reply := w.threadReply(autoReplyUserID, "what is the weather", true)
				require.NoError(t, w.conversations.HandleLoopInAgent(context.Background(), autoReplyUserID, policyBot(w.policyEnv), reply, w.channel))
				return ""
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "DM tool approval continuation ends with the final answer",
			start: func(t *testing.T, w *notificationWiring) string {
				botPost := w.pausedRun(t, w.dm, pendingTimeBlock(), nil)
				require.NoError(t, w.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, w.dm, []string{"tc-time"}, nil))
				return botPost.Id
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "ask-user answer continuation ends with the final answer",
			start: func(t *testing.T, w *notificationWiring) string {
				botPost := w.pausedRun(t, w.channel, pendingQuestionBlock(), nil)
				answers := map[string]mmtools.UserInteractionAnswer{"q-1": {Selected: []string{"NYC"}}}
				require.NoError(t, w.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, w.channel, []string{"q-1"}, answers))
				return botPost.Id
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "share decision continuation ends with the final answer",
			start: func(t *testing.T, w *notificationWiring) string {
				toolUse, toolResults := executedWeatherBlocks()
				botPost := w.pausedRun(t, w.channel, toolUse, toolResults)
				require.NoError(t, w.conversations.HandleToolResult(context.Background(), autoReplyUserID, botPost, w.channel, []string{policyWeatherCall.ID}))
				return botPost.Id
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDelivery: []fakeDelivery{release},
		},
		{
			name: "shared channel mention, which the server does not defer, pushes only to the requester",
			setup: func(w *notificationWiring) {
				w.channel.Shared = new(true)
			},
			start: func(_ *testing.T, w *notificationWiring) string {
				w.conversations.MessageHasBeenPosted(nil, w.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" weather?"))
				return ""
			},
			wantOutcome: notifications.OutcomeCompleted,
			wantPushes:  []string{autoReplyUserID},
		},
		{
			name: "release the server keeps failing notifies the requester",
			setup: func(w *notificationWiring) {
				w.mmClient.deliveryErr = func(d fakeDelivery) error {
					if d.userIDs == nil {
						return errUnavailable
					}
					return nil
				}
			},
			start: func(_ *testing.T, w *notificationWiring) string {
				w.conversations.MessageHasBeenPosted(nil, w.dmPost("what is the weather"))
				return ""
			},
			wantOutcome:  notifications.OutcomeCompleted,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{release, release, release, toRequester},
		},
		{
			name: "multiplayer channels unavailable reply notifies only the requester",
			setup: func(w *notificationWiring) {
				w.overrideLicense(nil)
			},
			start: func(_ *testing.T, w *notificationWiring) string {
				w.conversations.MessageHasBeenPosted(nil, w.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" weather?"))
				return ""
			},
			wantOutcome:  notifications.OutcomeErrored,
			wantDeferred: true,
			wantDelivery: []fakeDelivery{toRequester},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := setupEntryPointWiring(t, dmMakeTextStream("It's sunny"))
			if tc.setup != nil {
				tc.setup(w)
			}

			resumedPostID := tc.start(t, w)
			end := w.notifier.waitForRunEnd(t)
			w.notifier.requireNoMoreRunEnds(t)

			assert.Equal(t, autoReplyUserID, end.RequesterID)
			assert.Equal(t, tc.wantOutcome, end.Outcome)
			assert.False(t, end.PausedForRequester)
			if resumedPostID != "" {
				assert.Equal(t, resumedPostID, end.PostID)
				assert.True(t, end.PostDeferred, "the resumed post was still deferred")
			} else {
				created := w.createdPost(end.PostID)
				require.NotNil(t, created, "the run answers on a new response post")
				assert.Equal(t, tc.wantDeferred, created.HasDeferredNotification())
				assert.Equal(t, tc.wantDeferred, end.PostDeferred)
			}

			var want []fakeDelivery
			for _, d := range tc.wantDelivery {
				d.postID = end.PostID
				want = append(want, d)
			}
			assert.Equal(t, want, w.mmClient.deliveredNotifications())
			assert.Equal(t, tc.wantPushes, w.client.pushedTo())
		})
	}
}
