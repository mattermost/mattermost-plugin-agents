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
	p, err := ParseProfile(nil)
	require.NoError(t, err)
	require.Equal(t, d.LatencyProfiles["realistic_default"], p.LatencyProfiles["realistic_default"])

	p2, err := ParseProfile(json.RawMessage(""))
	require.NoError(t, err)
	require.Equal(t, d.ProfileWeights, p2.ProfileWeights)

	p3, err := ParseProfile(json.RawMessage("   \n\t  "))
	require.NoError(t, err)
	require.Equal(t, 0.10, p3.ReasoningSkipProbability)
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
	require.Contains(t, s, "profile_weights:")
	require.Contains(t, s, "realistic_default")
	require.Contains(t, s, "0.7000")
	require.Contains(t, s, "tool_weights:")
	require.Contains(t, s, "read_channel")
	require.Contains(t, s, "reasoning_skip_p=0.1000")
	require.Contains(t, s, "max_tool_rounds=5")
	require.Contains(t, s, "latency_profiles:")
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
	_, err := ParseProfile(json.RawMessage(`{"tool_weights":{"read_channel":-0.1}}`))
	require.Error(t, err)

	_, err = ParseProfile(json.RawMessage(`{"profile_weights":{}}`))
	require.Error(t, err)

	_, err = ParseProfile(json.RawMessage(`{"reasoning_skip_probability":1.5}`))
	require.Error(t, err)
}

func TestParseProfileInvalidLatencyRange(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"latency_profiles":{"realistic_default":{"ttft_ms":[500,100]}}}`))
	require.Error(t, err)
}

func TestParseProfileDisallowUnknownTopLevel(t *testing.T) {
	t.Parallel()
	_, err := ParseProfile(json.RawMessage(`{"name":"x","extra_field":true}`))
	require.Error(t, err)
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
