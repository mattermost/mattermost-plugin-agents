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

var (
	// errConnectionAdmission marks a local gate or shutdown failure. It is
	// never an upstream MCP server error and must not be remembered as a
	// sticky remote connect failure.
	errConnectionAdmission = errors.New("mcp connection admission failed")
	errManagerClosed       = errors.New("mcp client manager closed")
)

func admissionError(err error) error {
	if err == nil {
		return errConnectionAdmission
	}
	return fmt.Errorf("%w: %w", errConnectionAdmission, err)
}

func isConnectionAdmissionError(err error) bool {
	return errors.Is(err, errConnectionAdmission)
}

// connectionAdmission is a per-ClientManager permit gate for runtime network
// MCP connection sequences. It is not process-global and is not shared across
// cluster nodes.
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
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-g.closed:
		return admissionError(errManagerClosed)
	default:
	}

	select {
	case g.sem <- struct{}{}:
		// Close and a free slot can be ready together. Re-check so a
		// post-shutdown acquire cannot leave with a permit.
		select {
		case <-g.closed:
			<-g.sem
			return admissionError(errManagerClosed)
		default:
			return nil
		}
	case <-g.closed:
		return admissionError(errManagerClosed)
	case <-ctx.Done():
		return admissionError(ctx.Err())
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
