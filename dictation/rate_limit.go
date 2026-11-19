// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"slices"
	"sync"
	"time"
)

// sessionStartsPerWindow bounds how often one user can start sessions on one
// node. The concurrent-session cap in sessionLeases is the cluster-wide
// bound; this one only damps rapid retries.
const (
	sessionStartsPerWindow = 10
	sessionStartWindow     = time.Minute
)

// sessionEndsPerWindow bounds end requests per user and node, so repeated
// ends of a call whose hangup keeps failing can't flood the provider.
const sessionEndsPerWindow = 30

type rateLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	limit  int
	window time.Duration
	users  map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{now: time.Now, limit: limit, window: window, users: make(map[string][]time.Time)}
}

// allow records an attempt by userID and reports whether it is within the
// limit.
func (l *rateLimiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	windowStart := now.Add(-l.window)
	for id, starts := range l.users {
		starts = slices.DeleteFunc(starts, func(t time.Time) bool { return !t.After(windowStart) })
		if len(starts) == 0 {
			delete(l.users, id)
		} else {
			l.users[id] = starts
		}
	}
	if len(l.users[userID]) >= l.limit {
		return false
	}
	l.users[userID] = append(l.users[userID], now)
	return true
}
