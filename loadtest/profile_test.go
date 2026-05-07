// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProfileNilAndEmpty(t *testing.T) {
	t.Parallel()
	d := DefaultReadSearchHeavyProfile()
	tests := []struct {
		name   string
		raw    json.RawMessage
		assert func(*testing.T, MockProfile)
	}{
		{
			name: "nil",
			raw:  nil,
			assert: func(t *testing.T, p MockProfile) {
				require.Equal(t, d.LatencyProfiles["realistic_default"], p.LatencyProfiles["realistic_default"])
			},
		},
		{
			name: "empty",
			raw:  json.RawMessage(""),
			assert: func(t *testing.T, p MockProfile) {
				require.Equal(t, d.ProfileWeights, p.ProfileWeights)
			},
		},
		{
			name: "whitespace",
			raw:  json.RawMessage("   \n\t  "),
			assert: func(t *testing.T, p MockProfile) {
				require.Equal(t, 0.10, p.ReasoningSkipProbability)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := ParseProfile(tt.raw)
			require.NoError(t, err)
			tt.assert(t, p)
		})
	}
}

func TestDefaultLatencyMix(t *testing.T) {
	t.Parallel()
	p := DefaultReadSearchHeavyProfile()
	def := p.LatencyProfiles["realistic_default"]
	require.Equal(t, [2]int{3000, 12000}, def.TTFTMs)
	require.Equal(t, [2]int{150, 400}, def.ChunkCount)
	require.Equal(t, [2]int{30, 80}, def.ChunkIntervalMs)
	require.Equal(t, [2]int{15000, 25000}, def.TotalWallTimeMsPerRequest)

	fast := p.LatencyProfiles["realistic_fast"]
	require.Equal(t, [2]int{600, 2500}, fast.TTFTMs)
	require.Equal(t, [2]int{40, 120}, fast.ChunkCount)
	require.Equal(t, [2]int{40, 100}, fast.ChunkIntervalMs)
	require.Equal(t, [2]int{5000, 10000}, fast.TotalWallTimeMsPerRequest)

	slow := p.LatencyProfiles["realistic_slow"]
	require.Equal(t, [2]int{12000, 22000}, slow.TTFTMs)
	require.Equal(t, [2]int{400, 1000}, slow.ChunkCount)
	require.Equal(t, [2]int{15, 40}, slow.ChunkIntervalMs)
	require.Equal(t, [2]int{28000, 40000}, slow.TotalWallTimeMsPerRequest)

	require.InDelta(t, 0.70, p.ProfileWeights["realistic_default"], 1e-9)
	require.InDelta(t, 0.20, p.ProfileWeights["realistic_fast"], 1e-9)
	require.InDelta(t, 0.10, p.ProfileWeights["realistic_slow"], 1e-9)
	require.InDelta(t, 0.10, p.ReasoningSkipProbability, 1e-9)
}

func TestSummaryIncludesCriticalFields(t *testing.T) {
	t.Parallel()
	s := DefaultReadSearchHeavyProfile().Summary()
	require.Contains(t, s, "defaults_source=spikes/llm-latency-benchmark")
	require.Contains(t, s, "name=read_search_heavy_default")
	require.Contains(t, s, "seed=1")
	require.Contains(t, s, "streaming=true")
	require.Contains(t, s, "profile_weights:")
	require.Contains(t, s, "realistic_default=0.7000")
	require.Contains(t, s, "realistic_fast=0.2000")
	require.Contains(t, s, "realistic_slow=0.1000")
	require.Contains(t, s, "tool_weights:")
	require.Contains(t, s, "read_channel")
	require.Contains(t, s, "search_posts")
	require.Contains(t, s, "create_post")
	require.Contains(t, s, "reasoning_skip_p=0.1000")
	require.Contains(t, s, "max_tool_rounds=5")
	require.Contains(t, s, "latency_profiles:")
	require.Contains(t, s, "realistic_default: ttft_ms=[3000,12000]")
	require.Contains(t, s, "chunk_count=[150,400]")
	require.Contains(t, s, "chunk_interval_ms=[30,80]")
	require.Contains(t, s, "total_wall_time_ms_per_request=[15000,25000]")
	require.Contains(t, s, "realistic_fast: ttft_ms=[600,2500]")
	require.Contains(t, s, "total_wall_time_ms_per_request=[5000,10000]")
	require.Contains(t, s, "realistic_slow: ttft_ms=[12000,22000]")
	require.Contains(t, s, "total_wall_time_ms_per_request=[28000,40000]")
	require.Contains(t, s, "tool_argument_profiles:")
	require.Contains(t, s, "read_channel:")
	require.Contains(t, s, "post_limits=10,25,50,100")
	require.Contains(t, s, "search_posts:")
	require.Contains(t, s, "status update")
	require.Contains(t, s, "create_post:")
	require.Contains(t, s, "message_lengths=12,200,3500")
	require.Contains(t, s, "dm:")
	require.Contains(t, s, "usernames=alice,bob")
}

