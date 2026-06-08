// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/toolrunner/limits"
)

// LatencyProfile describes one named latency mix for mock streaming.
type LatencyProfile struct {
	TTFTMs                    [2]int `json:"ttft_ms"`
	ChunkCount                [2]int `json:"chunk_count"`
	ChunkIntervalMs           [2]int `json:"chunk_interval_ms"`
	TotalWallTimeMsPerRequest [2]int `json:"total_wall_time_ms_per_request"`
}

// ToolArgumentProfile holds optional discrete values for argument generation per tool.
type ToolArgumentProfile struct {
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

// MockProfile configures load-test LLM behavior.
type MockProfile struct {
	Name                     string                         `json:"name"`
	Seed                     int64                          `json:"seed"`
	LatencyProfiles          map[string]LatencyProfile      `json:"latency_profiles"`
	ProfileWeights           map[string]float64             `json:"profile_weights"`
	ReasoningSkipProbability float64                        `json:"reasoning_skip_probability"`
	StreamingEnabled         bool                           `json:"streaming_enabled"`
	ToolUseProbability       float64                        `json:"tool_use_probability"`
	ToolWeights              map[string]float64             `json:"tool_weights"`
	MaxToolRounds            int                            `json:"max_tool_rounds"`
	ToolArgumentProfiles     map[string]ToolArgumentProfile `json:"tool_argument_profiles,omitempty"`
	FinalResponseTemplates   []string                       `json:"final_response_templates"`
}

// DefaultReadSearchHeavyProfile returns the documented empirical defaults for read/search-heavy load tests.
func DefaultReadSearchHeavyProfile() MockProfile {
	return MockProfile{
		Name: "read_search_heavy_default",
		Seed: 1,
		LatencyProfiles: map[string]LatencyProfile{
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
		},
		ProfileWeights: map[string]float64{
			"realistic_default": 0.70,
			"realistic_fast":    0.20,
			"realistic_slow":    0.10,
		},
		ReasoningSkipProbability: 0.10,
		StreamingEnabled:         true,
		ToolUseProbability:       0.65,
		ToolWeights: map[string]float64{
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
		},
		MaxToolRounds: 5,
		ToolArgumentProfiles: map[string]ToolArgumentProfile{
			"read_channel": {
				PostLimits: []int{10, 25, 50, 100},
			},
			"search_posts": {
				SearchQueries: []string{
					"status update",
					"release notes",
					"bug triage",
					"design review",
					"SRE on-call",
				},
				SearchLimits: []int{10, 25, 50},
			},
			"search_users": {
				SearchLimits: []int{5, 10, 20},
			},
			"create_post": {
				MessageLengths: []int{12, 200, 3500},
			},
			"dm": {
				MessageLengths: []int{10, 120, 3000},
				Usernames:      []string{"alice", "bob"},
			},
			"group_message": {
				MessageLengths: []int{20, 180, 3200},
				Usernames:      []string{"alice", "bob", "carol", "dave"},
			},
			"read_post": {
				PostIDs: []string{
					"h5wqm8kxptbztfgzpaxbsqozah",
					"8xqzn3pfmtbyfkr9hqbw4hheoa",
				},
			},
			"get_channel_info": {
				ChannelIDs:   []string{"h5wqm8kxptbztfgzpaxbsqozah"},
				ChannelNames: []string{"town-square", "off-topic"},
			},
		},
		FinalResponseTemplates: []string{
			"Load test summary for request %d.",
			"Assistant reply %d (mock).",
			"Completed mock response #%d.",
		},
	}
}

type profileOverlay struct {
	Name                     *string                          `json:"name,omitempty"`
	Seed                     *int64                           `json:"seed,omitempty"`
	LatencyProfiles          map[string]latencyProfileOverlay `json:"latency_profiles,omitempty"`
	ProfileWeights           map[string]float64               `json:"profile_weights,omitempty"`
	ReasoningSkipProbability *float64                         `json:"reasoning_skip_probability,omitempty"`
	StreamingEnabled         *bool                            `json:"streaming_enabled,omitempty"`
	ToolUseProbability       *float64                         `json:"tool_use_probability,omitempty"`
	ToolWeights              map[string]float64               `json:"tool_weights,omitempty"`
	MaxToolRounds            *int                             `json:"max_tool_rounds,omitempty"`
	ToolArgumentProfiles     map[string]ToolArgumentProfile   `json:"tool_argument_profiles,omitempty"`
	FinalResponseTemplates   []string                         `json:"final_response_templates,omitempty"`
}

type latencyProfileOverlay struct {
	TTFTMs                    *[2]int `json:"ttft_ms,omitempty"`
	ChunkCount                *[2]int `json:"chunk_count,omitempty"`
	ChunkIntervalMs           *[2]int `json:"chunk_interval_ms,omitempty"`
	TotalWallTimeMsPerRequest *[2]int `json:"total_wall_time_ms_per_request,omitempty"`
}

func (o latencyProfileOverlay) isComplete() bool {
	return o.TTFTMs != nil &&
		o.ChunkCount != nil &&
		o.ChunkIntervalMs != nil &&
		o.TotalWallTimeMsPerRequest != nil
}

func (o latencyProfileOverlay) applyTo(base LatencyProfile) LatencyProfile {
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

// ParseProfile merges operator JSON on top of the default profile. Nil, empty, or whitespace-only raw returns the default.
func ParseProfile(raw json.RawMessage) (MockProfile, error) {
	if raw == nil || len(bytes.TrimSpace(raw)) == 0 {
		return DefaultReadSearchHeavyProfile(), nil
	}

	base := DefaultReadSearchHeavyProfile()

	var ov profileOverlay
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ov); err != nil {
		return MockProfile{}, fmt.Errorf("loadtest profile: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return MockProfile{}, fmt.Errorf("loadtest profile: unexpected trailing JSON value")
		}
		return MockProfile{}, fmt.Errorf("loadtest profile: %w", err)
	}

	if ov.Name != nil {
		base.Name = *ov.Name
	}
	if ov.Seed != nil {
		base.Seed = *ov.Seed
	}
	if ov.LatencyProfiles != nil {
		if len(ov.LatencyProfiles) == 0 {
			base.LatencyProfiles = map[string]LatencyProfile{}
		} else {
			for k, v := range ov.LatencyProfiles {
				existing, ok := base.LatencyProfiles[k]
				if !ok && !v.isComplete() {
					return MockProfile{}, fmt.Errorf("latency_profiles[%s] must define all latency fields for new profiles", k)
				}
				base.LatencyProfiles[k] = v.applyTo(existing)
			}
		}
	}
	if ov.ProfileWeights != nil {
		if len(ov.ProfileWeights) == 0 {
			base.ProfileWeights = map[string]float64{}
		} else {
			for k, v := range ov.ProfileWeights {
				base.ProfileWeights[k] = v
			}
		}
	}
	if ov.ReasoningSkipProbability != nil {
		base.ReasoningSkipProbability = *ov.ReasoningSkipProbability
	}
	if ov.StreamingEnabled != nil {
		base.StreamingEnabled = *ov.StreamingEnabled
	}
	if ov.ToolUseProbability != nil {
		base.ToolUseProbability = *ov.ToolUseProbability
	}
	if ov.ToolWeights != nil {
		if len(ov.ToolWeights) == 0 {
			base.ToolWeights = map[string]float64{}
		} else {
			for k, v := range ov.ToolWeights {
				base.ToolWeights[k] = v
			}
		}
	}
	if ov.MaxToolRounds != nil {
		base.MaxToolRounds = *ov.MaxToolRounds
	}
	if ov.ToolArgumentProfiles != nil {
		if base.ToolArgumentProfiles == nil {
			base.ToolArgumentProfiles = map[string]ToolArgumentProfile{}
		}
		for k, v := range ov.ToolArgumentProfiles {
			base.ToolArgumentProfiles[k] = v
		}
	}
	if ov.FinalResponseTemplates != nil {
		base.FinalResponseTemplates = ov.FinalResponseTemplates
	}

	if err := base.Validate(); err != nil {
		return MockProfile{}, err
	}
	return base, nil
}

func isFiniteProbability(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1
}

func validateWeightMap(m map[string]float64, name string, requirePositiveSum bool) error {
	if len(m) == 0 {
		return fmt.Errorf("%s must be non-empty", name)
	}
	sum := 0.0
	for k, w := range m {
		if k == "" {
			return fmt.Errorf("%s contains an empty key", name)
		}
		if math.IsNaN(w) || math.IsInf(w, 0) {
			return fmt.Errorf("%s entry %q is not finite", name, k)
		}
		if w < 0 {
			return fmt.Errorf("%s entry %q is negative", name, k)
		}
		sum += w
	}
	if requirePositiveSum && sum <= 0 {
		return fmt.Errorf("%s must sum to a positive value (got %v)", name, sum)
	}
	return nil
}

// Validate checks profile invariants.
func (p MockProfile) Validate() error {
	if len(p.LatencyProfiles) == 0 {
		return fmt.Errorf("latency_profiles must be non-empty")
	}
	for name, lp := range p.LatencyProfiles {
		if err := validateLatencyRange("latency_profiles["+name+"].ttft_ms", lp.TTFTMs); err != nil {
			return err
		}
		if err := validateLatencyRange("latency_profiles["+name+"].chunk_count", lp.ChunkCount); err != nil {
			return err
		}
		if err := validateLatencyRange("latency_profiles["+name+"].chunk_interval_ms", lp.ChunkIntervalMs); err != nil {
			return err
		}
		if err := validateLatencyRange("latency_profiles["+name+"].total_wall_time_ms_per_request", lp.TotalWallTimeMsPerRequest); err != nil {
			return err
		}
	}
	if err := validateWeightMap(p.ProfileWeights, "profile_weights", true); err != nil {
		return err
	}
	for name := range p.ProfileWeights {
		if _, ok := p.LatencyProfiles[name]; !ok {
			return fmt.Errorf("profile_weights references unknown latency profile %q", name)
		}
	}
	if err := validateWeightMap(p.ToolWeights, "tool_weights", true); err != nil {
		return err
	}
	if !isFiniteProbability(p.ReasoningSkipProbability) {
		return fmt.Errorf("reasoning_skip_probability must be finite and in [0,1], got %v", p.ReasoningSkipProbability)
	}
	if math.IsNaN(p.ToolUseProbability) || math.IsInf(p.ToolUseProbability, 0) || p.ToolUseProbability < 0 || p.ToolUseProbability > 1 {
		return fmt.Errorf("tool_use_probability must be finite and in [0,1], got %v", p.ToolUseProbability)
	}
	if p.MaxToolRounds < 0 {
		return fmt.Errorf("max_tool_rounds must be non-negative, got %d", p.MaxToolRounds)
	}
	if p.MaxToolRounds > limits.MaxToolRounds {
		return fmt.Errorf("max_tool_rounds must be <= %d (toolrunner limit), got %d", limits.MaxToolRounds, p.MaxToolRounds)
	}
	if len(p.FinalResponseTemplates) == 0 {
		return fmt.Errorf("final_response_templates must be non-empty")
	}
	for i, t := range p.FinalResponseTemplates {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("final_response_templates[%d] is empty", i)
		}
	}
	return nil
}

