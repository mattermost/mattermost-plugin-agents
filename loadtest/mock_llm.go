// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// toolCategory classifies tools for summaries and future tuning.
func toolCategory(name string) string {
	switch name {
	case "read_channel", "read_post", "get_channel_info", "get_channel_members", "get_user_channels":
		return "read"
	case "search_posts", "search_users", "WebSearch", "WebSearchFetchSource":
		return "search"
	case "create_post", "dm", "group_message":
		return "write"
	default:
		return "other"
	}
}

// MockLLM is an in-process llm.LanguageModel for load tests.
type MockLLM struct {
	profile MockProfile

	mu sync.Mutex
	rg *rand.Rand

	nextReq int64
}

// NewMockLLM constructs a mock LLM using the given profile (must be validated).
func NewMockLLM(profile MockProfile) *MockLLM {
	if err := profile.Validate(); err != nil {
		panic(fmt.Sprintf("invalid loadtest mock profile: %v", err))
	}
	profile = cloneMockProfile(profile)
	return &MockLLM{
		profile: profile,
		rg:      rand.New(rand.NewSource(profile.Seed)), // #nosec G404 -- deterministic load simulation uses seeded math/rand.
	}
}

type sampledRequest struct {
	LatencyName   string
	Latency       LatencyProfile
	TTFTMs        int
	ChunkCount    int
	ChunkInterval int
	WallTimeMs    int
	WantTools     bool
	EmitReasoning bool
	UseStreaming  bool
	SelectedTool  llm.Tool
	ToolArgs      []byte
	FinalText     string
	Seq           int64
}

func applyOptions(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := llm.LanguageModelConfig{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(&cfg)
	}
	return cfg
}

func countToolRounds(posts []llm.Post) int {
	var n int
	start := 0
	for i := len(posts) - 1; i >= 0; i-- {
		if posts[i].Role == llm.PostRoleUser {
			start = i + 1
			break
		}
	}
	for _, p := range posts[start:] {
		if p.Role != llm.PostRoleBot {
			continue
		}
		if len(p.ToolUse) > 0 {
			n++
		}
	}
	return n
}

func splitIntoChunks(text string, chunkCount int) []string {
	if chunkCount <= 0 {
		chunkCount = 1
	}
	if len(text) == 0 {
		return []string{""}
	}
	if chunkCount == 1 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) < chunkCount {
		chunks := make([]string, len(runes))
		for i := range runes {
			chunks[i] = string(runes[i])
		}
		return chunks
	}
	base := len(runes) / chunkCount
	rem := len(runes) % chunkCount
	out := make([]string, 0, chunkCount)
	start := 0
	for i := 0; i < chunkCount; i++ {
		add := base
		if i < rem {
			add++
		}
		end := start + add
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
		start = end
	}
	return out
}

func sampleRange(bounds [2]int, rng *rand.Rand) int {
	if bounds[1] < bounds[0] {
		return bounds[0]
	}
	span := bounds[1] - bounds[0] + 1
	return bounds[0] + rng.Intn(span)
}

func sampleWeightedName(weights map[string]float64, rng *rand.Rand) string {
	names := make([]string, 0, len(weights))
	for n := range weights {
		names = append(names, n)
	}
	slices.Sort(names)

	var sum float64
	for _, n := range names {
		w := weights[n]
		if w > 0 {
			sum += w
		}
	}
	if sum <= 0 {
		return ""
	}
	r := rng.Float64() * sum
	for _, n := range names {
		w := weights[n]
		if w <= 0 {
			continue
		}
		r -= w
		if r <= 0 {
			return n
		}
	}
	if len(names) > 0 {
		return names[len(names)-1]
	}
	return ""
}

