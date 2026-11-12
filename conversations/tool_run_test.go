// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/lifecycle"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const toolRunPostID = "approval-post-id"

// toolRunFixture is an approval post whose anchor turn has pending tool calls,
// wired through HandleToolCall with a resolver the test controls.
type toolRunFixture struct {
	c         *Conversations
	convStore *loadedStateFlowStore
	conv      *store.Conversation
	mmClient  *mocks.MockClient
	streaming *followUpStatusStreamingService
	lm        *loadedStateLLM
	post      *model.Post
	channel   *model.Channel

	cancelRoot context.CancelCauseFunc

	executions atomic.Int32
	running    chan struct{}
	release    chan struct{}

	// failUpdates makes UpdatePost fail, as when the post cannot be marked.
	failUpdates atomic.Bool

	mu         sync.Mutex
	heartbeats int
	updates    []*model.Post
}

// newToolRunFixture seeds toolIDs as pending calls. Each execution signals
// running and then waits for release or, when honorCancel is set, for its
// context to be canceled.
func newToolRunFixture(t *testing.T, dm bool, honorCancel bool, toolIDs ...string) *toolRunFixture {
	t.Helper()
	f := &toolRunFixture{
		running: make(chan struct{}, len(toolIDs)),
		release: make(chan struct{}),
		lm:      &loadedStateLLM{},
	}
	f.convStore, f.conv = loadedStateConversationStore()

	blocks := make([]conversation.ContentBlock, 0, len(toolIDs)+1)
	blocks = append(blocks, conversation.ContentBlock{Type: conversation.BlockTypeText, Text: "Let me look that up."})
	for _, id := range toolIDs {
		blocks = append(blocks, conversation.ContentBlock{
			Type: conversation.BlockTypeToolUse, ID: id, Name: "jira__get_issue",
			Input: json.RawMessage(`{}`), Status: conversation.StatusPending, Shared: new(false),
		})
	}
	seedClaimTestTurns(t, f.convStore, f.conv.ID, toolRunPostID, blocks, nil)

	f.post = &model.Post{Id: toolRunPostID, UserId: "bot-id", ChannelId: "channel-id", Message: "Let me look that up."}
	f.post.AddProp(streaming.ConversationIDProp, f.conv.ID)
	f.channel = &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}
	if dm {
		f.post.ChannelId = "dm-channel"
		f.channel = &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__" + testRequesterID}
	}

	f.streaming = &followUpStatusStreamingService{convStore: f.convStore, postID: toolRunPostID}
	f.c = newClaimTestConversations(t, f.convStore, f.lm, &f.streaming.loadedStateStreamingService,
		func(ctx context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			f.executions.Add(1)
			f.running <- struct{}{}
			if honorCancel {
				select {
				case <-ctx.Done():
					return "", context.Cause(ctx)
				case <-f.release:
				}
			} else {
				<-f.release
			}
			return "result", nil
		})
	f.c.streamingService = f.streaming

	root, cancelRoot := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancelRoot(nil) })
	f.cancelRoot = cancelRoot
	f.c.SetRootContext(root)

	f.mmClient = f.c.mmClient.(*mocks.MockClient)
	for _, method := range []string{"LogWarn", "LogError"} {
		f.mmClient.On(method, mock.Anything).Maybe().Return()
		for args := []any{mock.Anything}; len(args) <= 9; args = append(args, mock.Anything) {
			f.mmClient.On(method, args...).Maybe().Return()
		}
	}
	f.mmClient.On("GetChannel", mock.Anything).Maybe().Return(f.channel, nil)
	f.mmClient.On("PublishWebSocketEvent", "postupdate", mock.Anything, mock.Anything).Maybe().Run(func(args mock.Arguments) {
		payload := args.Get(1).(map[string]any)
		if payload["control"] == streaming.PostStreamingControlHeartbeat && payload["post_id"] == toolRunPostID {
			f.mu.Lock()
			f.heartbeats++
			f.mu.Unlock()
		}
	}).Return()
	f.mmClient.EXPECT().GetPost(toolRunPostID).RunAndReturn(func(string) (*model.Post, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.updates) > 0 {
			return f.updates[len(f.updates)-1].Clone(), nil
		}
		return f.post.Clone(), nil
	}).Maybe()
	f.mmClient.EXPECT().UpdatePost(mock.Anything).RunAndReturn(func(post *model.Post) error {
		if f.failUpdates.Load() {
			return errors.New("update failed")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.updates = append(f.updates, post.Clone())
		return nil
	}).Maybe()
	return f
}