func validateLatencyRange(field string, b [2]int) error {
	if b[0] < 0 || b[1] < 0 {
		return fmt.Errorf("%s ranges must be non-negative", field)
	}
	if b[0] > b[1] {
		return fmt.Errorf("%s must satisfy min<=max, got [%d,%d]", field, b[0], b[1])
	}
	return nil
}

const summaryDefaultsSource = "spikes/llm-latency-benchmark"

func formatIntList(xs []int) string {
	if len(xs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", v)
	}
	return b.String()
}

func formatStringList(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(v)
	}
	return b.String()
}

func appendToolArgumentProfileLines(b *strings.Builder, tool string, tap ToolArgumentProfile) {
	fmt.Fprintf(b, "  %s:\n", tool)
	wrote := false
	write := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(b, "    %s=%s\n", label, value)
		wrote = true
	}
	write("post_limits", formatIntList(tap.PostLimits))
	write("search_queries", formatStringList(tap.SearchQueries))
	write("search_limits", formatIntList(tap.SearchLimits))
	write("message_lengths", formatIntList(tap.MessageLengths))
	write("usernames", formatStringList(tap.Usernames))
	write("channel_ids", formatStringList(tap.ChannelIDs))
	write("channel_names", formatStringList(tap.ChannelNames))
	write("team_ids", formatStringList(tap.TeamIDs))
	write("team_names", formatStringList(tap.TeamNames))
	write("post_ids", formatStringList(tap.PostIDs))
	if !wrote {
		fmt.Fprintf(b, "    (no argument distributions)\n")
	}
}

