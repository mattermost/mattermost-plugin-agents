// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/lifecycle"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedStreamingClient wraps fakeStreamingClient for use while a stream runs
// in another goroutine.
type lockedStreamingClient struct {
	fakeStreamingClient
	mu sync.Mutex
}

func (c *lockedStreamingClient) UpdatePost(post *model.Post) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fakeStreamingClient.UpdatePost(post)
}

func (c *lockedStreamingClient) PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fakeStreamingClient.PublishWebSocketEvent(event, payload, broadcast)
}

func (c *lockedStreamingClient) LogWarn(msg string, kv ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fakeStreamingClient.LogWarn(msg, kv...)
}

func (c *lockedStreamingClient) lastUpdate() *model.Post {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.updatedPosts) == 0 {
		return nil
	}
	return c.updatedPosts[len(c.updatedPosts)-1]
}

func (c *lockedStreamingClient) updateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.updatedPosts)
}

func (c *lockedStreamingClient) controlCount(control string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.payload["control"] == control {
			n++
		}
	}
	return n
}

func newRecoveryTestService(t *testing.T) (*MMPostStreamService, *lockedStreamingClient, *fakeTurnStore) {
	t.Helper()
	client := &lockedStreamingClient{}
	client.channels = map[string]*model.Channel{"channel-id": {Id: "channel-id", Type: model.ChannelTypeOpen}}
	ts := &fakeTurnStore{}
	service := NewMMPostStreamService(client, i18n.Init())
	service.SetTurnStore(ts)
	return service, client, ts
}

func newStreamPost() *model.Post {
	post := &model.Post{Id: "post-id", ChannelId: "channel-id", UserId: "bot-id"}
	post.AddProp(ConversationIDProp, "conv-id")
	return post
}

func TestStreamCancellationStatus(t *testing.T) {
	tests := []struct {
		name            string
		shutdown        bool
		wantStatus      string
		wantInterrupted any
	}{
		{name: "user stop finalizes as complete without marker", shutdown: false, wantStatus: store.TurnStatusComplete, wantInterrupted: nil},
		{name: "plugin shutdown finalizes as interrupted with shutdown marker", shutdown: true, wantStatus: store.TurnStatusInterrupted, wantInterrupted: InterruptedReasonShutdown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, client, ts := newRecoveryTestService(t)
			root, cancelRoot := context.WithCancelCause(context.Background())
			defer cancelRoot(nil)
			service.SetRootContext(root)

			post := newStreamPost()
			ctx, err := service.GetStreamingContext(context.Background(), post.Id)
			require.NoError(t, err)

			stream := make(chan llm.TextStreamEvent, 1)
			stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "partial answer"}

			done := make(chan struct{})
			go func() {
				defer close(done)
				defer service.FinishStreaming(post.Id)
				service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
			}()

			require.Eventually(t, func() bool { return service.ActiveStreams() == 1 && len(stream) == 0 }, time.Second, 5*time.Millisecond)
			if tc.shutdown {
				cancelRoot(lifecycle.ErrShutdown)
				assert.True(t, service.WaitForActiveStreams(2*time.Second))
			} else {
				service.StopStreaming(post.Id)
			}
			<-done

			ts.mu.Lock()
			turn := findStreamTurn(ts.turns, post.Id)
			ts.mu.Unlock()
			require.NotNil(t, turn)
			assert.Equal(t, tc.wantStatus, turn.Status)
			assert.Contains(t, string(turn.Content), "partial answer")

			last := client.lastUpdate()
			require.NotNil(t, last)
			assert.Equal(t, "partial answer", last.Message)
			assert.Equal(t, tc.wantInterrupted, last.GetProp(InterruptedProp))
			if tc.shutdown {
				assert.NotNil(t, last.GetProp(InterruptedAtProp))
			}
		})
	}
}

