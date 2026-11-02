// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAmbientLimiter(t *testing.T) {
	type step struct {
		sleep   time.Duration
		channel string
		// hold keeps the acquired slot instead of releasing it right away.
		hold bool
		// releaseHeld releases every held slot before acquiring.
		releaseHeld bool
		want        bool
	}
	repeat := func(n int, s step) []step {
		steps := make([]step, n)
		for i := range steps {
			steps[i] = s
		}
		return steps
	}
	distinctChannels := func(n int, s step) []step {
		steps := make([]step, n)
		for i := range steps {
			steps[i] = s
			steps[i].channel = fmt.Sprintf("channel-%d", i)
		}
		return steps
	}

	tests := []struct {
		name            string
		steps           []step
		maxTrackedAfter int
	}{
		{
			name: "a channel gets its burst, then one call per interval",
			steps: append(repeat(ambientChannelBurst, step{channel: "c1", want: true}),
				step{channel: "c1", want: false},
				step{sleep: ambientChannelInterval, channel: "c1", want: true},
				step{channel: "c1", want: false},
			),
		},
		{
			name: "channels are rate limited independently and a rate denial frees its slot",
			steps: append(append(repeat(ambientChannelBurst, step{channel: "c1", want: true}),
				repeat(ambientMaxConcurrentClassifications+1, step{channel: "c1", want: false})...),
				step{channel: "c2", want: true},
			),
		},
		{
			name: "the concurrency cap is shared across channels and frees on release",
			steps: append(distinctChannels(ambientMaxConcurrentClassifications, step{hold: true, want: true}),
				step{channel: "another", want: false},
				step{channel: "another", releaseHeld: true, want: true},
			),
		},
		{
			name: "tracked channels are bounded and idle ones are pruned",
			steps: append(distinctChannels(ambientMaxTrackedChannels, step{want: true}),
				step{channel: "one-too-many", want: false},
				step{sleep: ambientChannelInterval, channel: "one-too-many", want: true},
			),
			maxTrackedAfter: ambientMaxTrackedChannels,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l := newAmbientLimiter()
				var held []func()
				for i, s := range tc.steps {
					time.Sleep(s.sleep)
					if s.releaseHeld {
						for _, release := range held {
							release()
						}
						held = nil
					}
					release, ok := l.tryAcquire(s.channel)
					require.Equal(t, s.want, ok, "step %d (%s)", i, s.channel)
					if !ok {
						continue
					}
					if s.hold {
						held = append(held, release)
					} else {
						release()
					}
				}
				if tc.maxTrackedAfter > 0 {
					require.LessOrEqual(t, len(l.channels), tc.maxTrackedAfter)
				}
			})
		})
	}
}

func TestAmbientLimiterNilAllowsNothing(t *testing.T) {
	var l *ambientLimiter
	_, ok := l.tryAcquire("c1")
	require.False(t, ok)
}
