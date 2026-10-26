// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/notifications"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notifyCall struct {
	end notifications.StreamEnd
	// updatesBefore and lastControl capture the client state when Notify
	// ran, to check it follows the final post update and the end event.
	updatesBefore int
	lastControl   any
}

// recordingNotifier marks posts deferred like a supporting server would and
// records every Notify call.
type recordingNotifier struct {
	mu        sync.Mutex
	supported bool
	client    *fakeStreamingClient
	calls     []notifyCall
	notified  chan struct{}
}

func newRecordingNotifier(client *fakeStreamingClient, supported bool) *recordingNotifier {
	return &recordingNotifier{supported: supported, client: client, notified: make(chan struct{}, 8)}
}

func (r *recordingNotifier) MarkDeferred(post *model.Post) {
	if r.supported {
		post.AddProp(model.PostPropsDeferredNotification, true)
	}
}

func (r *recordingNotifier) Notify(_ context.Context, end notifications.StreamEnd) {
	r.mu.Lock()
	call := notifyCall{end: end, updatesBefore: len(r.client.updatedPosts)}
	if n := len(r.client.events); n > 0 {
		call.lastControl = r.client.events[n-1].payload["control"]
	}
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	r.notified <- struct{}{}
}

func (r *recordingNotifier) snapshot() []notifyCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notifyCall(nil), r.calls...)
}

func TestStreamToPostNotifiesOnceAtRunEnd(t *testing.T) {
	const channelID = "channel-id"
	const postID = "post-id"
	const requesterID = "requester-id"

	pendingApproval := []llm.ToolCall{{ID: "call-1", Name: "create_post", Status: llm.ToolCallStatusPending}}
	pendingMixed := []llm.ToolCall{
		{ID: "call-1", Name: "search", Status: llm.ToolCallStatusPending, WouldAutoExecute: true},
		{ID: "call-2", Name: "create_post", Status: llm.ToolCallStatusPending},
	}
	pendingAuto := []llm.ToolCall{{ID: "call-1", Name: "search", Status: llm.ToolCallStatusPending, WouldAutoExecute: true}}
	resolved := []llm.ToolCall{{ID: "call-1", Name: "search", Status: llm.ToolCallStatusSuccess, Result: "ok"}}
	pendingInteraction := []llm.ToolCall{{ID: "call-1", Name: "ask_user", Status: llm.ToolCallStatusPending, UserInteraction: "select"}}
	pendingConnect := []llm.ToolCall{{ID: "call-1", Name: "connect", Status: llm.ToolCallStatusPending, UserInteraction: "connect"}}
	text := func(s string) llm.TextStreamEvent { return llm.TextStreamEvent{Type: llm.EventTypeText, Value: s} }
	tools := func(calls []llm.ToolCall) llm.TextStreamEvent {
		return llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: append([]llm.ToolCall(nil), calls...)}
	}
	end := llm.TextStreamEvent{Type: llm.EventTypeEnd}

	tests := []struct {
		name         string
		events       []llm.TextStreamEvent
		keepOpen     bool
		notDeferred  bool
		updateErr    error
		stop         bool
		cancelParent bool
		wantEnd      notifications.StreamEnd
		wantDelivery notifications.Delivery
	}{
		{
			name:         "final text",
			events:       []llm.TextStreamEvent{text("answer"), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PostDeferred: true},
			wantDelivery: notifications.DeliveryEveryone,
		},
		{
			name:         "stream closes without an end event",
			events:       []llm.TextStreamEvent{text("answer")},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PostDeferred: true},
			wantDelivery: notifications.DeliveryEveryone,
		},
		{
			name:         "auto-executed batch resolves then text",
			events:       []llm.TextStreamEvent{text("checking"), tools(pendingAuto), tools(resolved), text("answer"), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PostDeferred: true},
			wantDelivery: notifications.DeliveryEveryone,
		},
		{
			name:         "pending approval then end",
			events:       []llm.TextStreamEvent{text("I need approval"), tools(pendingApproval), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PausedForRequester: true, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "partly auto-approved batch still waits for the requester",
			events:       []llm.TextStreamEvent{text("I need approval"), tools(pendingMixed), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PausedForRequester: true, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "ask-user interaction tool pending",
			events:       []llm.TextStreamEvent{text("Which one?"), tools(pendingInteraction), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PausedForRequester: true, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "connect interaction tool pending",
			events:       []llm.TextStreamEvent{text("Connect first"), tools(pendingConnect), end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, PausedForRequester: true, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "final answer on a post that was already delivered",
			events:       []llm.TextStreamEvent{text("answer"), end},
			notDeferred:  true,
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "error",
			events:       []llm.TextStreamEvent{text("partial"), {Type: llm.EventTypeError, Value: errors.New("provider down")}},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeErrored, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "empty",
			events:       []llm.TextStreamEvent{end},
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeEmpty, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "stopped by the requester",
			keepOpen:     true,
			stop:         true,
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeStoppedByUser, PostDeferred: true},
			wantDelivery: notifications.DeliveryNone,
		},
		{
			name:         "canceled without a user stop",
			keepOpen:     true,
			cancelParent: true,
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCancelled, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "final update fails",
			events:       []llm.TextStreamEvent{text("answer"), end},
			updateErr:    errors.New("db down"),
			wantEnd:      notifications.StreamEnd{Outcome: notifications.OutcomeCompleted, UpdateFailed: true, PostDeferred: true},
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStreamingClient{
				channels:  map[string]*model.Channel{channelID: {Id: channelID, Type: model.ChannelTypeOpen}},
				updateErr: tc.updateErr,
			}
			notifier := newRecordingNotifier(client, true)
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetNotifier(notifier)

			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			ctx, err := service.GetStreamingContext(parent, postID)
			require.NoError(t, err)
			defer service.FinishStreaming(postID)

			stream := make(chan llm.TextStreamEvent, len(tc.events))
			for _, e := range tc.events {
				stream <- e
			}
			if !tc.keepOpen {
				close(stream)
			}
			if tc.stop {
				service.StopStreaming(postID)
			}
			if tc.cancelParent {
				cancelParent()
			}

			post := &model.Post{Id: postID, ChannelId: channelID}
			if !tc.notDeferred {
				post.AddProp(model.PostPropsDeferredNotification, true)
			}
			service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", requesterID)

			calls := notifier.snapshot()
			require.Len(t, calls, 1, "exactly one notification decision per run")
			call := calls[0]

			want := tc.wantEnd
			want.PostID = postID
			want.ChannelID = channelID
			want.RequesterID = requesterID
			assert.Equal(t, want, call.end)
			assert.Equal(t, tc.wantDelivery, notifications.Decide(call.end))

			assert.Equal(t, len(client.updatedPosts), call.updatesBefore, "notify follows the final post update")
			assert.Equal(t, PostStreamingControlEnd, call.lastControl, "notify follows the end event")
		})
	}
}