// Shutdown also cancels the LLM request, so the stream can end (error or
// close) in the same instant ctx.Done fires. Whichever select case wins, the
// reply must be marked interrupted. Repeat to cover both select orders.
func TestStreamShutdownWhileLLMStreamEnds(t *testing.T) {
	endings := []struct {
		name string
		end  func(chan llm.TextStreamEvent)
	}{
		{name: "error event", end: func(c chan llm.TextStreamEvent) {
			c <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: context.Canceled}
		}},
		{name: "channel closed", end: func(c chan llm.TextStreamEvent) { close(c) }},
	}
	for _, ending := range endings {
		t.Run(ending.name, func(t *testing.T) {
			for range 20 {
				service, client, ts := newRecoveryTestService(t)
				root, cancelRoot := context.WithCancelCause(context.Background())
				service.SetRootContext(root)

				post := newStreamPost()
				ctx, err := service.GetStreamingContext(context.Background(), post.Id)
				require.NoError(t, err)

				stream := make(chan llm.TextStreamEvent, 2)
				stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "partial answer"}

				done := make(chan struct{})
				go func() {
					defer close(done)
					defer service.FinishStreaming(post.Id)
					service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
				}()
				require.Eventually(t, func() bool { return service.ActiveStreams() == 1 && len(stream) == 0 }, time.Second, time.Millisecond)

				cancelRoot(lifecycle.ErrShutdown)
				ending.end(stream)
				<-done

				ts.mu.Lock()
				turn := findStreamTurn(ts.turns, post.Id)
				ts.mu.Unlock()
				require.NotNil(t, turn)
				require.Equal(t, store.TurnStatusInterrupted, turn.Status)
				last := client.lastUpdate()
				require.NotNil(t, last)
				require.Equal(t, InterruptedReasonShutdown, last.GetProp(InterruptedProp))
				require.Equal(t, "partial answer", last.Message)
			}
		})
	}
}

func TestStreamHeartbeat(t *testing.T) {
	service, client, ts := newRecoveryTestService(t)
	service.SetHeartbeatInterval(10 * time.Millisecond)

	post := newStreamPost()
	stream := make(chan llm.TextStreamEvent, 1)
	stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "so far"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
	}()

	// While streaming (no further tokens), the turn exists in the streaming
	// state with partial content and heartbeat events keep reaching clients.
	// The post itself is not rewritten (each update adds edit history).
	require.Eventually(t, func() bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		turn := findStreamTurn(ts.turns, post.Id)
		return turn != nil && ts.heartbeats >= 2 && turn.Status == store.TurnStatusStreaming &&
			string(turn.Content) != "[]"
	}, 2*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		return client.controlCount(PostStreamingControlHeartbeat) >= 2
	}, 2*time.Second, 5*time.Millisecond)
	if last := client.lastUpdate(); last != nil {
		assert.NotEqual(t, "so far", last.Message, "heartbeats must not update the post")
	}

	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	<-done

	ts.mu.Lock()
	defer ts.mu.Unlock()
	turn := findStreamTurn(ts.turns, post.Id)
	require.NotNil(t, turn)
	assert.Equal(t, store.TurnStatusComplete, turn.Status)
}

// A stream whose turn can't be kept fresh would soon look abandoned to other
// nodes, which may recover or regenerate it; it must stop instead of running on.
func TestStreamStopsWhenItsTurnCannotBePersisted(t *testing.T) {
	tests := []struct {
		name         string
		createErr    error
		heartbeatErr error
	}{
		{name: "streaming turn insert fails", createErr: errors.New("db down")},
		{name: "heartbeats keep failing", heartbeatErr: errors.New("db down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, client, ts := newRecoveryTestService(t)
			service.SetHeartbeatInterval(5 * time.Millisecond)
			turnLeaseMaxAge = 20 * time.Millisecond
			t.Cleanup(func() { turnLeaseMaxAge = StaleStreamThreshold / 2 })
			ts.createErr = tt.createErr
			ts.heartbeatErr = tt.heartbeatErr

			post := newStreamPost()
			ctx, err := service.GetStreamingContext(context.Background(), post.Id)
			require.NoError(t, err)
			stream := make(chan llm.TextStreamEvent)

			done := make(chan struct{})
			go func() {
				defer close(done)
				defer service.FinishStreaming(post.Id)
				service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				require.Fail(t, "the stream must stop on its own")
			}
			require.ErrorIs(t, context.Cause(ctx), errTurnLeaseLost, "the run's context is canceled so its tools stop")
			last := client.lastUpdate()
			require.NotNil(t, last)
			assert.Equal(t, InterruptedReasonLost, last.GetProp(InterruptedProp))
		})
	}
}