// Summary returns a compact operator-facing description for logging.
func (p MockProfile) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name=%s seed=%d streaming=%v reasoning_skip_p=%.4f tool_use_p=%.4f max_tool_rounds=%d defaults_source=%s\n",
		p.Name, p.Seed, p.StreamingEnabled, p.ReasoningSkipProbability, p.ToolUseProbability, p.MaxToolRounds, summaryDefaultsSource)

	names := make([]string, 0, len(p.LatencyProfiles))
	for n := range p.LatencyProfiles {
		names = append(names, n)
	}
	slices.Sort(names)
	fmt.Fprintf(&b, "latency_profiles:\n")
	for _, n := range names {
		lp := p.LatencyProfiles[n]
		fmt.Fprintf(&b, "  %s: ttft_ms=[%d,%d] chunk_count=[%d,%d] chunk_interval_ms=[%d,%d] total_wall_time_ms_per_request=[%d,%d]\n",
			n, lp.TTFTMs[0], lp.TTFTMs[1], lp.ChunkCount[0], lp.ChunkCount[1],
			lp.ChunkIntervalMs[0], lp.ChunkIntervalMs[1], lp.TotalWallTimeMsPerRequest[0], lp.TotalWallTimeMsPerRequest[1])
	}

	pwNames := make([]string, 0, len(p.ProfileWeights))
	for n := range p.ProfileWeights {
		pwNames = append(pwNames, n)
	}
	slices.Sort(pwNames)
	fmt.Fprintf(&b, "profile_weights:")
	for _, n := range pwNames {
		fmt.Fprintf(&b, " %s=%.4f", n, p.ProfileWeights[n])
	}
	b.WriteByte('\n')

	twNames := make([]string, 0, len(p.ToolWeights))
	for n := range p.ToolWeights {
		twNames = append(twNames, n)
	}
	slices.Sort(twNames)
	fmt.Fprintf(&b, "tool_weights:")
	for _, n := range twNames {
		fmt.Fprintf(&b, " %s=%.4f", n, p.ToolWeights[n])
	}
	b.WriteByte('\n')

	fmt.Fprintf(&b, "tool_argument_profiles:\n")
	if len(p.ToolArgumentProfiles) == 0 {
		fmt.Fprintf(&b, "  (none configured)\n")
	} else {
		argKeys := make([]string, 0, len(p.ToolArgumentProfiles))
		for k := range p.ToolArgumentProfiles {
			argKeys = append(argKeys, k)
		}
		slices.Sort(argKeys)
		for _, k := range argKeys {
			appendToolArgumentProfileLines(&b, k, p.ToolArgumentProfiles[k])
		}
	}
	return b.String()
}

