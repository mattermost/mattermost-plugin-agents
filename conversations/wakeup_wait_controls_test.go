// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
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

	// The scheduled trigger is canceled so it cannot also fire.
	require.Equal(t, []string{WakeJobKeyPrefix + env.conv.ID}, sched.canceled)

	// The resume runs as the requester on the wait's response post, carrying
	// the marker so its compare-and-delete consume matches.
	require.Equal(t, post.Id, captured.PostID)
	require.Equal(t, wakeUserID, captured.UserID)
	require.Equal(t, marker, captured.wakeMarker)
}
