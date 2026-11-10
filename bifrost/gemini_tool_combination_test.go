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
	"sync"
	"testing"

	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// geminiToolCombinationStream is a Gemini stream in which the model runs
// Google Search itself and then calls a function, as it does when
// includeServerSideToolInvocations is on.
const geminiToolCombinationStream = `{"candidates":[{"index":0,"content":{"role":"model","parts":[` +
	`{"thoughtSignature":"c2lnLXRvb2xjYWxs","toolCall":{"toolType":"GOOGLE_SEARCH_WEB","args":{"queries":["mattermost agents"]},"id":"srch1"}},` +
	`{"thoughtSignature":"c2lnLXRvb2xyZXNwb25zZQ==","toolResponse":{"toolType":"GOOGLE_SEARCH_WEB","response":{"search_suggestions":"<div>chips</div>"},"id":"srch1"}},` +
	`{"thoughtSignature":"c2lnLWZ1bmN0aW9u","functionCall":{"name":"search_posts","args":{"query":"agents"}}}` +
	`]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["mattermost agents"],` +
	`"groundingChunks":[{"web":{"uri":"https://example.com/a","title":"Example"}}],` +
	`"groundingSupports":[{"segment":{"startIndex":0,"endIndex":5},"groundingChunkIndices":[0]}]}}],` +
	`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`

const geminiTextStream = `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],` +
	`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":1,"totalTokenCount":13}}`

// recordingProvider captures every request body sent to a fake provider and
// answers each with the stream returned by respond.
type recordingProvider struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func newRecordingProvider(t *testing.T, respond func(r *http.Request, call int, w http.ResponseWriter)) (*recordingProvider, *httptest.Server) {
	t.Helper()
	p := &recordingProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(data, &parsed)
		p.mu.Lock()
		p.bodies = append(p.bodies, parsed)
		call := len(p.bodies) - 1
		p.mu.Unlock()
		respond(r, call, w)
	}))
	t.Cleanup(server.Close)
	return p, server
}

func (p *recordingProvider) body(t *testing.T, i int) map[string]any {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Greater(t, len(p.bodies), i, "provider call %d was not made", i)
	return p.bodies[i]
}

func geminiSSE(w http.ResponseWriter, chunk string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\n", chunk)
}

func searchPostsToolStore() *llm.ToolStore {
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name:        "search_posts",
		Description: "search posts",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
	}})
	return store
}

func readStream(t *testing.T, result *llm.TextStreamResult) []llm.TextStreamEvent {
	t.Helper()
	var events []llm.TextStreamEvent
	for event := range result.Stream {
		require.NotEqual(t, llm.EventTypeError, event.Type, "unexpected stream error: %v", event.Value)
		events = append(events, event)
	}
	return events
}

// wireToolKinds summarizes the tools in a captured Gemini request body.
func wireToolKinds(body map[string]any) (functionNames []string, hasGoogleSearch bool) {
	tools, _ := body["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if _, ok := tool["googleSearch"]; ok {
			hasGoogleSearch = true
		}
		declarations, _ := tool["functionDeclarations"].([]any)
		for _, rawDecl := range declarations {
			decl, _ := rawDecl.(map[string]any)
			name, _ := decl["name"].(string)
			functionNames = append(functionNames, name)
		}
	}
	return functionNames, hasGoogleSearch
}

func wireIncludeServerSideToolInvocations(body map[string]any) (value, present bool) {
	toolConfig, _ := body["toolConfig"].(map[string]any)
	value, present = toolConfig["includeServerSideToolInvocations"].(bool)
	return value, present
}

