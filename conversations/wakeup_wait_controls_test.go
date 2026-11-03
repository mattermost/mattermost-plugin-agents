// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// waitPost returns a bot response post in rootID's thread, tagged with convID,
// as the webapp would target a wait control at.
func waitPost(id, convID, rootID string) *model.Post {
	post := &model.Post{Id: id, UserId: "bot-id", RootId: rootID}
	post.AddProp(streaming.ConversationIDProp, convID)
	return post
}

// seedMarker writes marker as the pending wake of post's thread.
func (e *wakeEnv) seedMarker(t *testing.T, post *model.Post, marker wakeMarker) {
	t.Helper()
	raw, err := json.Marshal(marker)
	require.NoError(t, err)
	e.kv.data[responsePostMarkerKey(post)] = raw
}

func TestScheduleWakeRecordsWaitMetadata(t *testing.T) {
	env := newWakeEnv(t, nil)
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	env.c.wakeScheduler = sched

	const rootID = "root-post-id"
	post := waitPost("bot-post-id", env.conv.ID, rootID)
	env.mmClient.On("GetPost", post.Id).Return(post, nil)

	before := time.Now()
	require.NoError(t, env.c.ScheduleWake(post.Id, wakeUserID, 10*time.Minute))

	info, err := env.c.ActiveWaitForConversation(env.conv.ID, rootID, "bot-id")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, post.Id, info.ResponsePostID)
	require.Equal(t, wakeUserID, info.RequesterUserID)
	require.Equal(t, 10, info.DurationMinutes)
	require.GreaterOrEqual(t, info.ScheduledAt, before.UnixMilli())
	require.LessOrEqual(t, info.ScheduledAt, time.Now().UnixMilli())
	require.GreaterOrEqual(t, info.ResumeAt, before.Add(10*time.Minute).UnixMilli())
}

func TestActiveWaitForConversation(t *testing.T) {
	const rootID = "root-post-id"
	marker := wakeMarker{
		ConversationID:  "conv-id",
		ScheduledAt:     wakeScheduledAt,
		ResponsePostID:  "bot-post-id",
		RequesterUserID: wakeUserID,
		ResumeAt:        wakeScheduledAt + (10 * time.Minute).Milliseconds(),
		DurationMinutes: 10,
	}

	tests := []struct {
		name     string
		seeded   bool
		marker   wakeMarker
		queryID  string
		noSched  bool
		wantInfo bool
	}{
		{name: "pending wait is reported to its own conversation", seeded: true, marker: marker, queryID: "conv-id", wantInfo: true},
		{name: "a wait another conversation scheduled is hidden", seeded: true, marker: marker, queryID: "other-conv"},
		{name: "no pending wait reports nothing", queryID: "conv-id"},
		{name: "scheduler not wired reports nothing", seeded: true, marker: marker, queryID: "conv-id", noSched: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			if !tt.noSched {
				env.c.wakeScheduler = &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			}
			post := waitPost("bot-post-id", "conv-id", rootID)
			if tt.seeded {
				env.seedMarker(t, post, tt.marker)
			}

			info, err := env.c.ActiveWaitForConversation(tt.queryID, rootID, "bot-id")
			require.NoError(t, err)
			if !tt.wantInfo {
				require.Nil(t, info)
				return
			}
			require.NotNil(t, info)
			require.Equal(t, "bot-post-id", info.ResponsePostID)
			require.Equal(t, wakeUserID, info.RequesterUserID)
			require.Equal(t, 10, info.DurationMinutes)
		})
	}
}

func fullWaitMarker(convID, postID, requester string) wakeMarker {
	return wakeMarker{
		ConversationID:  convID,
		ScheduledAt:     wakeScheduledAt,
		ResponsePostID:  postID,
		RequesterUserID: requester,
		ResumeAt:        wakeScheduledAt + (10 * time.Minute).Milliseconds(),
		DurationMinutes: 10,
	}
}

func TestHandleWaitCancel(t *testing.T) {
	const rootID = "root-post-id"
	otherUser := model.NewId()

	tests := []struct {
		name       string
		seeded     bool
		caller     string
		wantErr    error
		wantCancel bool
	}{
		{name: "requester cancels the pending wait", seeded: true, caller: wakeUserID, wantCancel: true},
		{name: "onlooker cannot cancel", seeded: true, caller: otherUser, wantErr: ErrNotWaitOwner},
		{name: "no pending wait is a conflict", caller: wakeUserID, wantErr: ErrNoPendingWait},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			env.c.wakeScheduler = sched
			post := waitPost("bot-post-id", env.conv.ID, rootID)
			if tt.seeded {
				env.seedMarker(t, post, fullWaitMarker(env.conv.ID, post.Id, wakeUserID))
				sched.scheduled[WakeJobKeyPrefix+env.conv.ID] = newWakeJob(post.Id, env.conv.ID, wakeUserID, wakeScheduledAt)
			}

			err := env.c.HandleWaitCancel(tt.caller, post)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if tt.seeded {
					require.NotEmpty(t, env.kv.data, "a rejected cancel must leave the wait pending")
					require.Empty(t, sched.canceled)
				}
				return
			}
			require.NoError(t, err)
			require.Empty(t, env.kv.data, "a canceled wait must consume its marker")
			require.Equal(t, []string{WakeJobKeyPrefix + env.conv.ID}, sched.canceled)
			require.Equal(t, []string{env.conv.ID}, env.convUpdates, "canceling must tell clients to refetch")
		})
	}
}