// Once another node claims the turn as abandoned it owns the post (it may be
// regenerating it), so this run stops calling tools and leaves the post alone.
func TestStreamStopsWhenItsTurnIsClaimed(t *testing.T) {
	service, client, ts := newRecoveryTestService(t)
	service.SetHeartbeatInterval(5 * time.Millisecond)

	post := newStreamPost()
	ctx, err := service.GetStreamingContext(context.Background(), post.Id)
	require.NoError(t, err)
	stream := make(chan llm.TextStreamEvent)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer service.FinishStreaming(post.Id)
		service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
	}()

	require.Eventually(t, func() bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		turn := findStreamTurn(ts.turns, post.Id)
		if turn == nil {
			return false
		}
		turn.Status = store.TurnStatusInterrupted
		return true
	}, 2*time.Second, time.Millisecond)
	updatesAtClaim := client.updateCount()
	heartbeatsAtClaim := client.controlCount(PostStreamingControlHeartbeat)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "the stream must stop once its turn is claimed")
	}
	require.ErrorIs(t, context.Cause(ctx), errTurnClaimed)
	assert.Equal(t, updatesAtClaim, client.updateCount(), "the claiming node owns the post")
	assert.Zero(t, client.controlCount(PostStreamingControlEnd), "no end event may cut off the claiming node's stream")
	assert.LessOrEqual(t, client.controlCount(PostStreamingControlHeartbeat), heartbeatsAtClaim+1, "no heartbeats after the claim is seen")
	ts.mu.Lock()
	defer ts.mu.Unlock()
	assert.Zero(t, ts.finalizes, "a claimed turn must not be finalized by the old run")
}

// A claim can land between heartbeats; the run learns of it only when it
// finalizes at the end, and must then leave the claiming node's post alone.
func TestStreamClaimedBeforeItEndsLeavesThePostAlone(t *testing.T) {
	tests := []struct {
		name     string
		endEvent bool
	}{
		{name: "stream ends with an end event", endEvent: true},
		{name: "stream channel just closes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, client, ts := newRecoveryTestService(t)
			service.SetHeartbeatInterval(time.Hour)

			post := newStreamPost()
			ctx, err := service.GetStreamingContext(context.Background(), post.Id)
			require.NoError(t, err)
			stream := make(chan llm.TextStreamEvent)

			done := make(chan struct{})
			go func() {
				defer close(done)
				defer service.FinishStreaming(post.Id)
				service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
			}()

			stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "old run"}
			ts.mu.Lock()
			turn := findStreamTurn(ts.turns, post.Id)
			require.NotNil(t, turn)
			turn.Status = store.TurnStatusInterrupted
			ts.mu.Unlock()
			updatesAtClaim := client.updateCount()

			if tt.endEvent {
				stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			}
			close(stream)
			<-done

			assert.Equal(t, updatesAtClaim, client.updateCount(), "the claiming node owns the post")
			assert.Zero(t, client.controlCount(PostStreamingControlEnd))
			ts.mu.Lock()
			defer ts.mu.Unlock()
			assert.Equal(t, store.TurnStatusInterrupted, findStreamTurn(ts.turns, post.Id).Status)
		})
	}
}