func cloneMockProfile(p MockProfile) MockProfile {
	p.LatencyProfiles = cloneLatencyProfiles(p.LatencyProfiles)
	p.ProfileWeights = cloneFloatMap(p.ProfileWeights)
	p.ToolWeights = cloneFloatMap(p.ToolWeights)
	p.ToolArgumentProfiles = cloneToolArgumentProfiles(p.ToolArgumentProfiles)
	p.FinalResponseTemplates = cloneStringSlice(p.FinalResponseTemplates)
	return p
}

func cloneLatencyProfiles(in map[string]LatencyProfile) map[string]LatencyProfile {
	if in == nil {
		return nil
	}
	out := make(map[string]LatencyProfile, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	if in == nil {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneToolArgumentProfiles(in map[string]ToolArgumentProfile) map[string]ToolArgumentProfile {
	if in == nil {
		return nil
	}
	out := make(map[string]ToolArgumentProfile, len(in))
	for k, v := range in {
		out[k] = ToolArgumentProfile{
			PostLimits:     cloneIntSlice(v.PostLimits),
			SearchQueries:  cloneStringSlice(v.SearchQueries),
			SearchLimits:   cloneIntSlice(v.SearchLimits),
			MessageLengths: cloneIntSlice(v.MessageLengths),
			Usernames:      cloneStringSlice(v.Usernames),
			ChannelIDs:     cloneStringSlice(v.ChannelIDs),
			ChannelNames:   cloneStringSlice(v.ChannelNames),
			TeamIDs:        cloneStringSlice(v.TeamIDs),
			TeamNames:      cloneStringSlice(v.TeamNames),
			PostIDs:        cloneStringSlice(v.PostIDs),
		}
	}
	return out
}

func cloneIntSlice(in []int) []int {
	return append([]int(nil), in...)
}

func cloneStringSlice(in []string) []string {
	return append([]string(nil), in...)
}
