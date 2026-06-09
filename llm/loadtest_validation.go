// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/toolrunner/limits"
)

type loadTestMockProfileOverlay struct {
	Name                     *string                                       `json:"name,omitempty"`
	Seed                     *int64                                        `json:"seed,omitempty"`
	LatencyProfiles          map[string]loadTestLatencyProfileOverlay      `json:"latency_profiles,omitempty"`
	ProfileWeights           map[string]float64                            `json:"profile_weights,omitempty"`
	ReasoningSkipProbability *float64                                      `json:"reasoning_skip_probability,omitempty"`
	StreamingEnabled         *bool                                         `json:"streaming_enabled,omitempty"`
	ToolUseProbability       *float64                                      `json:"tool_use_probability,omitempty"`
	ToolWeights              map[string]float64                            `json:"tool_weights,omitempty"`
	MaxToolRounds            *int                                          `json:"max_tool_rounds,omitempty"`
	ToolArgumentProfiles     map[string]loadTestToolArgumentProfileOverlay `json:"tool_argument_profiles,omitempty"`
	FinalResponseTemplates   []string                                      `json:"final_response_templates,omitempty"`
}

type loadTestLatencyProfileOverlay struct {
	TTFTMs                    *[2]int `json:"ttft_ms,omitempty"`
	ChunkCount                *[2]int `json:"chunk_count,omitempty"`
	ChunkIntervalMs           *[2]int `json:"chunk_interval_ms,omitempty"`
	TotalWallTimeMsPerRequest *[2]int `json:"total_wall_time_ms_per_request,omitempty"`
}

type loadTestToolArgumentProfileOverlay struct {
	PostLimits     []int    `json:"post_limits,omitempty"`
	SearchQueries  []string `json:"search_queries,omitempty"`
	SearchLimits   []int    `json:"search_limits,omitempty"`
	MessageLengths []int    `json:"message_lengths,omitempty"`
	Usernames      []string `json:"usernames,omitempty"`
	ChannelIDs     []string `json:"channel_ids,omitempty"`
	ChannelNames   []string `json:"channel_names,omitempty"`
	TeamIDs        []string `json:"team_ids,omitempty"`
	TeamNames      []string `json:"team_names,omitempty"`
	PostIDs        []string `json:"post_ids,omitempty"`
}

type loadTestLatencyProfile struct {
	TTFTMs                    [2]int
	ChunkCount                [2]int
	ChunkIntervalMs           [2]int
	TotalWallTimeMsPerRequest [2]int
}

func isValidLoadTestMockConfig(raw json.RawMessage) bool {
	if raw == nil || len(bytes.TrimSpace(raw)) == 0 {
		return true
	}

	var ov loadTestMockProfileOverlay
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ov); err != nil {
		return false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return false
	}

	latencyProfiles := defaultLoadTestLatencyProfiles()
	if ov.LatencyProfiles != nil {
		if len(ov.LatencyProfiles) == 0 {
			latencyProfiles = map[string]loadTestLatencyProfile{}
		} else {
			for name, overlay := range ov.LatencyProfiles {
				existing, ok := latencyProfiles[name]
				if !ok && !overlay.isComplete() {
					return false
				}
				latencyProfiles[name] = overlay.applyTo(existing)
			}
		}
	}

	profileWeights := defaultLoadTestProfileWeights()
	if ov.ProfileWeights != nil {
		if len(ov.ProfileWeights) == 0 {
			profileWeights = map[string]float64{}
		} else {
			for name, weight := range ov.ProfileWeights {
				profileWeights[name] = weight
			}
		}
	}

	toolWeights := defaultLoadTestToolWeights()
	if ov.ToolWeights != nil {
		if len(ov.ToolWeights) == 0 {
			toolWeights = map[string]float64{}
		} else {
			for name, weight := range ov.ToolWeights {
				toolWeights[name] = weight
			}
		}
	}

	reasoningSkipProbability := 0.10
	if ov.ReasoningSkipProbability != nil {
		reasoningSkipProbability = *ov.ReasoningSkipProbability
	}
	toolUseProbability := 0.65
	if ov.ToolUseProbability != nil {
		toolUseProbability = *ov.ToolUseProbability
	}
	maxToolRounds := 5
	if ov.MaxToolRounds != nil {
		maxToolRounds = *ov.MaxToolRounds
	}
	finalResponseTemplates := []string{
		"Load test summary for request %d.",
		"Assistant reply %d (mock).",
		"Completed mock response #%d.",
	}
	if ov.FinalResponseTemplates != nil {
		finalResponseTemplates = ov.FinalResponseTemplates
	}

	return validateLoadTestMockProfile(latencyProfiles, profileWeights, toolWeights, reasoningSkipProbability, toolUseProbability, maxToolRounds, finalResponseTemplates)
}