func TestSummaryDeterministic(t *testing.T) {
	t.Parallel()
	a := DefaultReadSearchHeavyProfile().Summary()
	b := DefaultReadSearchHeavyProfile().Summary()
	require.Equal(t, a, b)
}

func TestParseProfileUnknownLatencyNameRejected(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"profile_weights":{"does_not_exist":1}}`)
	_, err := ParseProfile(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown latency profile")
}

func TestParseProfileInvalidWeights(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{
			name: "negative tool weight",
			raw:  json.RawMessage(`{"tool_weights":{"read_channel":-0.1}}`),
		},
		{
			name: "empty profile weights",
			raw:  json.RawMessage(`{"profile_weights":{}}`),
		},
		{
			name: "reasoning skip probability out of range",
			raw:  json.RawMessage(`{"reasoning_skip_probability":1.5}`),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseProfile(tt.raw)
			require.Error(t, err)
		})
	}
}

func TestParseProfileInvalidLatencyRange(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"latency_profiles":{"realistic_default":{"ttft_ms":[500,100]}}}`))
	require.Error(t, err)
}

func TestParseProfilePartialLatencyProfileInheritsDefaults(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"latency_profiles":{"realistic_default":{"ttft_ms":[42,84]}}}`)
	p, err := ParseProfile(raw)
	require.NoError(t, err)

	lp := p.LatencyProfiles["realistic_default"]
	require.Equal(t, [2]int{42, 84}, lp.TTFTMs)
	require.Equal(t, [2]int{150, 400}, lp.ChunkCount)
	require.Equal(t, [2]int{30, 80}, lp.ChunkIntervalMs)
	require.Equal(t, [2]int{15000, 25000}, lp.TotalWallTimeMsPerRequest)
}

func TestParseProfileNewLatencyProfileRequiresAllFields(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"latency_profiles":{"custom":{"ttft_ms":[1,2]}}}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must define all latency fields")
}

func TestParseProfileDisallowUnknownTopLevel(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"name":"x","extra_field":true}`))
	require.Error(t, err)
}

func TestParseProfileRejectsTrailingTopLevelJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"name":"x"} {"seed":2}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected trailing JSON value")
}

func TestParseProfileMergeOverrides(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"profile_weights":{"realistic_default":1.0,"realistic_fast":0,"realistic_slow":0},
		"tool_argument_profiles":{"read_channel":{"post_limits":[99]}}
	}`)
	p, err := ParseProfile(raw)
	require.NoError(t, err)
	require.InDelta(t, 1.0, p.ProfileWeights["realistic_default"], 1e-9)
	arg := p.ToolArgumentProfiles["read_channel"]
	require.Equal(t, []int{99}, arg.PostLimits)
}

func TestValidateNaNWeight(t *testing.T) {
	t.Parallel()
	p := DefaultReadSearchHeavyProfile()
	p.ToolWeights["read_channel"] = math.NaN()
	require.Error(t, p.Validate())
}
