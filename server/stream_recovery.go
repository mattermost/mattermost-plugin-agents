// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/lifecycle"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// AuditEventRecoverInterruptedStream is emitted once per bot post the
// abandoned-stream sweep marks as interrupted (a system-initiated post edit).
const AuditEventRecoverInterruptedStream = "recoverInterruptedStream"

const (
	// recoverySecondSweepDelay schedules one follow-up sweep after activation
	// so streams that were still heartbeating when this node activated (e.g.
	// on a node that died moments ago) are caught once they go stale. Must
	// exceed StaleStreamThreshold.
	recoverySecondSweepDelay = 75 * time.Second

	// shutdownStreamWait bounds how long OnDeactivate waits for streams to
	// finalize. Mattermost kills the plugin 10s into OnDeactivate.
	shutdownStreamWait = 7 * time.Second
)

// recoveryClient adapts pluginapi to streaming.RecoveryClient.
type recoveryClient struct {
	api *pluginapi.Client
}

func (c recoveryClient) GetPost(postID string) (*model.Post, error) {
	return c.api.Post.GetPost(postID)
}

func (c recoveryClient) UpdatePost(post *model.Post) error {
	return c.api.Post.UpdatePost(post)
}

func (c recoveryClient) LogWarn(msg string, kv ...any) { c.api.Log.Warn(msg, kv...) }
func (c recoveryClient) LogInfo(msg string, kv ...any) { c.api.Log.Info(msg, kv...) }

// sweepAbandonedStreams runs one recovery pass and audits each repaired post.
func (p *Plugin) sweepAbandonedStreams(ctx context.Context) {
	if p.store == nil || p.pluginAPI == nil {
		return
	}
	recovered, err := streaming.RecoverAbandonedStreams(ctx, p.store, recoveryClient{api: p.pluginAPI}, streaming.DefaultRecoveryBatchSize)
	if err != nil {
		p.pluginAPI.Log.Warn("Abandoned stream recovery sweep failed", "error", err)
	}
	for _, r := range recovered {
		rec := plugin.MakeAuditRecord(AuditEventRecoverInterruptedStream, model.AuditStatusSuccess)
		audit.AddParam(rec, audit.KeyPostID, r.PostID)
		audit.AddParam(rec, audit.KeyChannelID, r.ChannelID)
		p.API.LogAuditRec(rec)
	}
}

// startStreamRecovery runs the sweep now and once more after
// recoverySecondSweepDelay. There is deliberately no periodic job: a stream
// can only be abandoned by a node dying, and every node activation (restart,
// upgrade, replacement pod) re-runs this.
func (p *Plugin) startStreamRecovery() {
	ctx := lifecycle.Context()
	go p.sweepAbandonedStreams(ctx)
	p.recoveryMu.Lock()
	defer p.recoveryMu.Unlock()
	p.recoveryTimer = time.AfterFunc(recoverySecondSweepDelay, func() {
		if ctx.Err() != nil {
			return
		}
		p.sweepAbandonedStreams(ctx)
	})
}

// stopStreamsForShutdown cancels the follow-up sweep, interrupts all
// in-flight streams on this node (they finalize as interrupted/"shutdown")
// and waits up to shutdownStreamWait for them. Streams that do not finish are
// left in the streaming state for the next activation's sweep.
func (p *Plugin) stopStreamsForShutdown() {
	p.recoveryMu.Lock()
	if p.recoveryTimer != nil {
		p.recoveryTimer.Stop()
		p.recoveryTimer = nil
	}
	p.recoveryMu.Unlock()

	lifecycle.Shutdown()

	if p.streamService != nil && !p.streamService.WaitForActiveStreams(shutdownStreamWait) && p.pluginAPI != nil {
		p.pluginAPI.Log.Warn("Some LLM streams did not finalize before shutdown; they will be recovered on next activation",
			"remaining", p.streamService.ActiveStreams())
	}
}
