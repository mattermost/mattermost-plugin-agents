// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"sync"
	"time"
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

type channelBucket struct {
	tokens float64
	last   time.Time
}

func (b *channelBucket) tokensAt(now time.Time) float64 {
	if !now.After(b.last) {
		return b.tokens
	}
	refill := float64(now.Sub(b.last)) / float64(ambientChannelInterval)
	return min(b.tokens+refill, float64(ambientChannelBurst))
}

func (b *channelBucket) allow(now time.Time) bool {
	tokens := b.tokensAt(now)
	if now.After(b.last) {
		b.last = now
	}
	if tokens < 1 {
		b.tokens = tokens
		return false
	}
	b.tokens = tokens - 1
	return true
}

// ambientLimiter bounds ambient classifier calls: a concurrency cap shared by
// every channel and a rate limit per channel. Every check is non-blocking, and
// a call over any limit is skipped rather than queued.
type ambientLimiter struct {
	slots chan struct{}

	mu       sync.Mutex
	channels map[string]*channelBucket
}

func newAmbientLimiter() *ambientLimiter {
	return &ambientLimiter{
		slots:    make(chan struct{}, ambientMaxConcurrentClassifications),
		channels: make(map[string]*channelBucket),
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

	bucket, tracked := l.channels[channelID]
	if !tracked {
		if len(l.channels) >= ambientMaxTrackedChannels {
			l.pruneIdle(now)
			if len(l.channels) >= ambientMaxTrackedChannels {
				return false
			}
		}
		bucket = &channelBucket{
			tokens: ambientChannelBurst,
			last:   now,
		}
		l.channels[channelID] = bucket
	}
	return bucket.allow(now)
}

// pruneIdle drops channels whose limiter has refilled to its full burst; a
// fresh limiter behaves the same, so dropping them changes no decision.
func (l *ambientLimiter) pruneIdle(now time.Time) {
	for channelID, bucket := range l.channels {
		if bucket.tokensAt(now) >= ambientChannelBurst {
			delete(l.channels, channelID)
		}
	}
}