func TestHandleWaitCheckNowAuthorization(t *testing.T) {
	const rootID = "root-post-id"
	otherUser := model.NewId()

	tests := []struct {
		name    string
		seeded  bool
		caller  string
		wantErr error
	}{
		{name: "onlooker cannot check now", seeded: true, caller: otherUser, wantErr: ErrNotWaitOwner},
		{name: "no pending wait is a conflict", caller: wakeUserID, wantErr: ErrNoPendingWait},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			env.c.wakeScheduler = sched
			var resumed bool
			env.c.resumeWake = func(WakeJob) { resumed = true }
			post := waitPost("bot-post-id", env.conv.ID, rootID)
			if tt.seeded {
				env.seedMarker(t, post, fullWaitMarker(env.conv.ID, post.Id, wakeUserID))
				sched.scheduled[WakeJobKeyPrefix+env.conv.ID] = newWakeJob(post.Id, env.conv.ID, wakeUserID, wakeScheduledAt)
			}

			err := env.c.HandleWaitCheckNow(tt.caller, post)
			require.ErrorIs(t, err, tt.wantErr)
			require.False(t, resumed, "a rejected check-now must not resume")
			if tt.seeded {
				require.NotEmpty(t, env.kv.data, "a rejected check-now must leave the wait pending")
				require.Empty(t, sched.canceled)
			}
		})
	}
}

func TestHandleWaitCheckNowResumesForRequester(t *testing.T) {
	env := newWakeEnv(t, nil)
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	env.c.wakeScheduler = sched

	var captured WakeJob
	env.c.resumeWake = func(j WakeJob) { captured = j }

	const rootID = "root-post-id"
	post := waitPost("bot-post-id", env.conv.ID, rootID)
	marker := fullWaitMarker(env.conv.ID, post.Id, wakeUserID)
	env.seedMarker(t, post, marker)
	sched.scheduled[WakeJobKeyPrefix+env.conv.ID] = newWakeJob(post.Id, env.conv.ID, wakeUserID, wakeScheduledAt)

	require.NoError(t, env.c.HandleWaitCheckNow(wakeUserID, post))

	// The resume, not the request, consumes the marker and cancels the trigger.
	require.NotEmpty(t, env.kv.data)
	require.Empty(t, sched.canceled)

	// The resume runs as the requester on the wait's response post, carrying
	// the marker so its compare-and-delete consume matches.
	require.Equal(t, post.Id, captured.PostID)
	require.Equal(t, wakeUserID, captured.UserID)
	require.Equal(t, marker, captured.wakeMarker)
	require.True(t, captured.cancelOnResume, "the resume must cancel the scheduled trigger")
}

// replacementWait is a wait another requester scheduled for the same post after
// the original one resolved; it shares the original's marker key and job key.
func replacementWait(convID, postID string) wakeMarker {
	marker := fullWaitMarker(convID, postID, model.NewId())
	marker.ScheduledAt++
	return marker
}

func TestWaitControlsRejectOtherResponsePost(t *testing.T) {
	const rootID = "root-post-id"

	tests := []struct {
		name string
		call func(env *wakeEnv, post *model.Post) error
	}{
		{name: "check now", call: func(env *wakeEnv, post *model.Post) error {
			return env.c.HandleWaitCheckNow(wakeUserID, post)
		}},
		{name: "cancel", call: func(env *wakeEnv, post *model.Post) error {
			return env.c.HandleWaitCancel(wakeUserID, post)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			env.c.wakeScheduler = sched
			var resumed bool
			env.c.resumeWake = func(WakeJob) { resumed = true }

			olderPost := waitPost("older-bot-post-id", env.conv.ID, rootID)
			newerPost := waitPost("newer-bot-post-id", env.conv.ID, rootID)
			env.seedMarker(t, newerPost, fullWaitMarker(env.conv.ID, newerPost.Id, wakeUserID))
			pending := env.kv.data[responsePostMarkerKey(newerPost)]

			require.ErrorIs(t, tt.call(env, olderPost), ErrNoPendingWait)
			require.False(t, resumed)
			require.Empty(t, sched.canceled)
			require.Equal(t, pending, env.kv.data[responsePostMarkerKey(newerPost)])
		})
	}
}