func TestShutdownCancelsStreamsAndNotifiesRequesters(t *testing.T) {
	const channelID = "channel-id"
	tests := []struct {
		name         string
		stopFirst    bool
		wantOutcome  notifications.Outcome
		wantDelivery notifications.Delivery
	}{
		{
			name:         "in-flight stream ends as a cancel that notifies the requester",
			wantOutcome:  notifications.OutcomeCancelled,
			wantDelivery: notifications.DeliveryRequesterOnly,
		},
		{
			name:         "stream the requester stopped stays stopped by the user",
			stopFirst:    true,
			wantOutcome:  notifications.OutcomeStoppedByUser,
			wantDelivery: notifications.DeliveryNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStreamingClient{channels: map[string]*model.Channel{channelID: {Id: channelID, Type: model.ChannelTypeOpen}}}
			notifier := newRecordingNotifier(client, true)
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetNotifier(notifier)

			post := &model.Post{Id: model.NewId(), ChannelId: channelID}
			stream := make(chan llm.TextStreamEvent)
			require.NoError(t, service.StreamToNewPost(context.Background(), "bot-id", "user-id", &llm.TextStreamResult{Stream: stream}, post, ""))
			if tc.stopFirst {
				service.StopStreaming(post.Id)
			}

			require.True(t, service.Shutdown(5*time.Second), "the run finished before the timeout")

			calls := notifier.snapshot()
			require.Len(t, calls, 1, "the run notified once before shutdown returned")
			assert.Equal(t, tc.wantOutcome, calls[0].end.Outcome)
			assert.Equal(t, tc.wantDelivery, notifications.Decide(calls[0].end))

			_, err := service.GetStreamingContext(context.Background(), model.NewId())
			assert.ErrorIs(t, err, ErrShuttingDown, "no new streams start during shutdown")
		})
	}
}

func TestShutdownTimesOut(t *testing.T) {
	tests := []struct {
		name     string
		inFlight bool
		want     bool
	}{
		{name: "nothing in flight", want: true},
		{name: "a run that never finishes", inFlight: true, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewMMPostStreamService(&fakeStreamingClient{}, i18n.Init())
			var ctx context.Context
			if tc.inFlight {
				var err error
				ctx, err = service.GetStreamingContext(context.Background(), "post-id")
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, service.Shutdown(50*time.Millisecond))
			if ctx != nil {
				assert.ErrorIs(t, context.Cause(ctx), errPluginShutdown)
			}
		})
	}
}

func TestStreamToNewPostMarksDeferred(t *testing.T) {
	const channelID = "channel-id"
	const botID = "bot-id"
	const userID = "user-id"

	tests := []struct {
		name         string
		notifier     bool
		supported    bool
		newDM        bool
		wantDeferred bool
	}{
		{name: "new post on a supporting server", notifier: true, supported: true, wantDeferred: true},
		{name: "new DM on a supporting server", notifier: true, supported: true, newDM: true, wantDeferred: true},
		{name: "new post on a server without support", notifier: true},
		{name: "new post without a notifier"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeStreamingClient{channels: map[string]*model.Channel{channelID: {Id: channelID, Type: model.ChannelTypeOpen}}}
			service := NewMMPostStreamService(client, i18n.Init())
			var notifier *recordingNotifier
			if tc.notifier {
				notifier = newRecordingNotifier(client, tc.supported)
				service.SetNotifier(notifier)
			}

			stream := make(chan llm.TextStreamEvent, 2)
			stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "answer"}
			stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			close(stream)

			post := &model.Post{Id: model.NewId(), ChannelId: channelID}
			if tc.newDM {
				require.NoError(t, service.StreamToNewDM(context.Background(), botID, &llm.TextStreamResult{Stream: stream}, userID, post, ""))
			} else {
				require.NoError(t, service.StreamToNewPost(context.Background(), botID, userID, &llm.TextStreamResult{Stream: stream}, post, ""))
			}

			require.Len(t, client.createdPosts, 1)
			assert.Equal(t, tc.wantDeferred, client.createdPosts[0].HasDeferredNotification())

			if notifier != nil {
				select {
				case <-notifier.notified:
				case <-time.After(5 * time.Second):
					require.FailNow(t, "stream did not end")
				}
				calls := notifier.snapshot()
				require.Len(t, calls, 1)
				assert.Equal(t, tc.wantDeferred, calls[0].end.PostDeferred)
				assert.Equal(t, userID, calls[0].end.RequesterID)
			}
		})
	}
}
