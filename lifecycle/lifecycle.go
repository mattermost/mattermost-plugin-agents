// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package lifecycle owns the plugin-level root context. Background work that
// must outlive an HTTP request or hook (LLM streaming, title generation,
// summarization) derives from Context() instead of context.Background() so
// that OnDeactivate can cancel all of it at once with ErrShutdown as the
// cause, letting streams distinguish a plugin shutdown from a user stop.
package lifecycle

import (
	"context"
	"errors"
	"sync"
)

// ErrShutdown is the cancellation cause attached to the root context when the
// plugin is deactivating.
var ErrShutdown = errors.New("plugin is shutting down")

var (
	mu     sync.RWMutex
	root   = context.Background()
	cancel context.CancelCauseFunc
)

// Start installs a fresh root context. Called from OnActivate.
func Start() {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		cancel(ErrShutdown)
	}
	root, cancel = context.WithCancelCause(context.Background())
}

// Shutdown cancels the root context with ErrShutdown. Called from
// OnDeactivate. Safe to call when Start was never called.
func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		cancel(ErrShutdown)
		cancel = nil
	}
}

// Context returns the current plugin root context. Before Start it is
// context.Background().
func Context() context.Context {
	mu.RLock()
	defer mu.RUnlock()
	return root
}

// IsShutdown reports whether ctx was canceled because the plugin is shutting
// down (as opposed to a user stop or request cancellation).
func IsShutdown(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrShutdown)
}
