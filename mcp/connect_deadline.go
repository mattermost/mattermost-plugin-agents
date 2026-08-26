// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"io"
	"time"
)

// connectWithDeadline runs one MCP connection sequence under a context that is
// canceled unless the sequence finishes within budget, and hands that same
// context to the session the sequence produced. target names the server in the
// timeout error, for example "MCP server Jira".
//
// A plain context.WithTimeout does not work here: the legacy HTTP+SSE transport
// keeps its persistent GET stream on the context passed to Connect, so
// canceling that context after a successful handshake tears the session down.
// Stopping the timer lifts the bound instead, but only once there is a session
// to protect. A session that loses the race against the timer is closed and
// reported as a timeout, because its transport may already be gone.
func connectWithDeadline[T io.Closer](parent context.Context, budget time.Duration, target string, connect func(context.Context) (T, error)) (T, error) {
	var zero T

	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(budget, cancel)

	session, err := connect(ctx)
	if err != nil {
		timer.Stop()
		cancel()
		return zero, err
	}

	// Stop reports false once the timer has fired, in which case ctx is already
	// being canceled.
	if !timer.Stop() {
		_ = session.Close()
		return zero, fmt.Errorf("timed out connecting to %s after %s", target, budget)
	}

	return session, nil
}
