// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import "sync"

// waiterRegistry tracks parents blocked on sub-turn completion, keyed by the
// delegation conversation ID. Signals are edge-triggered and coalesced: the
// waiter re-reads the conversation state from the database on every wake, so
// a lost intermediate signal can never lose data.
type waiterRegistry struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}
}

func newWaiterRegistry() *waiterRegistry {
	return &waiterRegistry{waiters: make(map[string]chan struct{})}
}

// register creates (or returns the existing) wake channel for a delegation.
func (r *waiterRegistry) register(conversationID string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waiters[conversationID]; ok {
		return ch
	}
	ch := make(chan struct{}, 1)
	r.waiters[conversationID] = ch
	return ch
}

// deregister removes the wake channel for a delegation.
func (r *waiterRegistry) deregister(conversationID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiters, conversationID)
}

// signal wakes the waiter for a delegation, if any. Returns whether a waiter
// was present.
func (r *waiterRegistry) signal(conversationID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.waiters[conversationID]
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
	default: // already signaled; the pending wake covers this notification
	}
	return true
}