func TestGeminiWebSearchWithFunctionToolsRequest(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		nativeTools  []string
		withFunction bool
		wantFlag     bool
		wantSearch   bool
		wantFuncs    []string
	}{
		{
			name:         "web search with function tools sets the flag and keeps both",
			model:        "gemini-3-flash-preview",
			nativeTools:  []string{llm.NativeToolWebSearch},
			withFunction: true,
			wantFlag:     true,
			wantSearch:   true,
			wantFuncs:    []string{"search_posts"},
		},
		{
			name:         "function tools only is unchanged",
			model:        "gemini-3-flash-preview",
			withFunction: true,
			wantFuncs:    []string{"search_posts"},
		},
		{
			name:        "web search only is unchanged",
			model:       "gemini-3-flash-preview",
			nativeTools: []string{llm.NativeToolWebSearch},
			wantSearch:  true,
		},
		{
			name:         "models without tool combination keep the previous behavior",
			model:        "gemini-2.5-flash",
			nativeTools:  []string{llm.NativeToolWebSearch},
			withFunction: true,
			wantFuncs:    []string{"search_posts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, server := newRecordingProvider(t, func(_ *http.Request, _ int, w http.ResponseWriter) {
				geminiSSE(w, geminiTextStream)
			})
			service := llm.ServiceConfig{
				ID: "gemini", Type: llm.ServiceTypeGemini, APIKey: "test-key", APIURL: server.URL, DefaultModel: tt.model,
			}
			bot := llm.BotConfig{ID: "bot-1", ServiceID: service.ID, EnabledNativeTools: tt.nativeTools}
			agent, err := NewFromServiceConfig(service, bot, nil)
			require.NoError(t, err)
			t.Cleanup(agent.Shutdown)

			request := llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}}}
			if tt.withFunction {
				request.Context = &llm.Context{Tools: searchPostsToolStore()}
			}
			result, err := agent.ChatCompletion(context.Background(), request)
			require.NoError(t, err)
			readStream(t, result)

			body := provider.body(t, 0)
			funcs, hasSearch := wireToolKinds(body)
			assert.Equal(t, tt.wantFuncs, funcs)
			assert.Equal(t, tt.wantSearch, hasSearch)
			flag, present := wireIncludeServerSideToolInvocations(body)
			assert.Equal(t, tt.wantFlag, present && flag)
			if !tt.wantFlag {
				assert.False(t, present, "toolConfig.includeServerSideToolInvocations must not be sent")
			}
		})
	}
}

// TestGeminiWebSearchWithFunctionToolsRoundTrip drives a full tool round:
// the model searches and calls a function, then the function result goes back
// in the next request.
func TestGeminiWebSearchWithFunctionToolsRoundTrip(t *testing.T) {
	provider, server := newRecordingProvider(t, func(_ *http.Request, call int, w http.ResponseWriter) {
		if call == 0 {
			geminiSSE(w, geminiToolCombinationStream)
			return
		}
		geminiSSE(w, geminiTextStream)
	})
	service := llm.ServiceConfig{
		ID: "gemini", Type: llm.ServiceTypeGemini, APIKey: "test-key", APIURL: server.URL, DefaultModel: "gemini-3-flash-preview",
	}
	bot := llm.BotConfig{ID: "bot-1", ServiceID: service.ID, EnabledNativeTools: []string{llm.NativeToolWebSearch}}
	agent, err := NewFromServiceConfig(service, bot, nil)
	require.NoError(t, err)
	t.Cleanup(agent.Shutdown)

	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "find agents posts"}},
		Context: &llm.Context{Tools: searchPostsToolStore()},
	}
	result, err := agent.ChatCompletion(context.Background(), request)
	require.NoError(t, err)
	events := readStream(t, result)

	var toolCalls []llm.ToolCall
	var serverTools []llm.ServerToolUse
	var annotations []llm.Annotation
	for _, event := range events {
		switch event.Type {
		case llm.EventTypeToolCalls:
			toolCalls = event.Value.([]llm.ToolCall)
		case llm.EventTypeServerToolUse:
			serverTools = event.Value.([]llm.ServerToolUse)
		case llm.EventTypeAnnotations:
			annotations = event.Value.([]llm.Annotation)
		}
	}

	require.Len(t, toolCalls, 1, "the function call must survive alongside the server-side search parts")
	assert.Equal(t, "search_posts", toolCalls[0].Name)
	assert.JSONEq(t, `{"query":"agents"}`, string(toolCalls[0].Arguments))
	require.Len(t, serverTools, 1)
	assert.Equal(t, llm.NativeToolWebSearch, serverTools[0].Tool)
	assert.Equal(t, "mattermost agents", serverTools[0].Query)
	assert.Equal(t, llm.ServerToolStatusSuccess, serverTools[0].Status)
	require.NotEmpty(t, annotations, "grounding citations must still be produced")
	assert.Equal(t, "https://example.com/a", annotations[0].URL)

	toolCalls[0].Result = "found 2 posts"
	toolCalls[0].Status = llm.ToolCallStatusSuccess
	request.Posts = append(request.Posts, llm.Post{Role: llm.PostRoleBot, ToolUse: toolCalls})
	result, err = agent.ChatCompletion(context.Background(), request)
	require.NoError(t, err)
	readStream(t, result)

	second := provider.body(t, 1)
	funcs, hasSearch := wireToolKinds(second)
	assert.Equal(t, []string{"search_posts"}, funcs)
	assert.True(t, hasSearch)
	flag, present := wireIncludeServerSideToolInvocations(second)
	assert.True(t, present && flag, "follow-up turn must keep the flag")

	var call, response map[string]any
	contents, _ := second["contents"].([]any)
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if fc, ok := part["functionCall"].(map[string]any); ok {
				call = part
				assert.Equal(t, "search_posts", fc["name"])
			}
			if _, ok := part["functionResponse"]; ok {
				response = part
			}
		}
	}
	require.NotNil(t, call, "replayed history must include the function call")
	assert.Equal(t, "c2lnLWZ1bmN0aW9u", call["thoughtSignature"], "the function call's thought signature must be replayed")
	require.NotNil(t, response, "replayed history must include the function response")
}