func TestStaleWaitCancelLeavesReplacementWaitPending(t *testing.T) {
	env := newWakeEnv(t, nil)
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	env.c.wakeScheduler = sched

	post := waitPost("bot-post-id", env.conv.ID, "root-post-id")
	env.seedMarker(t, post, fullWaitMarker(env.conv.ID, post.Id, wakeUserID))

	replacement := replacementWait(env.conv.ID, post.Id)
	replacementRaw, err := json.Marshal(replacement)
	require.NoError(t, err)
	jobKey := WakeJobKeyPrefix + env.conv.ID
	env.kv.afterGet = func() {
		env.kv.data[responsePostMarkerKey(post)] = replacementRaw
		sched.scheduled[jobKey] = WakeJob{PostID: post.Id, UserID: replacement.RequesterUserID, wakeMarker: replacement}
	}

	require.ErrorIs(t, env.c.HandleWaitCancel(wakeUserID, post), ErrNoPendingWait)
	require.Empty(t, sched.canceled, "a stale cancel must not cancel the replacement wake")
	require.Contains(t, sched.scheduled, jobKey)
	require.Equal(t, replacementRaw, env.kv.data[responsePostMarkerKey(post)])
}

func TestStaleCheckNowResumeLeavesReplacementWaitPending(t *testing.T) {
	tests := []struct {
		name string
		// after changes the stored wait between the click and the queued resume.
		after func(env *wakeEnv, post *model.Post)
	}{
		{name: "replaced by a newer wait", after: func(env *wakeEnv, post *model.Post) {
			env.seedMarker(t, post, replacementWait(env.conv.ID, post.Id))
		}},
		{name: "steered by a newer message", after: func(env *wakeEnv, post *model.Post) {
			delete(env.kv.data, responsePostMarkerKey(post))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			env.c.wakeScheduler = sched
			var queued WakeJob
			env.c.resumeWake = func(j WakeJob) { queued = j }

			post := waitPost("bot-post-id", env.conv.ID, "root-post-id")
			post.ChannelId = "dm-channel"
			env.seedMarker(t, post, fullWaitMarker(env.conv.ID, post.Id, wakeUserID))
			jobKey := WakeJobKeyPrefix + env.conv.ID
			require.NoError(t, env.c.HandleWaitCheckNow(wakeUserID, post))

			tt.after(env, post)
			sched.scheduled[jobKey] = WakeJob{PostID: post.Id, UserID: wakeUserID, wakeMarker: replacementWait(env.conv.ID, post.Id)}
			remaining := env.kv.data[responsePostMarkerKey(post)]
			env.mmClient.On("GetPost", post.Id).Return(post, nil).Once()

			require.NoError(t, env.c.ResumeConversation(context.Background(), queued))
			require.Empty(t, sched.canceled, "a dropped early resume must not cancel a newer wake")
			require.Contains(t, sched.scheduled, jobKey)
			require.Equal(t, remaining, env.kv.data[responsePostMarkerKey(post)])
			turns, err := env.convStore.GetTurnsForConversation(env.conv.ID)
			require.NoError(t, err)
			require.Empty(t, turns)
		})
	}
}

func TestEarlyResumeCancelsScheduledTrigger(t *testing.T) {
	env := newWakeEnv(t, nil)
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	env.c.wakeScheduler = sched
	env.seedTurn(t, "user-1", "user", "please watch the job", nil, 1, 0)

	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: "root-post-id"}
	post.AddProp(streaming.ConversationIDProp, env.conv.ID)
	env.seedMarker(t, post, fullWaitMarker(env.conv.ID, post.Id, wakeUserID))
	sched.scheduled[WakeJobKeyPrefix+env.conv.ID] = newWakeJob(post.Id, env.conv.ID, wakeUserID, wakeScheduledAt)
	env.expectLiveWake(post, channel, wakeUserID)

	var queued WakeJob
	env.c.resumeWake = func(j WakeJob) { queued = j }
	require.NoError(t, env.c.HandleWaitCheckNow(wakeUserID, post))
	require.NoError(t, env.c.ResumeConversation(context.Background(), queued))
	env.streaming.waitForStreaming()

	require.Empty(t, env.kv.data, "the early resume must consume the marker")
	require.Equal(t, []string{WakeJobKeyPrefix + env.conv.ID}, sched.canceled)
	require.NotNil(t, env.wakePost, "the early resume must reach the LLM")
}
