// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/lifecycle"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrToolRunInterrupted is returned when approved tool calls did not finish
// because the plugin shut down, or because stream recovery declared the run
// lost while they ran. The post carries the interruption notice instead of
// results; the HTTP layer maps this to 503 Service Unavailable.
var ErrToolRunInterrupted = errors.New("approved tool calls were interrupted")

// errToolRunTakenOver cancels a run whose claimed turn left the streaming
// state while its tools ran (a recovery sweep marked it interrupted).
var errToolRunTakenOver = errors.New("approval tool run was marked interrupted by stream recovery")

// toolRun covers approved tool calls from the claim until their results are
// saved. The claimed turn stays in the streaming state and heartbeats, so a
// recovery sweep treats a run whose node died like any abandoned stream, and
// plugin shutdown cancels ctx with lifecycle.ErrShutdown.
type toolRun struct {
	ctx           context.Context
	root          context.Context
	cancel        context.CancelCauseFunc
	stopRootWatch func() bool
	stop          chan struct{}
	done          chan struct{}
	stopOnce      sync.Once
	endOnce       sync.Once
}

// SetRootContext overrides lifecycle.Context() as the context whose
// cancellation interrupts approved tool runs. Tests only.
func (c *Conversations) SetRootContext(ctx context.Context) {
	c.rootCtx = ctx
}

// SetToolRunHeartbeatInterval overrides streaming.DefaultHeartbeatInterval
// for approved tool runs. Tests only.
func (c *Conversations) SetToolRunHeartbeatInterval(d time.Duration) {
	c.toolRunHeartbeatInterval = d
}

func (c *Conversations) root() context.Context {
	if c.rootCtx != nil {
		return c.rootCtx
	}
	return lifecycle.Context()
}

// ActiveToolRuns returns the number of approved tool runs on this node.
func (c *Conversations) ActiveToolRuns() int64 {
	return c.activeToolRuns.Load()
}

// WaitForToolRuns blocks until no approved tool run is active on this node or
// the timeout elapses. Returns true if all runs ended.
func (c *Conversations) WaitForToolRuns(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for c.activeToolRuns.Load() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// startToolRun begins the run for turnID, whose claim already moved it to the
// streaming state. The caller must call endToolRun.
func (c *Conversations) startToolRun(ctx context.Context, turnID string, post *model.Post) *toolRun {
	c.activeToolRuns.Add(1)
	root := c.root()
	runCtx, cancel := context.WithCancelCause(ctx)
	r := &toolRun{
		ctx:           runCtx,
		root:          root,
		cancel:        cancel,
		stopRootWatch: context.AfterFunc(root, func() { cancel(context.Cause(root)) }),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}

	interval := c.toolRunHeartbeatInterval
	if interval <= 0 {
		interval = streaming.DefaultHeartbeatInterval
	}
	go c.heartbeatToolRun(r, turnID, post, interval)
	return r
}

// heartbeatToolRun keeps the claimed turn fresh for the recovery sweep and
// tells clients the post is still being worked on. It stops on shutdown so
// that a run which cannot record its interruption in time is left stale for
// the next activation's sweep.
func (c *Conversations) heartbeatToolRun(r *toolRun, turnID string, post *model.Post, interval time.Duration) {
	defer close(r.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-r.root.Done():
			return
		case <-ticker.C:
			still, err := c.convService.TouchStreamingTurn(turnID)
			if err != nil {
				c.mmClient.LogWarn("Failed to heartbeat approved tool run", "error", err, "post_id", post.Id, "turn_id", turnID)
				continue
			}
			if !still {
				r.cancel(errToolRunTakenOver)
				return
			}
			c.mmClient.PublishWebSocketEvent("postupdate", map[string]any{
				"post_id": post.Id,
				"control": streaming.PostStreamingControlHeartbeat,
			}, &model.WebsocketBroadcast{ChannelId: post.ChannelId, ReliableClusterSend: true})
		}
	}
}

// stopHeartbeat stops the heartbeat and waits for it to exit.
func (r *toolRun) stopHeartbeat() {
	r.stopOnce.Do(func() {
		close(r.stop)
		<-r.done
	})
}

// shuttingDown reports whether plugin shutdown interrupted the run. The root
// is checked too: ctx learns of the shutdown asynchronously.
func (r *toolRun) shuttingDown() bool {
	return lifecycle.IsShutdown(r.ctx) || lifecycle.IsShutdown(r.root)
}