// click runs the approval asynchronously and returns its result channel.
func (f *toolRunFixture) click(toolIDs ...string) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- f.c.HandleToolCall(context.Background(), testRequesterID, f.post, f.channel, toolIDs, nil)
	}()
	return errCh
}

func (f *toolRunFixture) waitRunning(t *testing.T) {
	t.Helper()
	select {
	case <-f.running:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "approved tool never started")
	}
}

func (f *toolRunFixture) waitResult(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		require.FailNow(t, "HandleToolCall did not return")
		return nil
	}
}

func (f *toolRunFixture) anchor(t *testing.T) store.Turn {
	t.Helper()
	turn, err := f.convStore.GetTurnByPostID(toolRunPostID)
	require.NoError(t, err)
	require.NotNil(t, turn)
	return *turn
}

func (f *toolRunFixture) anchorStatuses(t *testing.T) []string {
	t.Helper()
	blocks, err := conversation.UnmarshalBlocks(f.anchor(t).Content)
	require.NoError(t, err)
	var statuses []string
	for _, b := range blocks {
		if b.Type == conversation.BlockTypeToolUse {
			statuses = append(statuses, b.Status)
		}
	}
	return statuses
}

func (f *toolRunFixture) heartbeatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heartbeats
}

func (f *toolRunFixture) postUpdates() []*model.Post {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*model.Post(nil), f.updates...)
}

// followUpStatusStreamingService records the anchor turn's status when the
// follow-up stream starts, and how often the post's streaming context was held.
type followUpStatusStreamingService struct {
	loadedStateStreamingService
	convStore *loadedStateFlowStore
	postID    string

	mu              sync.Mutex
	statusAtStream  string
	streamStarted   bool
	contextAcquired int
}

func (s *followUpStatusStreamingService) GetStreamingContext(ctx context.Context, postID string) (context.Context, error) {
	if postID == s.postID {
		s.mu.Lock()
		s.contextAcquired++
		s.mu.Unlock()
	}
	return s.loadedStateStreamingService.GetStreamingContext(ctx, postID)
}

func (s *followUpStatusStreamingService) StreamContinuationToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, locale, userID string) {
	if post.Id == s.postID {
		if turn, err := s.convStore.GetTurnByPostID(post.Id); err == nil && turn != nil {
			s.mu.Lock()
			s.statusAtStream = turn.Status
			s.streamStarted = true
			s.mu.Unlock()
		}
	}
	s.loadedStateStreamingService.StreamContinuationToPost(ctx, stream, post, locale, userID)
}

func TestHandleToolCallKeepsClaimedTurnStreamingWhileToolsRun(t *testing.T) {
	f := newToolRunFixture(t, true, false, "tool-use-1")
	f.c.SetToolRunHeartbeatInterval(5 * time.Millisecond)

	errCh := f.click("tool-use-1")
	f.waitRunning(t)

	running := f.anchor(t)
	assert.Equal(t, store.TurnStatusStreaming, running.Status, "a claimed turn is streaming while its tools run")
	assert.Equal(t, []string{conversation.StatusAccepted}, f.anchorStatuses(t))
	assert.Equal(t, int64(1), f.c.ActiveToolRuns())
	firstBeat := running.UpdatedAt
	require.Eventually(t, func() bool {
		return f.anchor(t).UpdatedAt > firstBeat && f.heartbeatCount() >= 2
	}, 5*time.Second, 5*time.Millisecond, "the run must heartbeat its turn and its post")

	close(f.release)
	require.NoError(t, f.waitResult(t, errCh))
	f.streaming.waitForStreaming()

	assert.Equal(t, store.TurnStatusComplete, f.anchor(t).Status)
	assert.Equal(t, []string{conversation.StatusSuccess}, f.anchorStatuses(t))
	f.streaming.mu.Lock()
	assert.True(t, f.streaming.streamStarted, "a DM approval streams the follow-up")
	assert.Equal(t, store.TurnStatusComplete, f.streaming.statusAtStream, "the run ends before the follow-up stream takes the post")
	f.streaming.mu.Unlock()
	assert.Equal(t, int64(0), f.c.ActiveToolRuns())
	assert.Empty(t, f.postUpdates(), "a finished run does not mark the post")
}