func defaultLoadTestLatencyProfiles() map[string]loadTestLatencyProfile {
	return map[string]loadTestLatencyProfile{
		"realistic_default": {
			TTFTMs:                    [2]int{3000, 12000},
			ChunkCount:                [2]int{150, 400},
			ChunkIntervalMs:           [2]int{30, 80},
			TotalWallTimeMsPerRequest: [2]int{15000, 25000},
		},
		"realistic_fast": {
			TTFTMs:                    [2]int{600, 2500},
			ChunkCount:                [2]int{40, 120},
			ChunkIntervalMs:           [2]int{40, 100},
			TotalWallTimeMsPerRequest: [2]int{5000, 10000},
		},
		"realistic_slow": {
			TTFTMs:                    [2]int{12000, 22000},
			ChunkCount:                [2]int{400, 1000},
			ChunkIntervalMs:           [2]int{15, 40},
			TotalWallTimeMsPerRequest: [2]int{28000, 40000},
		},
	}
}

func defaultLoadTestProfileWeights() map[string]float64 {
	return map[string]float64{
		"realistic_default": 0.70,
		"realistic_fast":    0.20,
		"realistic_slow":    0.10,
	}
}

func defaultLoadTestToolWeights() map[string]float64 {
	return map[string]float64{
		"read_channel":        0.20,
		"search_posts":        0.20,
		"search_users":        0.10,
		"get_channel_info":    0.12,
		"WebSearch":           0.12,
		"read_post":           0.08,
		"get_channel_members": 0.05,
		"get_user_channels":   0.05,
		"create_post":         0.02,
		"dm":                  0.03,
		"group_message":       0.03,
	}
}

func (o loadTestLatencyProfileOverlay) isComplete() bool {
	return o.TTFTMs != nil &&
		o.ChunkCount != nil &&
		o.ChunkIntervalMs != nil &&
		o.TotalWallTimeMsPerRequest != nil
}

func (o loadTestLatencyProfileOverlay) applyTo(base loadTestLatencyProfile) loadTestLatencyProfile {
	if o.TTFTMs != nil {
		base.TTFTMs = *o.TTFTMs
	}
	if o.ChunkCount != nil {
		base.ChunkCount = *o.ChunkCount
	}
	if o.ChunkIntervalMs != nil {
		base.ChunkIntervalMs = *o.ChunkIntervalMs
	}
	if o.TotalWallTimeMsPerRequest != nil {
		base.TotalWallTimeMsPerRequest = *o.TotalWallTimeMsPerRequest
	}
	return base
}

func validateLoadTestMockProfile(latencyProfiles map[string]loadTestLatencyProfile, profileWeights map[string]float64, toolWeights map[string]float64, reasoningSkipProbability float64, toolUseProbability float64, maxToolRounds int, finalResponseTemplates []string) bool {
	if len(latencyProfiles) == 0 {
		return false
	}
	for _, lp := range latencyProfiles {
		if !isValidLoadTestLatencyRange(lp.TTFTMs) ||
			!isValidLoadTestLatencyRange(lp.ChunkCount) ||
			!isValidLoadTestLatencyRange(lp.ChunkIntervalMs) ||
			!isValidLoadTestLatencyRange(lp.TotalWallTimeMsPerRequest) {
			return false
		}
	}
	if !isValidLoadTestWeightMap(profileWeights, true) {
		return false
	}
	for name := range profileWeights {
		if _, ok := latencyProfiles[name]; !ok {
			return false
		}
	}
	if !isValidLoadTestWeightMap(toolWeights, true) {
		return false
	}
	if !isFiniteLoadTestProbability(reasoningSkipProbability) || !isFiniteLoadTestProbability(toolUseProbability) {
		return false
	}
	if maxToolRounds < 0 || maxToolRounds > limits.MaxToolRounds {
		return false
	}
	if len(finalResponseTemplates) == 0 {
		return false
	}
	for _, template := range finalResponseTemplates {
		if strings.TrimSpace(template) == "" {
			return false
		}
	}
	return true
}

func isValidLoadTestWeightMap(m map[string]float64, requirePositiveSum bool) bool {
	if len(m) == 0 {
		return false
	}
	sum := 0.0
	for k, w := range m {
		if k == "" || math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			return false
		}
		sum += w
	}
	return !requirePositiveSum || sum > 0
}

func isFiniteLoadTestProbability(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1
}

func isValidLoadTestLatencyRange(bounds [2]int) bool {
	return bounds[0] >= 0 && bounds[1] >= 0 && bounds[0] <= bounds[1]
}