// Stop during an auto-run batch must not leave its calls pending: they may
// already have taken effect, and a pending call would be offered to run again.
func TestStopDuringAutoRunBatchRecordsCallsAsErrored(t *testing.T) {
	service, _, ts := newRecoveryTestService(t)
	post := newStreamPost()
	ctx, err := service.GetStreamingContext(context.Background(), post.Id)
	require.NoError(t, err)
	stream := make(chan llm.TextStreamEvent)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer service.FinishStreaming(post.Id)
		service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
	}()

	stream <- llm.TextStreamEvent{Type: llm.EventTypeToolCallBatch, Value: llm.ToolCallBatch{
		Phase: llm.ToolCallPhaseExecuting,
		Calls: []llm.ToolCall{{ID: "call-1", Name: "create_post", Status: llm.ToolCallStatusPending, WouldAutoExecute: true}},
	}}
	service.StopStreaming(post.Id)
	<-done

	ts.mu.Lock()
	defer ts.mu.Unlock()
	turn := findStreamTurn(ts.turns, post.Id)
	require.NotNil(t, turn)
	blocks, err := conversation.UnmarshalBlocks(turn.Content)
	require.NoError(t, err)
	var statuses []string
	for _, b := range blocks {
		if b.Type == conversation.BlockTypeToolUse {
			statuses = append(statuses, b.Status)
		}
	}
	assert.Equal(t, []string{conversation.StatusError}, statuses)
}

// A finalize that fails leaves the turn's ownership unknown: a transient
// failure is retried, and one that keeps failing must not write the post.
func TestStreamEndWhenFinalizeFails(t *testing.T) {
	tests := []struct {
		name         string
		failures     int
		wantPostEdit bool
	}{
		{name: "transient failure is retried", failures: 1, wantPostEdit: true},
		{name: "persistent failure leaves the post alone", failures: finalizeAttempts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalizeRetryDelay = time.Millisecond
			t.Cleanup(func() { finalizeRetryDelay = 200 * time.Millisecond })
			service, client, ts := newRecoveryTestService(t)
			ts.finalizeFailures = tt.failures

			post := newStreamPost()
			stream := make(chan llm.TextStreamEvent, 2)
			stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "final answer"}
			stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
			close(stream)
			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")

			last := client.lastUpdate()
			if tt.wantPostEdit {
				require.NotNil(t, last)
				assert.Equal(t, "final answer", last.Message)
				assert.Equal(t, 1, client.controlCount(PostStreamingControlEnd))
				return
			}
			assert.Nil(t, last, "a run that can't confirm it owns the post must not write it")
			assert.Zero(t, client.controlCount(PostStreamingControlEnd))
		})
	}
}

// A run whose post reservation is lost may no longer own the post: its stream
// stops without finalizing the turn or writing the post.
func TestStreamThatLosesItsRunReservationLeavesThePostAlone(t *testing.T) {
	service, client, ts := newRecoveryTestService(t)
	post := newStreamPost()
	runCtx, cancelRun := context.WithCancelCause(context.Background())
	ctx, err := service.GetStreamingContext(runCtx, post.Id)
	require.NoError(t, err)
	stream := make(chan llm.TextStreamEvent)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer service.FinishStreaming(post.Id)
		service.StreamToPost(ctx, &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")
	}()
	stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "old run"}
	updatesBefore := client.updateCount()

	cancelRun(ErrRunOwnershipLost)
	<-done

	assert.Equal(t, updatesBefore, client.updateCount(), "another run may own the post")
	assert.Zero(t, client.controlCount(PostStreamingControlEnd))
	ts.mu.Lock()
	defer ts.mu.Unlock()
	assert.Zero(t, ts.finalizes, "the turn is left for whoever owns the post now")
}