func TestHandleToolCallInterruptedByShutdown(t *testing.T) {
	tests := []struct {
		name        string
		honorCancel bool
		// pluginLifecycle runs against the plugin's own root context,
		// canceled by lifecycle.Shutdown as OnDeactivate does.
		pluginLifecycle bool
	}{
		{name: "running tool stops on cancellation", honorCancel: true},
		{name: "running tool ignores cancellation and finishes", honorCancel: false},
		{name: "plugin lifecycle shutdown", honorCancel: true, pluginLifecycle: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newToolRunFixture(t, true, tt.honorCancel, "tool-use-1", "tool-use-2")
			shutdown := func() { f.cancelRoot(lifecycle.ErrShutdown) }
			if tt.pluginLifecycle {
				lifecycle.Start()
				// Later tests run on the default root; leave it live.
				t.Cleanup(lifecycle.Start)
				f.c.rootCtx = nil
				shutdown = lifecycle.Shutdown
			}

			errCh := f.click("tool-use-1", "tool-use-2")
			f.waitRunning(t)

			// What OnDeactivate does: cancel the root, then wait (bounded)
			// for runs to finish.
			shutdown()
			if !tt.honorCancel {
				close(f.release)
			}
			require.True(t, f.c.WaitForToolRuns(5*time.Second), "the shutdown wait must cover the run")

			// The interruption is recorded before the run stops counting.
			assert.Equal(t, store.TurnStatusInterrupted, f.anchor(t).Status)
			updates := f.postUpdates()
			require.Len(t, updates, 1)
			assert.Equal(t, streaming.InterruptedReasonShutdown, updates[0].GetProp(streaming.InterruptedProp))
			assert.Equal(t, "Let me look that up.", updates[0].Message, "the post message is kept")

			err := f.waitResult(t, errCh)
			require.ErrorIs(t, err, ErrToolRunInterrupted)
			assert.Equal(t, int32(1), f.executions.Load(), "no further approved tool starts after shutdown")
			assert.Equal(t, []string{conversation.StatusAccepted, conversation.StatusAccepted}, f.anchorStatuses(t),
				"interrupted calls are never offered for approval again")
			assert.Equal(t, 0, countToolResultTurns(t, f.convStore, f.conv.ID))
			assert.False(t, f.streaming.streamStarted, "no follow-up is streamed")

			turns, err := f.convStore.GetTurnsForConversation(f.conv.ID)
			require.NoError(t, err)
			assert.Equal(t, conversation.ApprovalStageDone, conversation.ComputePostApprovalState(turns, toolRunPostID))

			retryErr := f.c.HandleToolCall(context.Background(), testRequesterID, f.post, f.channel, []string{"tool-use-1", "tool-use-2"}, nil)
			require.ErrorIs(t, retryErr, ErrStaleToolClick)
			assert.Equal(t, int32(1), f.executions.Load(), "a retry must not re-run the calls")
		})
	}
}

