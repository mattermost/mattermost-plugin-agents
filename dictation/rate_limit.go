// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
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
	// expiries holds the expiry of each unexpired key or in-flight mint.
	expiries []time.Time
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
	if len(u.attempts) >= mintsPerWindow || len(u.expiries) >= maxUnexpiredKeys {
		return nil, false
	}

	pending := now.Add(pendingKeyTTL)
	u.attempts = append(u.attempts, now)
	u.expiries = append(u.expiries, pending)

	return func(session *bifrost.TranscriptionSession) {
		l.mu.Lock()
		defer l.mu.Unlock()
		u := l.users[userID]
		if u == nil {
			return
		}
		for i, exp := range u.expiries {
			if !exp.Equal(pending) {
				continue
			}
			if session == nil {
				u.expiries = append(u.expiries[:i], u.expiries[i+1:]...)
			} else {
				u.expiries[i] = time.UnixMilli(session.ExpiresAt)
			}
			break
		}
	}, true
}

// pruneLocked drops users with no recent activity. mu must be held.
func (l *rateLimiter) pruneLocked(now time.Time) {
	for id, u := range l.users {
		u.attempts = keepAfter(u.attempts, now.Add(-mintWindow))
		u.expiries = keepAfter(u.expiries, now)
		if len(u.attempts) == 0 && len(u.expiries) == 0 {
			delete(l.users, id)
		}
	}
}

func keepAfter(times []time.Time, cutoff time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}