func TestOpenAIWebSearchWithFunctionToolsRequestUnchanged(t *testing.T) {
	provider, server := newRecordingProvider(t, func(_ *http.Request, _ int, w http.ResponseWriter) {
		responsesSSE(w, "ok")
	})
	service := llm.ServiceConfig{
		ID: "openai", Type: llm.ServiceTypeOpenAI, APIKey: "test-key", APIURL: server.URL, DefaultModel: "gpt-5",
	}
	bot := llm.BotConfig{ID: "bot-1", ServiceID: service.ID, EnabledNativeTools: []string{llm.NativeToolWebSearch}}
	agent, err := NewFromServiceConfig(service, bot, nil)
	require.NoError(t, err)
	t.Cleanup(agent.Shutdown)

	result, err := agent.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: &llm.Context{Tools: searchPostsToolStore()},
	})
	require.NoError(t, err)
	readStream(t, result)

	body := provider.body(t, 0)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "server_side")
	assert.NotContains(t, string(encoded), "ServerSide")

	var types []string
	tools, _ := body["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		types = append(types, tool["type"].(string))
	}
	assert.ElementsMatch(t, []string{"web_search", "function"}, types)
}

// TestServerSideToolInvocationsAcrossProviders builds the request each
// provider type receives for web search plus function tools and runs it
// through Bifrost's own Gemini converter where the provider is Google.
func TestServerSideToolInvocationsAcrossProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider schemas.ModelProvider
		model    string
		wantFlag bool
	}{
		{name: "gemini 3", provider: schemas.Gemini, model: "gemini-3-flash-preview", wantFlag: true},
		{name: "gemini 4", provider: schemas.Gemini, model: "gemini-4-argon", wantFlag: true},
		{name: "vertex 3", provider: schemas.Vertex, model: "gemini-3.5-flash"},
		{name: "vertex with publisher path", provider: schemas.Vertex, model: "publishers/google/models/gemini-3.5-flash"},
		{name: "gemini 2.5", provider: schemas.Gemini, model: "gemini-2.5-pro"},
		{name: "vertex 2.5", provider: schemas.Vertex, model: "gemini-2.5-flash"},
		{name: "vertex unrecognized model", provider: schemas.Vertex, model: "my-tuned-endpoint"},
		{name: "openai", provider: schemas.OpenAI, model: "gpt-5"},
		{name: "anthropic", provider: schemas.Anthropic, model: "claude-sonnet-4-6"},
		{name: "bedrock", provider: schemas.Bedrock, model: "anthropic.claude-sonnet-4-6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &LLM{provider: tt.provider, enabledNativeTools: []string{llm.NativeToolWebSearch}}
			cfg := llm.LanguageModelConfig{Model: tt.model, MaxGeneratedTokens: 1024}
			request := llm.CompletionRequest{
				Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
				Context: &llm.Context{Tools: searchPostsToolStore()},
			}

			bifrostReq, err := b.convertToBifrostResponsesRequest(request, cfg)
			require.NoError(t, err)
			require.NotNil(t, bifrostReq.Params)

			if tt.wantFlag {
				require.NotNil(t, bifrostReq.Params.IncludeServerSideToolInvocations)
				assert.True(t, *bifrostReq.Params.IncludeServerSideToolInvocations)
			} else {
				assert.Nil(t, bifrostReq.Params.IncludeServerSideToolInvocations)
			}

			if tt.provider != schemas.Gemini && tt.provider != schemas.Vertex {
				return
			}

			wire, err := gemini.ToGeminiResponsesRequest(nil, bifrostReq)
			require.NoError(t, err)
			encoded, err := json.Marshal(wire)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.Unmarshal(encoded, &body))
			flag, present := wireIncludeServerSideToolInvocations(body)
			assert.Equal(t, tt.wantFlag, present && flag)
			if !tt.wantFlag {
				assert.False(t, present)
				return
			}
			funcs, hasSearch := wireToolKinds(body)
			assert.Equal(t, []string{"search_posts"}, funcs)
			assert.True(t, hasSearch)
		})
	}
}