func TestNewStreamClearsInterruptedMarker(t *testing.T) {
	service, client, _ := newRecoveryTestService(t)
	post := newStreamPost()
	MarkInterrupted(post, InterruptedReasonLost, 123)

	stream := make(chan llm.TextStreamEvent, 2)
	stream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "again"}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: stream}, post, "en", "requester-id")

	last := client.lastUpdate()
	require.NotNil(t, last)
	assert.Nil(t, last.GetProp(InterruptedProp))
	assert.Nil(t, last.GetProp(InterruptedAtProp))
}

// fakeRecoveryStore simulates the claim semantics of the real store.
type fakeRecoveryStore struct {
	stale   []store.Turn
	claimed map[string]bool
	listErr error
}

func (f *fakeRecoveryStore) GetStaleStreamingTurns(_ int64, limit uint64) ([]store.Turn, error) {
	var out []store.Turn
	for _, turn := range f.stale {
		if !f.claimed[turn.ID] && uint64(len(out)) < limit {
			out = append(out, turn)
		}
	}
	return out, f.listErr
}

func (f *fakeRecoveryStore) ReleaseStreamingTurnClaim(id string) error {
	f.claimed[id] = false
	return nil
}

func (f *fakeRecoveryStore) ClaimStaleStreamingTurn(id string, _ int64) (bool, error) {
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

type fakeRecoveryClient struct {
	posts     map[string]*model.Post
	updated   []*model.Post
	updateErr error
}

func (c *fakeRecoveryClient) GetPost(id string) (*model.Post, error) {
	p, ok := c.posts[id]
	if !ok {
		return nil, ErrPostNotFound
	}
	return p.Clone(), nil
}

func (c *fakeRecoveryClient) UpdatePost(post *model.Post) error {
	if c.updateErr != nil {
		return c.updateErr
	}
	c.updated = append(c.updated, post.Clone())
	c.posts[post.Id] = post.Clone()
	return nil
}

func (c *fakeRecoveryClient) LogWarn(string, ...any) {}
func (c *fakeRecoveryClient) LogInfo(string, ...any) {}

func TestRecoverAbandonedStreams(t *testing.T) {
	strp := func(s string) *string { return &s }
	textContent := func(s string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`[{"type":"text","text":%q}]`, s))
	}

	tests := []struct {
		name          string
		postMessage   string
		turnContent   json.RawMessage
		alreadyClaim  bool
		wantRecovered int
		wantMessage   string
	}{
		{name: "longer turn content replaces flushed message", postMessage: "Hel", turnContent: textContent("Hello world"), wantRecovered: 1, wantMessage: "Hello world"},
		{name: "longer post message is kept", postMessage: "Hello world, more", turnContent: textContent("Hello"), wantRecovered: 1, wantMessage: "Hello world, more"},
		{name: "turn claimed by another node is skipped", postMessage: "Hel", turnContent: textContent("Hello"), alreadyClaim: true, wantRecovered: 0, wantMessage: "Hel"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rs := &fakeRecoveryStore{
				stale:   []store.Turn{{ID: "turn-1", PostID: strp("post-1"), Role: "assistant", Status: store.TurnStatusStreaming, Content: tc.turnContent}},
				claimed: map[string]bool{"turn-1": tc.alreadyClaim},
			}
			client := &fakeRecoveryClient{posts: map[string]*model.Post{"post-1": {Id: "post-1", ChannelId: "ch", Message: tc.postMessage}}}

			recovered, err := RecoverAbandonedStreams(context.Background(), rs, client, 10)
			require.NoError(t, err)
			require.Len(t, recovered, tc.wantRecovered)

			post := client.posts["post-1"]
			assert.Equal(t, tc.wantMessage, post.Message)
			if tc.wantRecovered == 1 {
				assert.Equal(t, InterruptedReasonLost, post.GetProp(InterruptedProp))
				assert.NotNil(t, post.GetProp(InterruptedAtProp))
				assert.Equal(t, RecoveredStream{PostID: "post-1", ChannelID: "ch", TurnID: "turn-1"}, recovered[0])
			} else {
				assert.Nil(t, post.GetProp(InterruptedProp))
			}
		})
	}

	t.Run("failed post update releases the claim for a later retry", func(t *testing.T) {
		rs := &fakeRecoveryStore{
			stale:   []store.Turn{{ID: "turn-1", PostID: strp("post-1"), Role: "assistant", Content: textContent("Hello")}},
			claimed: map[string]bool{},
		}
		client := &fakeRecoveryClient{posts: map[string]*model.Post{"post-1": {Id: "post-1"}}, updateErr: errors.New("api down")}

		recovered, err := RecoverAbandonedStreams(context.Background(), rs, client, 10)
		require.NoError(t, err)
		require.Empty(t, recovered)
		require.False(t, rs.claimed["turn-1"], "the turn must stay retryable")
	})

	t.Run("drains more stale turns than one batch", func(t *testing.T) {
		rs := &fakeRecoveryStore{claimed: map[string]bool{}}
		client := &fakeRecoveryClient{posts: map[string]*model.Post{}}
		for i := range 5 {
			id := fmt.Sprintf("post-%d", i)
			rs.stale = append(rs.stale, store.Turn{ID: fmt.Sprintf("turn-%d", i), PostID: strp(id), Role: "assistant", Content: textContent("x")})
			client.posts[id] = &model.Post{Id: id}
		}

		recovered, err := RecoverAbandonedStreams(context.Background(), rs, client, 2)
		require.NoError(t, err)
		require.Len(t, recovered, 5)
	})

	t.Run("turns whose posts were deleted leave the backlog without blocking newer turns", func(t *testing.T) {
		rs := &fakeRecoveryStore{claimed: map[string]bool{}}
		client := &fakeRecoveryClient{posts: map[string]*model.Post{}}
		// The oldest full batch points at deleted posts.
		for i := range 4 {
			rs.stale = append(rs.stale, store.Turn{ID: fmt.Sprintf("gone-%d", i), PostID: strp(fmt.Sprintf("deleted-%d", i)), Role: "assistant", Content: textContent("x")})
		}
		rs.stale = append(rs.stale, store.Turn{ID: "turn-live", PostID: strp("post-live"), Role: "assistant", Content: textContent("x")})
		client.posts["post-live"] = &model.Post{Id: "post-live"}

		recovered, err := RecoverAbandonedStreams(context.Background(), rs, client, 2)
		require.NoError(t, err)
		require.Len(t, recovered, 1)
		assert.Equal(t, "post-live", recovered[0].PostID)
		for i := range 4 {
			assert.True(t, rs.claimed[fmt.Sprintf("gone-%d", i)], "a deleted post's turn must not be listed again")
		}
	})

	t.Run("list error is returned", func(t *testing.T) {
		rs := &fakeRecoveryStore{listErr: errors.New("db down"), claimed: map[string]bool{}}
		_, err := RecoverAbandonedStreams(context.Background(), rs, &fakeRecoveryClient{posts: map[string]*model.Post{}}, 10)
		require.Error(t, err)
	})
}

// A stream that ignores cancellation past the stop timeout must keep the post
// reserved; otherwise its late FinishStreaming would cancel the next stream.
func TestStopStreamingTimeoutKeepsPostReserved(t *testing.T) {
	service, _, _ := newRecoveryTestService(t)
	postID := newStreamPost().Id

	_, err := service.GetStreamingContext(context.Background(), postID)
	require.NoError(t, err)

	service.StopStreaming(postID) // nothing finishes the stream, so this times out

	_, err = service.GetStreamingContext(context.Background(), postID)
	require.ErrorIs(t, err, ErrAlreadyStreamingToPost)

	service.FinishStreaming(postID) // the stuck stream eventually exits
	nextCtx, err := service.GetStreamingContext(context.Background(), postID)
	require.NoError(t, err)
	require.NoError(t, nextCtx.Err())
}
