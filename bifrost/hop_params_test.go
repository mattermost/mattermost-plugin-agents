// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// captureServer records the last request body it receives. A succeeding
// server answers it, ending the fallback chain; a failing one rejects it so
// Bifrost moves on to the next hop. A 400 fails over without Bifrost's retry
// backoff for server errors.
func captureServer(t *testing.T, body *atomic.Value, succeed bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(data, &parsed)
		body.Store(parsed)
		switch {
		case !succeed:
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"rejected"}}`, http.StatusBadRequest)
		case strings.HasSuffix(r.URL.Path, "/responses"):
			responsesSSE(w, "ok")
		default:
			chatCompletionSSE(w, "ok")
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// captureHop is one service of a chain driven by captureFailover.
type captureHop struct {
	svc     llm.ServiceConfig
	succeed bool
}

// localHop is an OpenAI-compatible chat-only service that answers, ending a
// chain driven by captureFailover.
var localHop = captureHop{
	svc:     llm.ServiceConfig{Type: llm.ServiceTypeOpenAICompatible, DefaultModel: "llama3"},
	succeed: true,
}

// hopServiceID is the service ID captureFailover gives hop i.
func hopServiceID(i int) string {
	return fmt.Sprintf("hop%d", i)
}

// captureFailover sends request through the production wiring for a chain of
// hops, each served by a captureServer, and returns the client and the body
// each hop received, nil for a hop that was not tried. The service IDs, URLs,
// keys, and the bot's identity and service are filled in here.
func captureFailover(t *testing.T, hops []captureHop, bot llm.BotConfig, request llm.CompletionRequest) (*LLM, []map[string]any) {
	t.Helper()
	llmInstance, captures := newCaptureChain(t, hops, bot)

	_, err := llmInstance.ChatCompletionNoStream(context.Background(), request, llm.WithToolsDisabled())
	require.NoError(t, err)

	return llmInstance, loadCaptures(captures)
}

// newCaptureChain builds the production client for a chain of hops, each
// served by a captureServer, and returns it with the capture of each hop.
func newCaptureChain(t *testing.T, hops []captureHop, bot llm.BotConfig) (*LLM, []atomic.Value) {
	t.Helper()
	captures := make([]atomic.Value, len(hops))
	services := make([]llm.ServiceConfig, len(hops))
	for i, hop := range hops {
		services[i] = hop.svc
		services[i].ID = hopServiceID(i)
		services[i].APIKey = fmt.Sprintf("key-%d", i)
		services[i].APIURL = captureServer(t, &captures[i], hop.succeed).URL
	}
	bot.ID, bot.Name, bot.DisplayName, bot.ServiceID = "bot-1", "ai", "AI", services[0].ID

	llmInstance, err := NewFromServiceConfig(services[0], bot, services[1:])
	require.NoError(t, err)
	t.Cleanup(llmInstance.Shutdown)
	return llmInstance, captures
}

// loadCaptures returns the body each hop received, nil for a hop that was not
// tried.
func loadCaptures(captures []atomic.Value) []map[string]any {
	bodies := make([]map[string]any, len(captures))
	for i := range captures {
		bodies[i], _ = captures[i].Load().(map[string]any)
	}
	return bodies
}

// captureTwoHopFailover drives a primary -> fallback -> localHop chain whose
// first two hops reject the request, and returns the bodies the primary and
// fallback received.
func captureTwoHopFailover(t *testing.T, primarySvc, fallbackSvc llm.ServiceConfig, bot llm.BotConfig) (primaryBody, fallbackBody map[string]any) {
	t.Helper()
	_, bodies := captureFailover(t, []captureHop{{svc: primarySvc}, {svc: fallbackSvc}, localHop}, bot, llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})
	require.NotNil(t, bodies[0], "primary hop must have been called")
	require.NotNil(t, bodies[1], "fallback hop must have been called")
	require.NotNil(t, bodies[2], "the request must fail over through both hops")
	return bodies[0], bodies[1]
}

// anthropicThinkingOnWire returns the thinking object and output_config.effort
// of a captured Anthropic Messages request.
func anthropicThinkingOnWire(body map[string]any) (thinking map[string]any, effort string) {
	thinking, _ = body["thinking"].(map[string]any)
	if outputConfig, ok := body["output_config"].(map[string]any); ok {
		effort, _ = outputConfig["effort"].(string)
	}
	return thinking, effort
}

// TestReasoningIsResolvedPerFallbackHop drives real failovers through Bifrost
// and checks the reasoning each hop receives. Bifrost reuses the primary's
// request parameters for fallbacks, so without per-hop resolution a setting
// valid for the primary model is sent unchanged to a fallback model that
// rejects it or interprets it differently.
func TestReasoningIsResolvedPerFallbackHop(t *testing.T) {
	type wantAnthropic struct {
		thinkingType string // "" means no thinking parameter
		budget       float64
		effort       string // "" means no effort
		display      string
	}
	tests := []struct {
		name             string
		primaryModel     string
		fallbackType     string
		fallbackModel    string
		reasoningEnabled bool
		effort           string
		nativeWebSearch  bool
		wantPrimary      wantAnthropic
		wantFallback     wantAnthropic
		wantGeminiThink  bool
	}{
		{
			name:         "Sonnet 5 off disables thinking but Opus 5.5 fallback gets low effort",
			primaryModel: "claude-sonnet-5", fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-opus-5-5",
			wantPrimary:  wantAnthropic{thinkingType: "disabled"},
			wantFallback: wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
		},
		{
			name:         "Opus 5.5 off uses low effort but Haiku fallback thinks only when asked",
			primaryModel: "claude-opus-5-5", fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-haiku-4-5-20251001",
			wantPrimary:  wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
			wantFallback: wantAnthropic{},
		},
		{
			name:         "xhigh is kept for Opus 4.7 and stepped down for an Opus 4.6 fallback",
			primaryModel: "claude-opus-4-7", fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-opus-4-6",
			reasoningEnabled: true, effort: "xhigh",
			wantPrimary:  wantAnthropic{thinkingType: "adaptive", effort: "xhigh", display: "summarized"},
			wantFallback: wantAnthropic{thinkingType: "adaptive", effort: "high"},
		},
		{
			name:         "max on a budget-only fallback uses the capped budget table",
			primaryModel: "claude-opus-4-7", fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-haiku-4-5-20251001",
			reasoningEnabled: true, effort: "max",
			wantPrimary:  wantAnthropic{thinkingType: "adaptive", effort: "max", display: "summarized"},
			wantFallback: wantAnthropic{thinkingType: "enabled", budget: 4096},
		},
		{
			name:         "Responses path resolves the fallback hop too",
			primaryModel: "claude-sonnet-5", fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-opus-5-5",
			nativeWebSearch: true,
			wantPrimary:     wantAnthropic{thinkingType: "disabled"},
			wantFallback:    wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
		},
		{
			name:         "Claude on an Azure fallback gets the effort an Anthropic hop gets",
			primaryModel: "claude-opus-4-7", fallbackType: llm.ServiceTypeAzure, fallbackModel: "claude-opus-4-6",
			reasoningEnabled: true, effort: "high",
			wantPrimary:  wantAnthropic{thinkingType: "adaptive", effort: "high"},
			wantFallback: wantAnthropic{thinkingType: "adaptive", effort: "high"},
		},
		{
			name:         "Sonnet 5 off on an Azure fallback disables thinking",
			primaryModel: "claude-opus-5-5", fallbackType: llm.ServiceTypeAzure, fallbackModel: "claude-sonnet-5",
			wantPrimary:  wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
			wantFallback: wantAnthropic{thinkingType: "disabled"},
		},
		{
			name:         "Responses path disables thinking for Sonnet 5 on an Azure fallback",
			primaryModel: "claude-opus-5-5", fallbackType: llm.ServiceTypeAzure, fallbackModel: "claude-sonnet-5",
			nativeWebSearch: true,
			wantPrimary:     wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
			wantFallback:    wantAnthropic{thinkingType: "disabled"},
		},
		{
			name:         "Opus 5.5 off does not ask a Gemini fallback for thoughts",
			primaryModel: "claude-opus-5-5", fallbackType: llm.ServiceTypeGemini, fallbackModel: "gemini-2.5-flash",
			wantPrimary:     wantAnthropic{thinkingType: "adaptive", effort: "low", display: "omitted"},
			wantGeminiThink: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := llm.BotConfig{ReasoningEnabled: tt.reasoningEnabled, ReasoningEffort: tt.effort}
			if tt.nativeWebSearch {
				bot.EnabledNativeTools = []string{"web_search"}
			}
			primaryBody, fallbackBody := captureTwoHopFailover(t,
				llm.ServiceConfig{Type: llm.ServiceTypeAnthropic, DefaultModel: tt.primaryModel},
				llm.ServiceConfig{Type: tt.fallbackType, DefaultModel: tt.fallbackModel},
				bot,
			)

			assertHop := func(label string, body map[string]any, want wantAnthropic) {
				thinking, effort := anthropicThinkingOnWire(body)
				assert.Equal(t, want.effort, effort, "%s effort", label)
				if want.thinkingType == "" {
					assert.Nil(t, thinking, "%s must not send thinking", label)
					return
				}
				require.NotNil(t, thinking, "%s thinking", label)
				assert.Equal(t, want.thinkingType, thinking["type"], "%s thinking type", label)
				if want.budget > 0 {
					assert.InDelta(t, want.budget, thinking["budget_tokens"], 0, "%s budget", label)
				}
				if want.display != "" {
					assert.Equal(t, want.display, thinking["display"], "%s display", label)
				}
			}

			assertHop("primary", primaryBody, tt.wantPrimary)
			if tt.fallbackType == llm.ServiceTypeGemini {
				generationConfig, _ := fallbackBody["generationConfig"].(map[string]any)
				thinkingConfig, _ := generationConfig["thinkingConfig"].(map[string]any)
				includeThoughts, _ := thinkingConfig["includeThoughts"].(bool)
				assert.Equal(t, tt.wantGeminiThink, includeThoughts, "Gemini includeThoughts")
				return
			}
			assertHop("fallback", fallbackBody, tt.wantFallback)
		})
	}
}

// maxTokensOnWire returns the max output tokens field of a captured request:
// max_tokens (Anthropic), max_completion_tokens (OpenAI chat) or
// max_output_tokens (OpenAI Responses).
func maxTokensOnWire(body map[string]any) (float64, bool) {
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if v, ok := body[key].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

// TestMaxOutputTokensAreClampedPerFallbackHop drives real failovers through
// Bifrost and checks the max output tokens each hop receives. Bifrost reuses
// the primary's max tokens for fallbacks and never clamps them to the model,
// so a value valid for the primary is rejected by a fallback model with a
// lower output limit. Bifrost v1.7.15 knows output limits only for Claude
// models; other models keep the requested value.
func TestMaxOutputTokensAreClampedPerFallbackHop(t *testing.T) {
	tests := []struct {
		name             string
		primaryType      string
		primaryModel     string
		outputTokenLimit int
		fallbackType     string
		fallbackModel    string
		// fallbackOutputTokenLimit is the fallback service's configured limit.
		fallbackOutputTokenLimit int
		reasoningEnabled         bool
		effort                   string
		nativeWebSearch          bool
		wantPrimary              float64
		wantFallback             float64
		// wantFallbackBudget is the fallback's thinking budget; 0 means unchecked.
		wantFallbackBudget float64
	}{
		{
			name:        "chat path clamps a fallback with a lower limit than the primary",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 128000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-3-5-haiku-20241022",
			wantPrimary: 128000, wantFallback: 8192,
		},
		{
			name:        "Responses path clamps a fallback with a lower limit than the primary",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 128000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-3-5-haiku-20241022",
			nativeWebSearch: true,
			wantPrimary:     128000, wantFallback: 8192,
		},
		{
			name:        "OpenAI Responses primary clamps an Anthropic fallback",
			primaryType: llm.ServiceTypeOpenAI, primaryModel: "gpt-4o", outputTokenLimit: 64000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-3-7-sonnet-20250219",
			wantPrimary: 64000, wantFallback: 8192,
		},
		{
			name:        "fallback with a higher limit is not capped to the primary's",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-3-5-haiku-20241022", outputTokenLimit: 64000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-sonnet-4-6",
			wantPrimary: 8192, wantFallback: 64000,
		},
		{
			name:        "thinking budget fits under the clamped fallback max_tokens",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 128000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-3-7-sonnet-20250219",
			reasoningEnabled: true, effort: "max",
			wantPrimary: 128000, wantFallback: 8192, wantFallbackBudget: 4096,
		},
		{
			name:        "fallback model without a known limit keeps the requested value",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-3-5-haiku-20241022", outputTokenLimit: 64000,
			fallbackType: llm.ServiceTypeOpenAI, fallbackModel: "gpt-4o",
			wantPrimary: 8192, wantFallback: 64000,
		},
		{
			name:        "fallback service's own limit clamps a model without a known limit",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 64000,
			fallbackType: llm.ServiceTypeOpenAI, fallbackModel: "gpt-4o", fallbackOutputTokenLimit: 2048,
			wantPrimary: 64000, wantFallback: 2048,
		},
		{
			name:        "fallback service's own limit clamps below its model's limit",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 128000,
			fallbackType: llm.ServiceTypeAnthropic, fallbackModel: "claude-sonnet-4-6", fallbackOutputTokenLimit: 4096,
			nativeWebSearch: true,
			wantPrimary:     128000, wantFallback: 4096,
		},
		{
			name:        "fallback service's own limit does not raise the requested value",
			primaryType: llm.ServiceTypeAnthropic, primaryModel: "claude-opus-4-6", outputTokenLimit: 16000,
			fallbackType: llm.ServiceTypeOpenAI, fallbackModel: "gpt-4o", fallbackOutputTokenLimit: 32000,
			wantPrimary: 16000, wantFallback: 16000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := llm.BotConfig{ReasoningEnabled: tt.reasoningEnabled, ReasoningEffort: tt.effort}
			if tt.nativeWebSearch {
				bot.EnabledNativeTools = []string{"web_search"}
			}
			primaryBody, fallbackBody := captureTwoHopFailover(t,
				llm.ServiceConfig{Type: tt.primaryType, DefaultModel: tt.primaryModel, OutputTokenLimit: tt.outputTokenLimit},
				llm.ServiceConfig{Type: tt.fallbackType, DefaultModel: tt.fallbackModel, OutputTokenLimit: tt.fallbackOutputTokenLimit},
				bot,
			)

			primaryMax, ok := maxTokensOnWire(primaryBody)
			require.True(t, ok, "primary must send max output tokens")
			assert.InDelta(t, tt.wantPrimary, primaryMax, 0, "primary max output tokens")
			fallbackMax, ok := maxTokensOnWire(fallbackBody)
			require.True(t, ok, "fallback must send max output tokens")
			assert.InDelta(t, tt.wantFallback, fallbackMax, 0, "fallback max output tokens")

			if tt.wantFallbackBudget > 0 {
				thinking, _ := anthropicThinkingOnWire(fallbackBody)
				require.NotNil(t, thinking, "fallback thinking")
				budget, _ := thinking["budget_tokens"].(float64)
				assert.InDelta(t, tt.wantFallbackBudget, budget, 0, "fallback thinking budget")
				assert.Less(t, budget, fallbackMax, "Anthropic requires budget_tokens below max_tokens")
			}
		})
	}
}

// TestChatOnlyFallbackGetsChatPathReasoning drives real failovers through
// Bifrost from a Responses-path primary to an OpenAI-compatible fallback
// registered chat-only. Bifrost sends that hop a chat completion and keeps any
// Responses reasoning effort as reasoning_effort, which the same service as a
// primary never sends.
func TestChatOnlyFallbackGetsChatPathReasoning(t *testing.T) {
	tests := []struct {
		name            string
		primary         llm.ServiceConfig
		nativeWebSearch bool
	}{
		{
			name:    "OpenAI Responses primary",
			primary: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-5"},
		},
		{
			name:            "Anthropic Responses primary",
			primary:         llm.ServiceConfig{Type: llm.ServiceTypeAnthropic, DefaultModel: "claude-opus-4-7"},
			nativeWebSearch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := llm.BotConfig{ReasoningEnabled: true, ReasoningEffort: "high"}
			if tt.nativeWebSearch {
				bot.EnabledNativeTools = []string{llm.NativeToolWebSearch}
			}
			chatOnly := captureHop{svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAICompatible, DefaultModel: "gpt-5"}}
			llmInstance, bodies := captureFailover(t, []captureHop{{svc: tt.primary}, chatOnly, localHop}, bot, llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			})
			require.True(t, llmInstance.shouldUseResponsesAPI(llmInstance.GetDefaultConfig()), "row must exercise the Responses path")
			fallbackBody := bodies[1]
			require.NotNil(t, fallbackBody, "the chat-only fallback must have been called")

			assert.Contains(t, fallbackBody, "messages", "the chat-only fallback must be sent a chat completion")
			assert.NotContains(t, fallbackBody, "reasoning_effort")
			assert.NotContains(t, fallbackBody, "reasoning")
		})
	}
}

// TestHopReasoningFollowsTheModelsMessagesAPI checks that a Claude model on
// Vertex, which Bifrost sends to Anthropic's Messages API, gets the reasoning
// an Anthropic hop with the same model gets, on both API paths, and that
// non-Claude models on Vertex and Azure keep their own provider's. Vertex
// cannot be pointed at a test server, so it is checked here rather than
// through a captured failover; Claude on Azure is covered by
// TestReasoningIsResolvedPerFallbackHop.
func TestHopReasoningFollowsTheModelsMessagesAPI(t *testing.T) {
	tests := []struct {
		name             string
		provider         schemas.ModelProvider
		model            string
		reasoningEnabled bool
		effort           string
		wantAnthropic    bool
	}{
		{name: "Sonnet 5 off on Vertex", provider: schemas.Vertex, model: "claude-sonnet-5@20260101", wantAnthropic: true},
		{name: "Opus 5.5 off on Vertex", provider: schemas.Vertex, model: "claude-opus-5-5", wantAnthropic: true},
		{name: "Opus 4.6 high on Vertex", provider: schemas.Vertex, model: "claude-opus-4-6", reasoningEnabled: true, effort: "high", wantAnthropic: true},
		{name: "Gemini on Vertex keeps Gemini reasoning", provider: schemas.Vertex, model: "gemini-2.5-pro", reasoningEnabled: true, effort: "high"},
		{name: "GPT on Azure keeps OpenAI reasoning", provider: schemas.Azure, model: "gpt-5", reasoningEnabled: true, effort: "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{
				provider:         schemas.Anthropic,
				reasoningEnabled: tt.reasoningEnabled,
				reasoningEffort:  tt.effort,
				account: testAccount(
					&providerAccount{Provider: schemas.Anthropic},
					&providerAccount{Provider: tt.provider},
				),
			}
			cfg := llm.LanguageModelConfig{Model: tt.model, MaxGeneratedTokens: 16000}

			chatHop, chatAnthropic := b.chatReasoningFor(tt.provider, cfg), b.chatReasoningFor(schemas.Anthropic, cfg)
			responsesHop, responsesAnthropic := b.responsesReasoningFor(tt.provider, cfg), b.responsesReasoningFor(schemas.Anthropic, cfg)
			if tt.wantAnthropic {
				assert.Equal(t, chatAnthropic, chatHop, "chat path")
				assert.Equal(t, responsesAnthropic, responsesHop, "Responses path")
				return
			}
			assert.NotEqual(t, responsesAnthropic, responsesHop, "Responses path")
		})
	}
}

// TestApplyHopRequestLeavesSharedRequestIntact guards the shallow-copy
// hazard: Bifrost's fallback request shares Input and Params with the primary,
// so the hook must replace them instead of writing through the shared values.
func TestApplyHopRequestLeavesSharedRequestIntact(t *testing.T) {
	b := &LLM{
		provider:         schemas.Anthropic,
		reasoningEffort:  "xhigh",
		reasoningEnabled: true,
		account: testAccount(
			&providerAccount{Provider: schemas.Anthropic},
			customAccount(schemas.Anthropic, "svc2"),
		),
	}
	cfg := llm.LanguageModelConfig{Model: "claude-opus-4-7", MaxGeneratedTokens: 200000}
	request := llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}}}
	primary := &schemas.BifrostRequest{ChatRequest: b.convertToBifrostRequest(request, cfg)}
	sharedParams, sharedInput := primary.ChatRequest.Params, primary.ChatRequest.Input

	hop := *primary.ChatRequest
	hop.Provider = customProviderName(schemas.Anthropic, "svc2")
	hop.Model = "claude-opus-4-6"
	hopReq := &schemas.BifrostRequest{ChatRequest: &hop}
	require.NoError(t, b.applyHopRequest(hopReq, request, cfg))

	rebuilt := hopReq.ChatRequest
	assert.Equal(t, hop.Provider, rebuilt.Provider)
	assert.Equal(t, hop.Model, rebuilt.Model)
	assert.Equal(t, primary.ChatRequest.Fallbacks, rebuilt.Fallbacks)
	require.NotNil(t, rebuilt.Params.Reasoning)
	assert.Equal(t, "high", *rebuilt.Params.Reasoning.Effort, "the Opus 4.6 hop must get an effort it accepts")
	require.NotNil(t, rebuilt.Params.MaxCompletionTokens)
	assert.Equal(t, 128000, *rebuilt.Params.MaxCompletionTokens, "the Opus 4.6 hop must get its own output limit")
	assert.Same(t, sharedParams, primary.ChatRequest.Params)
	assert.NotSame(t, sharedParams, rebuilt.Params)
	assert.Equal(t, "xhigh", *sharedParams.Reasoning.Effort, "the primary's shared params must be untouched")
	assert.Equal(t, 200000, *sharedParams.MaxCompletionTokens, "the primary's shared params must be untouched")
	assert.Equal(t, sharedInput, primary.ChatRequest.Input, "the primary's shared input must be untouched")
}