// A recovery sweep that declared the run lost has the final word: results
// that arrive afterwards are not written as if nothing happened.
func TestHandleToolCallKeepsSweepVerdict(t *testing.T) {
	tests := []struct {
		name      string
		heartbeat time.Duration
		toolIDs   []string
		// finish lets the running tool return after the sweep.
		finish func(f *toolRunFixture)
	}{
		{
			name:      "tool finishes after the sweep",
			heartbeat: time.Hour,
			toolIDs:   []string{"tool-use-1"},
			finish:    func(f *toolRunFixture) { close(f.release) },
		},
		{
			name:      "heartbeat notices the sweep and halts the remaining tools",
			heartbeat: 5 * time.Millisecond,
			toolIDs:   []string{"tool-use-1", "tool-use-2"},
			finish:    func(*toolRunFixture) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newToolRunFixture(t, true, true, tt.toolIDs...)
			f.c.SetToolRunHeartbeatInterval(tt.heartbeat)

			errCh := f.click(tt.toolIDs...)
			f.waitRunning(t)

			// What streaming.RecoverAbandonedStreams does to a stale turn.
			claimed, err := f.convStore.TransitionTurnStatus(f.anchor(t).ID, store.TurnStatusStreaming, store.TurnStatusInterrupted)
			require.NoError(t, err)
			require.True(t, claimed)
			tt.finish(f)

			require.ErrorIs(t, f.waitResult(t, errCh), ErrToolRunInterrupted)
			assert.Equal(t, store.TurnStatusInterrupted, f.anchor(t).Status)
			for _, status := range f.anchorStatuses(t) {
				assert.Equal(t, conversation.StatusAccepted, status, "results are not written over the sweep's verdict")
			}
			assert.Equal(t, int32(1), f.executions.Load())
			assert.Equal(t, 0, countToolResultTurns(t, f.convStore, f.conv.ID))
			assert.False(t, f.streaming.streamStarted)
			assert.Empty(t, f.postUpdates(), "the sweep already marked the post")
			assert.Equal(t, int64(0), f.c.ActiveToolRuns())
		})
	}
}

// Regenerate must not start a second run over a live approval run, and must be
// available once the run was interrupted.
func TestHandleRegenerateDuringApprovalRun(t *testing.T) {
	f := newToolRunFixture(t, true, true, "tool-use-1")
	f.c.SetToolRunHeartbeatInterval(5 * time.Millisecond)

	errCh := f.click("tool-use-1")
	f.waitRunning(t)

	err := f.c.HandleRegenerate(context.Background(), testRequesterID, f.post, f.channel)
	require.ErrorIs(t, err, streaming.ErrAlreadyStreamingToPost)

	f.cancelRoot(lifecycle.ErrShutdown)
	require.ErrorIs(t, f.waitResult(t, errCh), ErrToolRunInterrupted)

	require.False(t, f.streaming.streamStarted)
	f.streaming.mu.Lock()
	acquiredBefore := f.streaming.contextAcquired
	f.streaming.mu.Unlock()

	// The fixture post has no regenerate source, so regenerate fails after it
	// took over the post; only getting that far matters here.
	err = f.c.HandleRegenerate(context.Background(), testRequesterID, f.post, f.channel)
	assert.NotErrorIs(t, err, streaming.ErrAlreadyStreamingToPost)
	f.streaming.mu.Lock()
	assert.Greater(t, f.streaming.contextAcquired, acquiredBefore, "regenerate takes over an interrupted approval post")
	f.streaming.mu.Unlock()
}

// When shutdown cannot stamp the post, the run's turn goes back to streaming
// for the recovery sweep, which must find it stale at once rather than after
// a fresh heartbeat timeout.
func TestHandleToolCallShutdownWithUnmarkablePostLeavesTurnForRecovery(t *testing.T) {
	f := newToolRunFixture(t, true, true, "tool-use-1")
	f.failUpdates.Store(true)

	errCh := f.click("tool-use-1")
	f.waitRunning(t)
	f.cancelRoot(lifecycle.ErrShutdown)
	require.True(t, f.c.WaitForToolRuns(5*time.Second))

	require.ErrorIs(t, f.waitResult(t, errCh), ErrToolRunInterrupted)
	assert.Equal(t, store.TurnStatusStreaming, f.anchor(t).Status, "the unmarked turn stays streaming for the sweep")
	live, err := f.convStore.HasLiveStreamingTurn(toolRunPostID, streaming.StaleStreamThreshold.Milliseconds())
	require.NoError(t, err)
	assert.False(t, live, "recovery must see the turn as abandoned immediately")
}