func TestServerSideToolInvocationsForToolMix(t *testing.T) {
	webSearch := schemas.ResponsesTool{Type: schemas.ResponsesToolTypeWebSearch}
	function := schemas.ResponsesTool{Type: schemas.ResponsesToolTypeFunction, Name: new("search_posts")}

	tests := []struct {
		name  string
		tools []schemas.ResponsesTool
		want  bool
	}{
		{name: "both kinds", tools: []schemas.ResponsesTool{webSearch, function}, want: true},
		{name: "web search only", tools: []schemas.ResponsesTool{webSearch}},
		{name: "function only", tools: []schemas.ResponsesTool{function}},
		{name: "none"},
	}

	b := &LLM{provider: schemas.Gemini}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.serverSideToolInvocationsFor(schemas.Gemini, "gemini-3-flash-preview", tt.tools)
			if !tt.want {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.True(t, *got)
		})
	}
}

// TestServerSideToolInvocationsFlagIsPerHop checks that a fallback chain from
// a non-Google primary to Gemini sends the flag only to the Gemini hop.
func TestServerSideToolInvocationsFlagIsPerHop(t *testing.T) {
	geminiProvider, geminiServer := newRecordingProvider(t, func(_ *http.Request, _ int, w http.ResponseWriter) {
		geminiSSE(w, geminiTextStream)
	})
	openAIProvider, openAIServer := newRecordingProvider(t, func(_ *http.Request, _ int, w http.ResponseWriter) {
		http.Error(w, `{"error":{"message":"rejected","type":"invalid_request_error"}}`, http.StatusBadRequest)
	})

	primary := llm.ServiceConfig{ID: "primary", Type: llm.ServiceTypeOpenAI, APIKey: "k1", APIURL: openAIServer.URL, DefaultModel: "gpt-5"}
	fallback := llm.ServiceConfig{ID: "fallback", Type: llm.ServiceTypeGemini, APIKey: "k2", APIURL: geminiServer.URL, DefaultModel: "gemini-3-flash-preview"}
	bot := llm.BotConfig{ID: "bot-1", ServiceID: primary.ID, EnabledNativeTools: []string{llm.NativeToolWebSearch}}
	agent, err := NewFromServiceConfig(primary, bot, []llm.ServiceConfig{fallback})
	require.NoError(t, err)
	t.Cleanup(agent.Shutdown)

	result, err := agent.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: &llm.Context{Tools: searchPostsToolStore()},
	})
	require.NoError(t, err)
	readStream(t, result)

	openAIBody := openAIProvider.body(t, 0)
	geminiBody := geminiProvider.body(t, 0)

	encoded, err := json.Marshal(openAIBody)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "server_side")

	funcs, hasSearch := wireToolKinds(geminiBody)
	assert.Equal(t, []string{"search_posts"}, funcs)
	assert.True(t, hasSearch)
	flag, present := wireIncludeServerSideToolInvocations(geminiBody)
	assert.True(t, present && flag)
}

func TestGeminiSupportsToolCombination(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-3-flash-preview", true},
		{"gemini-3.8-flash", true},
		{"gemini-4-argon", true},
		{"gemini-10-pro", true},
		{"Gemini-3-Pro", true},
		{"models/gemini-3-flash", true},
		{"publishers/google/models/gemini-3.5-flash", true},
		{"gemini-2.5-pro", false},
		{"gemini-2.0-flash", false},
		{"gemini-1.5-pro", false},
		{"gemini-flash-latest", false},
		{"gemini-", false},
		{"gemma-3-27b", false},
		{"gpt-5", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.want, geminiSupportsToolCombination(tt.model))
		})
	}
}
