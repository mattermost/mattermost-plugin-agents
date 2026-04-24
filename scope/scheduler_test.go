// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

func TestNextFireBucket(t *testing.T) {
	tests := []struct {
		name            string
		intervalSeconds int64
		nowUnix         int64
		want            int64
	}{
		{
			name:            "aligned to bucket start",
			intervalSeconds: 3600,
			nowUnix:         3600,
			want:            7200,
		},
		{
			name:            "just after a bucket boundary",
			intervalSeconds: 3600,
			nowUnix:         3601,
			want:            7200,
		},
		{
			name:            "near the next boundary",
			intervalSeconds: 3600,
			nowUnix:         7199,
			want:            7200,
		},
		{
			name:            "zero interval produces zero",
			intervalSeconds: 0,
			nowUnix:         1000,
			want:            0,
		},
		{
			name:            "negative interval produces zero",
			intervalSeconds: -10,
			nowUnix:         1000,
			want:            0,
		},
		{
			name:            "daily bucket",
			intervalSeconds: 86400,
			nowUnix:         86400*3 + 1,
			want:            86400 * 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NextFireBucket(tc.intervalSeconds, time.Unix(tc.nowUnix, 0))
			if got != tc.want {
				t.Fatalf("NextFireBucket(%d, %d)=%d, want %d", tc.intervalSeconds, tc.nowUnix, got, tc.want)
			}
		})
	}
}

// TestNextFireBucket_RestartDeterminism proves two calls at different
// wall-clock instants within the same bucket yield the same next-fire.
// This is the guarantee schedule HA relies on: a plugin restart or a second
// HA node computing the same answer.
func TestNextFireBucket_RestartDeterminism(t *testing.T) {
	interval := int64(3600)

	// Start just after a bucket boundary so all offsets below the interval
	// stay inside the same bucket. Without this alignment, 1_700_000_000 is
	// 800s into its bucket and a 3599s offset rolls into the next bucket.
	alignedStart := int64(1_700_000_000) - (1_700_000_000 % interval)
	base := time.Unix(alignedStart+1, 0)

	first := NextFireBucket(interval, base)
	for _, offsetSec := range []int64{1, 5, 60, 600, interval - 2} {
		got := NextFireBucket(interval, base.Add(time.Duration(offsetSec)*time.Second))
		if got != first {
			t.Fatalf("bucket drift: t+%ds gave %d, want %d", offsetSec, got, first)
		}
	}
}

func TestDueNow(t *testing.T) {
	now := time.Unix(10_000, 0)

	tests := []struct {
		name string
		sch  llm.AgentSchedule
		want bool
	}{
		{
			name: "not yet due",
			sch:  llm.AgentSchedule{IntervalSeconds: 3600, NextFireAt: 10_001},
			want: false,
		},
		{
			name: "due exactly now",
			sch:  llm.AgentSchedule{IntervalSeconds: 3600, NextFireAt: 10_000},
			want: true,
		},
		{
			name: "overdue",
			sch:  llm.AgentSchedule{IntervalSeconds: 3600, NextFireAt: 9_999},
			want: true,
		},
		{
			name: "unset NextFireAt is not due (prevents avalanche)",
			sch:  llm.AgentSchedule{IntervalSeconds: 3600, NextFireAt: 0},
			want: false,
		},
		{
			name: "zero interval never due",
			sch:  llm.AgentSchedule{IntervalSeconds: 0, NextFireAt: 1},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dueNow(tc.sch, now); got != tc.want {
				t.Fatalf("dueNow=%v, want %v", got, tc.want)
			}
		})
	}
}
