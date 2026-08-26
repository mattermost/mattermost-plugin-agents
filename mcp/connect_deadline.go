// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"sync"
	"time"
)

// connectDeadline bounds one MCP connection sequence without shortening the
// session it produces.
//
// A plain context.WithTimeout does not work here: the legacy HTTP+SSE transport
// keeps its persistent GET stream on the context handed to Connect, so
// canceling that context after a successful handshake tears the session down.
// A connectDeadline instead cancels only while the sequence is still in
// progress, and is disarmed once the session is established.
type connectDeadline struct {
	ctx   context.Context
	timer *time.Timer

	mu      sync.Mutex
	cancel  context.CancelFunc
	expired bool
}

// newConnectDeadline arms a deadline that cancels its context unless keep is
// called within budget.
func newConnectDeadline(parent context.Context, budget time.Duration) *connectDeadline {
	ctx, cancel := context.WithCancel(parent)
	deadline := &connectDeadline{ctx: ctx, cancel: cancel}
	deadline.timer = time.AfterFunc(budget, deadline.expire)
	return deadline
}

func (d *connectDeadline) expire() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel == nil {
		return
	}
	d.cancel()
	d.cancel = nil
	d.expired = true
}

// keep disarms the deadline and hands its context to the established session.
// It reports false when the deadline already fired, in which case the caller
// must discard the session and fail: the transport may already be torn down.
func (d *connectDeadline) keep() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.timer.Stop()
	if d.expired {
		return false
	}
	d.cancel = nil
	return true
}

// abandon cancels the sequence, releasing any in-flight network work. It is a
// no-op once keep has succeeded.
func (d *connectDeadline) abandon() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.timer.Stop()
	if d.cancel == nil {
		return
	}
	d.cancel()
	d.cancel = nil
}