// halted reports whether no further approved tool may start.
func (r *toolRun) halted() bool {
	return r.shuttingDown() || errors.Is(context.Cause(r.ctx), errToolRunTakenOver)
}

// endToolRun releases the run. It runs after the run's final turn and post
// writes so that the shutdown wait covers them.
func (c *Conversations) endToolRun(r *toolRun) {
	r.endOnce.Do(func() {
		r.stopHeartbeat()
		r.stopRootWatch()
		r.cancel(nil)
		c.activeToolRuns.Add(-1)
	})
}

// interruptToolRun records a halted run: on shutdown the turn becomes
// interrupted and its post carries the shutdown notice, so the user is offered
// Regenerate instead of approving calls that may already have had side
// effects. The claimed tool_use blocks stay accepted (never pending again).
func (c *Conversations) interruptToolRun(r *toolRun, turnID string, post *model.Post) error {
	r.stopHeartbeat()

	if !r.shuttingDown() {
		c.mmClient.LogError("Approved tool run was marked interrupted by stream recovery while its tools ran; results discarded", "post_id", post.Id, "turn_id", turnID)
		return fmt.Errorf("turn %s left the streaming state while its tool calls ran: %w", turnID, ErrToolRunInterrupted)
	}

	moved, err := c.convService.TransitionTurnStatus(turnID, store.TurnStatusStreaming, store.TurnStatusInterrupted)
	if err != nil {
		// The turn stays streaming without a heartbeat; the next
		// activation's sweep marks the post.
		c.mmClient.LogError("Failed to mark interrupted approved tool run", "error", err, "post_id", post.Id, "turn_id", turnID)
		return fmt.Errorf("failed to mark turn %s interrupted: %w: %w", turnID, err, ErrToolRunInterrupted)
	}
	if !moved {
		c.mmClient.LogError("Approved tool run was marked interrupted by stream recovery before shutdown recorded it", "post_id", post.Id, "turn_id", turnID)
		return fmt.Errorf("turn %s left the streaming state while its tool calls ran: %w", turnID, ErrToolRunInterrupted)
	}

	if err := c.markPostInterrupted(post.Id, streaming.InterruptedReasonShutdown); err != nil {
		c.mmClient.LogError("Failed to mark post of interrupted approved tool run; leaving it for stream recovery", "error", err, "post_id", post.Id, "turn_id", turnID)
		if _, releaseErr := c.convService.TransitionTurnStatus(turnID, store.TurnStatusInterrupted, store.TurnStatusStreaming); releaseErr != nil {
			c.mmClient.LogError("Failed to release interrupted approved tool run for stream recovery", "error", releaseErr, "post_id", post.Id, "turn_id", turnID)
		}
		return fmt.Errorf("failed to mark post %s interrupted: %w: %w", post.Id, err, ErrToolRunInterrupted)
	}

	c.mmClient.PublishWebSocketEvent("postupdate", map[string]any{
		"post_id": post.Id,
		"control": streaming.PostStreamingControlEnd,
	}, &model.WebsocketBroadcast{ChannelId: post.ChannelId, ReliableClusterSend: true})
	return fmt.Errorf("tool calls on post %s were interrupted by plugin shutdown: %w", post.Id, ErrToolRunInterrupted)
}

// markPostInterrupted re-reads the post so only the interruption props change.
func (c *Conversations) markPostInterrupted(postID, reason string) error {
	post, err := c.mmClient.GetPost(postID)
	if err != nil {
		return fmt.Errorf("failed to get post: %w", err)
	}
	streaming.MarkInterrupted(post, reason, model.GetMillis())
	if err := c.mmClient.UpdatePost(post); err != nil {
		return fmt.Errorf("failed to update post: %w", err)
	}
	return nil
}

// isClaimedToolRunTurn reports whether turn is an approval run in progress:
// streaming with tool calls a claim already accepted. Such a turn never
// becomes pending again, so a click on it need not wait for it to settle.
func isClaimedToolRunTurn(turn store.Turn) bool {
	if turn.Status != store.TurnStatusStreaming {
		return false
	}
	blocks, err := conversation.UnmarshalBlocks(turn.Content)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(blocks, func(b conversation.ContentBlock) bool {
		return b.Type == conversation.BlockTypeToolUse && b.Status == conversation.StatusAccepted
	})
}
