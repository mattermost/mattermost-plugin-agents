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

type rateLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	users map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{now: time.Now, users: make(map[string][]time.Time)}
}

// allow records a session start for userID and reports whether it is
// within the limit.
func (l *rateLimiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	windowStart := now.Add(-sessionStartWindow)
	for id, starts := range l.users {
		starts = slices.DeleteFunc(starts, func(t time.Time) bool { return !t.After(windowStart) })
		if len(starts) == 0 {
			delete(l.users, id)
		} else {
			l.users[id] = starts
		}
	}
	if len(l.users[userID]) >= sessionStartsPerWindow {
		return false
	}
	l.users[userID] = append(l.users[userID], now)
	return true
}
