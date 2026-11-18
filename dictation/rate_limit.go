// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"slices"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
)

// The limits bound provider cost, not correctness, so they are kept in
// memory per node.
//
// They bound keys, not sessions: a key can open any number of concurrent
// sessions until it expires, and each session can run for the provider's
// maximum of 60 minutes. The server cannot end a session once a key is
// minted, so a user can still run several sessions from one key; the mint
// rate is what bounds the total.
const (
	mintsPerWindow = 10
	mintWindow     = time.Minute
	// maxUnexpiredKeys caps keys a user holds that can still open sessions.
	maxUnexpiredKeys = 2
	// pendingKeyTTL holds a reservation while its mint is in flight.
	pendingKeyTTL = 30 * time.Second
)

type userMints struct {
	attempts []time.Time
	keys     []*keyReservation
}

// keyReservation is an unexpired key, or a mint still in flight.
type keyReservation struct {
	expires time.Time
}

type rateLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	users map[string]*userMints
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{now: time.Now, users: make(map[string]*userMints)}
}

// reserve records a mint attempt for userID. The returned release must be
// called with the minted session, or nil when the mint failed, which frees
// the reservation.
func (l *rateLimiter) reserve(userID string) (release func(*bifrost.TranscriptionSession), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)
	u := l.users[userID]
	if u == nil {
		u = &userMints{}
		l.users[userID] = u
	}
	if len(u.attempts) >= mintsPerWindow || len(u.keys) >= maxUnexpiredKeys {
		return nil, false
	}

	key := &keyReservation{expires: now.Add(pendingKeyTTL)}
	u.attempts = append(u.attempts, now)
	u.keys = append(u.keys, key)

	return func(session *bifrost.TranscriptionSession) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if session == nil {
			key.expires = time.Time{}
			return
		}
		key.expires = time.UnixMilli(session.ExpiresAt)
	}, true
}

// pruneLocked drops expired entries and users with no recent activity. mu
// must be held.
func (l *rateLimiter) pruneLocked(now time.Time) {
	windowStart := now.Add(-mintWindow)
	for id, u := range l.users {
		u.attempts = slices.DeleteFunc(u.attempts, func(t time.Time) bool { return !t.After(windowStart) })
		u.keys = slices.DeleteFunc(u.keys, func(k *keyReservation) bool { return !k.expires.After(now) })
		if len(u.attempts) == 0 && len(u.keys) == 0 {
			delete(l.users, id)
		}
	}
}
