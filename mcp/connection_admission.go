// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// maxNodeConnections is the per-ClientManager cap on overlapping remote and
// plugin MCP connection sequences. It matches the per-request dial cap so one
// cold request can still use the full local budget when the node is otherwise idle.
const maxNodeConnections = maxConcurrentConnections

// errAdmissionUnavailable marks a local gate failure: shutdown or a canceled
// wait for a permit. It is never an upstream MCP server error, so it must not
// be remembered as a sticky remote connect failure.
var errAdmissionUnavailable = errors.New("mcp connection admission unavailable")

// connectionAdmission is a per-ClientManager permit gate for runtime network
// MCP connection sequences. It is not process-global and is not shared across
// cluster nodes. A nil gate admits everything, which keeps directly
// constructed test managers usable.
type connectionAdmission struct {
	sem       chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newConnectionAdmission(limit int) *connectionAdmission {
	if limit <= 0 {
		limit = maxNodeConnections
	}
	return &connectionAdmission{
		sem:    make(chan struct{}, limit),
		closed: make(chan struct{}),
	}
}

func (g *connectionAdmission) acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}

	select {
	case g.sem <- struct{}{}:
		// Close and a free slot can be ready together, and select picks
		// between them at random. Re-check so a post-shutdown acquire cannot
		// leave holding a permit nobody will wait for.
		select {
		case <-g.closed:
			<-g.sem
			return fmt.Errorf("%w: client manager closed", errAdmissionUnavailable)
		default:
			return nil
		}
	case <-g.closed:
		return fmt.Errorf("%w: client manager closed", errAdmissionUnavailable)
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", errAdmissionUnavailable, ctx.Err())
	}
}

func (g *connectionAdmission) release() {
	if g == nil {
		return
	}
	<-g.sem
}

func (g *connectionAdmission) close() {
	if g == nil {
		return
	}
	g.closeOnce.Do(func() {
		close(g.closed)
	})
}
