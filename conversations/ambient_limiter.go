// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limits are per node; a cluster of N nodes allows up to N times as much.
const (
	ambientMaxConcurrentClassifications = 8
	// Each channel may classify a burst of ambientChannelBurst posts, then one
	// post per ambientChannelInterval.
	ambientChannelBurst    = 6
	ambientChannelInterval = 5 * time.Second
	// ambientMaxTrackedChannels bounds the per-channel limiter state.
	ambientMaxTrackedChannels = 1024
)

// ambientLimiter bounds ambient classifier calls: a concurrency cap shared by
// every channel and a rate limit per channel. Every check is non-blocking, and
// a call over any limit is skipped rather than queued.
type ambientLimiter struct {
	slots chan struct{}

	mu       sync.Mutex
	channels map[string]*rate.Limiter
}

func newAmbientLimiter() *ambientLimiter {
	return &ambientLimiter{
		slots:    make(chan struct{}, ambientMaxConcurrentClassifications),
		channels: make(map[string]*rate.Limiter),
	}
}

// tryAcquire reserves one classifier call in channelID. ok is false when the
// node is at its concurrency cap, the channel is over its rate, or no more
// channels can be tracked; otherwise the caller must call release once the
// call ends. A nil limiter allows nothing.
func (l *ambientLimiter) tryAcquire(channelID string) (release func(), ok bool) {
	if l == nil {
		return nil, false
	}
	select {
	case l.slots <- struct{}{}:
	default:
		return nil, false
	}
	if !l.allowChannel(channelID) {
		<-l.slots
		return nil, false
	}
	return func() { <-l.slots }, true
}

func (l *ambientLimiter) allowChannel(channelID string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	limiter, tracked := l.channels[channelID]
	if !tracked {
		if len(l.channels) >= ambientMaxTrackedChannels {
			l.pruneIdle(now)
			if len(l.channels) >= ambientMaxTrackedChannels {
				return false
			}
		}
		limiter = rate.NewLimiter(rate.Every(ambientChannelInterval), ambientChannelBurst)
		l.channels[channelID] = limiter
	}
	return limiter.AllowN(now, 1)
}

// pruneIdle drops channels whose limiter has refilled to its full burst; a
// fresh limiter behaves the same, so dropping them changes no decision.
func (l *ambientLimiter) pruneIdle(now time.Time) {
	for channelID, limiter := range l.channels {
		if limiter.TokensAt(now) >= ambientChannelBurst {
			delete(l.channels, channelID)
		}
	}
}