func (m *MockLLM) sampleRequest(req llm.CompletionRequest, cfg llm.LanguageModelConfig) sampledRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextReq++
	seq := m.nextReq
	rng := m.rg

	name := sampleWeightedName(m.profile.ProfileWeights, rng)
	lp := m.profile.LatencyProfiles[name]
	ttft := sampleRange(lp.TTFTMs, rng)
	chunkCount := sampleRange(lp.ChunkCount, rng)
	chunkIv := sampleRange(lp.ChunkIntervalMs, rng)
	wall := sampleRange(lp.TotalWallTimeMsPerRequest, rng)

	tools := availableTools(req)
	rounds := countToolRounds(req.Posts)
	maxR := m.profile.MaxToolRounds

	wantTools := len(tools) > 0 && !cfg.ToolsDisabled &&
		maxR > 0 && rounds < maxR &&
		rng.Float64() < m.profile.ToolUseProbability

	var tool llm.Tool
	var targs []byte
	if wantTools {
		chosen, args, ok := chooseWeightedBuildableTool(m.profile, tools, m.profile.ToolWeights, req.Context, rng)
		if !ok {
			wantTools = false
		} else {
			tool = chosen
			targs = append([]byte(nil), args...)
		}
	}

	templates := m.profile.FinalResponseTemplates
	tmpl := templates[rng.Intn(len(templates))]
	finalText := fmt.Sprintf(tmpl, seq)

	skipRe := cfg.ReasoningDisabled || rng.Float64() < m.profile.ReasoningSkipProbability
	emitRe := !skipRe

	return sampledRequest{
		LatencyName:   name,
		Latency:       lp,
		TTFTMs:        ttft,
		ChunkCount:    chunkCount,
		ChunkInterval: chunkIv,
		WallTimeMs:    wall,
		WantTools:     wantTools,
		EmitReasoning: emitRe,
		UseStreaming:  m.profile.StreamingEnabled,
		SelectedTool:  tool,
		ToolArgs:      targs,
		FinalText:     finalText,
		Seq:           seq,
	}
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// ChatCompletion streams a synthetic response.
func (m *MockLLM) ChatCompletion(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	ctx = contextOrBackground(ctx)
	cfg := applyOptions(opts)
	sr := m.sampleRequest(req, cfg)

	stream := make(chan llm.TextStreamEvent, 32)

	go func(sr sampledRequest) {
		defer close(stream)

		start := time.Now()
		sleep := func(d time.Duration) bool {
			return sleepContext(ctx, d)
		}
		send := func(ev llm.TextStreamEvent) bool {
			if ctx.Err() != nil {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case stream <- ev:
				return true
			}
		}
		elapsed := func() time.Duration { return time.Since(start) }

		if !sleep(time.Duration(sr.TTFTMs) * time.Millisecond) {
			return
		}

		if sr.EmitReasoning {
			toolName := sr.SelectedTool.Name
			if toolName == "" {
				toolName = "(none)"
			}
			rText := fmt.Sprintf("reasoning block for seq %d category=%s", sr.Seq, toolCategory(toolName))
			rc := maxInt(1, sr.ChunkCount/20)
			chunks := splitIntoChunks(rText, rc)
			var full strings.Builder
			for _, ch := range chunks {
				full.WriteString(ch)
				if !send(llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: ch}) {
					return
				}
			}
			if !send(llm.TextStreamEvent{
				Type: llm.EventTypeReasoningEnd,
				Value: llm.ReasoningData{
					Text:      full.String(),
					Signature: "loadtest-reasoning-sig",
				},
			}) {
				return
			}
		}

		if sr.WantTools {
			if !send(llm.TextStreamEvent{Type: llm.EventTypeText, Value: fmt.Sprintf("tool preamble %d", sr.Seq)}) {
				return
			}
			tc := llm.ToolCall{
				ID:           fmt.Sprintf("loadtest-tool-%d-0", sr.Seq),
				Name:         sr.SelectedTool.Name,
				Arguments:    json.RawMessage(append([]byte(nil), sr.ToolArgs...)),
				Status:       llm.ToolCallStatusPending,
				ServerOrigin: sr.SelectedTool.ServerOrigin,
			}
			if !send(llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: []llm.ToolCall{tc},
			}) {
				return
			}
			if !padWallTime(start, sr.WallTimeMs, elapsed, sleep) {
				return
			}
			if !send(llm.TextStreamEvent{
				Type: llm.EventTypeUsage,
				Value: llm.TokenUsage{
					InputTokens:  int64(len(sr.FinalText) / 4),
					OutputTokens: int64(len(sr.FinalText) / 4),
				},
			}) {
				return
			}
			send(llm.TextStreamEvent{Type: llm.EventTypeEnd, Value: nil})
			return
		}

		text := sr.FinalText
		nChunks := sr.ChunkCount
		if !sr.UseStreaming {
			nChunks = 1
		}
		chunks := splitIntoChunks(text, nChunks)
		remMs := sr.WallTimeMs - sr.TTFTMs
		if remMs < 0 {
			remMs = 0
		}
		var gap time.Duration
		if len(chunks) > 1 {
			per := remMs / (len(chunks) - 1)
			if per < sr.ChunkInterval {
				gap = time.Duration(per) * time.Millisecond
			} else {
				gap = time.Duration(sr.ChunkInterval) * time.Millisecond
			}
		}
		for i, ch := range chunks {
			if !send(llm.TextStreamEvent{Type: llm.EventTypeText, Value: ch}) {
				return
			}
			if i < len(chunks)-1 {
				if !sleep(gap) {
					return
				}
			}
		}
		if !padWallTime(start, sr.WallTimeMs, elapsed, sleep) {
			return
		}

		if !send(llm.TextStreamEvent{
			Type: llm.EventTypeUsage,
			Value: llm.TokenUsage{
				InputTokens:  int64(len(text) / 4),
				OutputTokens: int64(len(text) / 4),
			},
		}) {
			return
		}
		send(llm.TextStreamEvent{Type: llm.EventTypeEnd, Value: nil})
	}(sr)

	return &llm.TextStreamResult{Stream: stream}, nil
}

func padWallTime(start time.Time, wallMs int, elapsed func() time.Duration, sleep func(time.Duration) bool) bool {
	target := time.Duration(wallMs) * time.Millisecond
	if d := target - elapsed(); d > 0 {
		return sleep(d)
	}
	return true
}

// ChatCompletionNoStream returns the final mock text after honoring wall-clock pacing.
func (m *MockLLM) ChatCompletionNoStream(ctx context.Context, req llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	ctx = contextOrBackground(ctx)
	sr := m.sampleRequest(req, applyOptions(opts))
	if !sleepContext(ctx, time.Duration(sr.WallTimeMs)*time.Millisecond) {
		return "", ctx.Err()
	}
	return sr.FinalText, nil
}

// CountTokens approximates tokens as ~4 characters with a minimum of 1 when non-empty.
func (m *MockLLM) CountTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	n := (len(text) + 3) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// InputTokenLimit returns a generous limit for load testing.
func (m *MockLLM) InputTokenLimit() int {
	return 100000
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
