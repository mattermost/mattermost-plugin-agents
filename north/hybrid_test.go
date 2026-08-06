// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package north

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
)

type weatherArgs struct {
	Location string `json:"location" jsonschema:"City name"`
}

// newWeatherStore builds a tool store with a single local get_weather tool
// that records executions.
func newWeatherStore(executed *atomic.Int32) *llm.ToolStore {
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{{
		Name:        "get_weather",
		Description: "Get the current weather for a location.",
		Schema:      llm.NewJSONSchemaFromStruct[weatherArgs](),
		Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var args weatherArgs
			if err := argsGetter(&args); err != nil {
				return "", err
			}
			if executed != nil {
				executed.Add(1)
			}
			return fmt.Sprintf(`{"location":%q,"temp_c":17,"condition":"Foggy"}`, args.Location), nil
		},
	}})
	return store
}

// hybridHandler serves GET /v1/agents/{id} plus scripted POST /v1/chat rounds.
func hybridHandler(t *testing.T, agentTools []ChatTool, chatRounds func(round int, req ChatRequest, w http.ResponseWriter)) http.HandlerFunc {
	t.Helper()
	var round atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/agents/") {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(Agent{
				ID:    strings.TrimPrefix(r.URL.Path, "/v1/agents/"),
				Name:  "Test Agent",
				Tools: agentTools,
			}))
			return
		}
		require.Equal(t, "/v1/chat", r.URL.Path)
		var req ChatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		chatRounds(int(round.Add(1)), req, w)
	}
}

func writeSSE(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, line := range lines {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
}

func toolNames(tools []ChatTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		switch {
		case tool.Function != nil:
			names = append(names, tool.Function.Name)
		case tool.NorthTool != nil:
			names = append(names, tool.NorthTool.Name)
		}
	}
	return names
}

func TestHybridRequestToolMapping(t *testing.T) {
	hostedTools := []ChatTool{{Type: "north_tool", NorthTool: &HostedToolDefinition{Name: "tavily_web_search"}}}

	tests := []struct {
		name          string
		store         *llm.ToolStore
		agentTools    []ChatTool
		opts          []llm.LanguageModelOption
		wantToolNames []string // nil means the tools field must be omitted
	}{
		{
			name:          "nil store omits tools (hosted tools stay active)",
			store:         nil,
			agentTools:    hostedTools,
			wantToolNames: nil,
		},
		{
			name:          "empty store omits tools (pure delegation bot)",
			store:         llm.NewNoTools(),
			agentTools:    hostedTools,
			wantToolNames: nil,
		},
		{
			name:          "mattermost tools forwarded as function tools plus bridge",
			store:         newWeatherStore(nil),
			agentTools:    hostedTools,
			wantToolNames: []string{"get_weather", BridgeToolName},
		},
		{
			name:          "no bridge when the north agent has no hosted tools",
			store:         newWeatherStore(nil),
			agentTools:    nil,
			wantToolNames: []string{"get_weather"},
		},
		{
			name:          "tools disabled option omits tools entirely",
			store:         newWeatherStore(nil),
			agentTools:    hostedTools,
			opts:          []llm.LanguageModelOption{llm.WithToolsDisabled()},
			wantToolNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured ChatRequest
			server := httptest.NewServer(hybridHandler(t, tt.agentTools, func(round int, req ChatRequest, w http.ResponseWriter) {
				captured = req
				writeSSE(w, `{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`)
			}))
			defer server.Close()

			provider := newTestProvider(server.URL, "agent-id")
			request := llm.CompletionRequest{
				Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
				Context: &llm.Context{Tools: tt.store},
			}
			stream, err := provider.ChatCompletion(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			collectEvents(t, stream)

			if tt.wantToolNames == nil {
				assert.Nil(t, captured.Tools, "tools field must be omitted")
				return
			}
			names := toolNames(captured.Tools)
			for _, want := range tt.wantToolNames {
				assert.Contains(t, names, want)
			}
			assert.Len(t, names, len(tt.wantToolNames))
			for _, tool := range captured.Tools {
				require.NotNil(t, tool.Function, "hybrid requests must only carry function tools (mixing is rejected by North)")
				assert.Nil(t, tool.NorthTool)
				assert.NotEmpty(t, tool.Function.Parameters, "function parameters must be a JSON schema object")
			}
		})
	}
}

func TestHybridToolUseHistoryMapping(t *testing.T) {
	var captured ChatRequest
	server := httptest.NewServer(hybridHandler(t, nil, func(round int, req ChatRequest, w http.ResponseWriter) {
		captured = req
		writeSSE(w, `{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`)
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	request := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleUser, Message: "weather in Toronto?"},
			{
				Role:    llm.PostRoleBot,
				Message: "Checking.",
				ToolUse: []llm.ToolCall{{
					ID:        "call-1",
					Name:      "get_weather",
					Arguments: json.RawMessage(`{"location":"Toronto"}`),
					Result:    `{"temp_c":17}`,
					Status:    llm.ToolCallStatusSuccess,
				}},
			},
		},
	}
	stream, err := provider.ChatCompletion(context.Background(), request)
	require.NoError(t, err)
	collectEvents(t, stream)

	require.Len(t, captured.Messages, 3)
	assert.Equal(t, "user", captured.Messages[0].Role)

	assistant := captured.Messages[1]
	assert.Equal(t, "assistant", assistant.Role)
	assert.Equal(t, "Checking.", assistant.Content)
	require.Len(t, assistant.ToolCalls, 1)
	assert.Equal(t, "call-1", assistant.ToolCalls[0].ToolCallID)
	assert.Equal(t, "get_weather", assistant.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"location":"Toronto"}`, assistant.ToolCalls[0].Function.Arguments)

	result := captured.Messages[2]
	assert.Equal(t, "tool", result.Role)
	assert.Equal(t, "call-1", result.ToolCallID)
	assert.Equal(t, `{"temp_c":17}`, result.Content)
}

func TestHybridStreamedToolCallsEmitted(t *testing.T) {
	server := httptest.NewServer(hybridHandler(t, nil, func(round int, req ChatRequest, w http.ResponseWriter) {
		writeSSE(w,
			`{"type":"stream-start","conversation_id":"c1"}`,
			`{"type":"tool-call-start","index":0,"delta":{"message":{"tool_calls":{"tool_call_id":"get_weather_abc","type":"function","function":{"name":"get_weather","arguments":""},"display_name":"get_weather","state":"pending"}}}}`,
			`{"type":"tool-call-delta","index":0,"delta":{"message":{"tool_calls":{"type":"function","function":{"arguments":"{\"location\""},"state":"pending"}}}}`,
			`{"type":"tool-call-delta","index":0,"delta":{"message":{"tool_calls":{"type":"function","function":{"arguments":": \"Toronto\"}"},"state":"pending"}}}}`,
			`{"type":"tool-call-end","index":0}`,
			`{"type":"message-end","delta":{"finish_reason":"TOOL_CALL","usage":{"tokens":{"input_tokens":10,"output_tokens":4}}}}`,
			`{"type":"stream-end","delta":{"finish_reason":"TOOL_CALL"}}`,
		)
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "weather in Toronto?"}},
		Context: &llm.Context{Tools: newWeatherStore(nil)},
	}
	stream, err := provider.ChatCompletion(context.Background(), request)
	require.NoError(t, err)
	events := collectEvents(t, stream)

	toolCallEvents := eventsOfType(events, llm.EventTypeToolCalls)
	require.Len(t, toolCallEvents, 1)
	toolCalls, ok := toolCallEvents[0].Value.([]llm.ToolCall)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "get_weather_abc", toolCalls[0].ID)
	assert.Equal(t, "get_weather", toolCalls[0].Name)
	assert.JSONEq(t, `{"location": "Toronto"}`, string(toolCalls[0].Arguments))
	assert.Equal(t, llm.ToolCallStatusPending, toolCalls[0].Status)

	// Offered tools must not be narrated as reasoning.
	assert.NotContains(t, joinTextEvents(events, llm.EventTypeReasoning), "Running North tool")
	assert.Equal(t, llm.EventTypeEnd, events[len(events)-1].Type)
}

func TestHostedToolActivityNarratedNotEmitted(t *testing.T) {
	// Pure delegation (no Mattermost tools): North-side tool activity streams
	// tool-call events, which must be narrated as reasoning, never emitted as
	// plugin tool calls.
	server := httptest.NewServer(hybridHandler(t, nil, func(round int, req ChatRequest, w http.ResponseWriter) {
		writeSSE(w,
			`{"type":"tool-call-start","index":0,"delta":{"message":{"tool_calls":{"tool_call_id":"search_1","type":"function","function":{"name":"web_tools_search","arguments":""},"display_name":"Web Search","state":"pending"}}}}`,
			`{"type":"tool-call-end","index":0}`,
			`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"Found it."}}}}`,
			`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
		)
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	stream, err := provider.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "search something"}},
	})
	require.NoError(t, err)
	events := collectEvents(t, stream)

	assert.Empty(t, eventsOfType(events, llm.EventTypeToolCalls))
	assert.Contains(t, joinTextEvents(events, llm.EventTypeReasoning), "Running North tool: Web Search")
	assert.Equal(t, "Found it.", joinTextEvents(events, llm.EventTypeText))
}

// TestHybridFullToolLoop drives the real tool runner through two rounds
// against a scripted North server: round 1 returns a TOOL_CALL for the local
// get_weather tool; round 2 must receive the tool result and returns the
// final text.
func TestHybridFullToolLoop(t *testing.T) {
	var round2Request ChatRequest
	server := httptest.NewServer(hybridHandler(t,
		[]ChatTool{{Type: "north_tool", NorthTool: &HostedToolDefinition{Name: "tavily_web_search"}}},
		func(round int, req ChatRequest, w http.ResponseWriter) {
			switch round {
			case 1:
				writeSSE(w,
					`{"type":"tool-call-start","index":0,"delta":{"message":{"tool_calls":{"tool_call_id":"get_weather_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\": \"Toronto\"}"},"display_name":"get_weather","state":"pending"}}}}`,
					`{"type":"tool-call-end","index":0}`,
					`{"type":"stream-end","delta":{"finish_reason":"TOOL_CALL"}}`,
				)
			default:
				round2Request = req
				writeSSE(w,
					`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"It is 17°C and foggy in Toronto."}}}}`,
					`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
				)
			}
		}))
	defer server.Close()

	var executed atomic.Int32
	provider := newTestProvider(server.URL, "agent-id")
	request := llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "What's the weather in Toronto?"}},
		Context: &llm.Context{Tools: newWeatherStore(&executed)},
	}

	runner := toolrunner.New(provider)
	result, err := runner.Run(context.Background(), request, func(llm.ToolCall) bool { return true }, nil)
	require.NoError(t, err)

	finalText, err := result.Stream.ReadAll()
	require.NoError(t, err)
	assert.Equal(t, "It is 17°C and foggy in Toronto.", finalText)
	assert.Equal(t, int32(1), executed.Load(), "the local tool must execute exactly once")
	require.Len(t, result.ToolTurns, 1)

	// Round 2 must replay the tool transcript.
	var sawToolResult bool
	for _, message := range round2Request.Messages {
		if message.Role == "tool" && message.ToolCallID == "get_weather_1" {
			sawToolResult = true
			assert.Contains(t, message.Content, `"temp_c":17`)
		}
	}
	assert.True(t, sawToolResult, "round 2 request must contain the role:tool result message")
}

func TestBridgeToolResolverRunsNestedHostedCall(t *testing.T) {
	var nestedRequest *ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/agents/") {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(Agent{
				ID:    "agent-id",
				Tools: []ChatTool{{Type: "north_tool", NorthTool: &HostedToolDefinition{Name: "tavily_web_search"}}},
			}))
			return
		}
		var req ChatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		nestedRequest = &req
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"conversation_id": "c1",
			"finish_reason": "COMPLETE",
			"messages": [{
				"role": "assistant",
				"content": [{"type": "text", "text": "Go 1.26.5 is the latest stable release."}],
				"citations": [{
					"text": "Go 1.26.5",
					"start": 0, "end": 9,
					"sources": [{"type":"document","id":"d1","document":{"url":"https://example.com/go","title":"Go releases"}}]
				}]
			}]
		}`)
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	names := provider.hostedToolNames(context.Background(), "agent-id")
	require.Equal(t, []string{"tavily_web_search"}, names)

	tool := provider.bridgeTool("agent-id", names)
	assert.Equal(t, BridgeToolName, tool.Name)
	assert.Contains(t, tool.Description, "tavily_web_search")

	result, err := tool.Resolver(context.Background(), &llm.Context{}, func(args any) error {
		return json.Unmarshal([]byte(`{"task":"What is the latest stable Go version?"}`), args)
	})
	require.NoError(t, err)
	assert.Contains(t, result, "Go 1.26.5 is the latest stable release.")
	assert.Contains(t, result, "Sources:")
	assert.Contains(t, result, "https://example.com/go")

	// The nested call must run hosted-tools-only: tools omitted, agent set.
	require.NotNil(t, nestedRequest)
	assert.Nil(t, nestedRequest.Tools)
	require.NotNil(t, nestedRequest.Agent)
	assert.Equal(t, "agent-id", nestedRequest.Agent.ID)
	assert.True(t, nestedRequest.Stateless)
}

func TestBridgeToolResolverErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		handler    http.HandlerFunc
		wantErrSub string
	}{
		{
			name:       "empty task rejected without a request",
			args:       `{"task":"  "}`,
			handler:    func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") },
			wantErrSub: "task must not be empty",
		},
		{
			name: "north error surfaces",
			args: `{"task":"do something"}`,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error_type":"overloaded_error","error_code":"OVERLOADED","message":"North is overloaded.","request_id":"r1","status_code":503,"is_retryable":true}`)
			},
			wantErrSub: "North is overloaded.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			provider := newTestProvider(server.URL, "agent-id")
			tool := provider.bridgeTool("agent-id", []string{"tavily_web_search"})
			_, err := tool.Resolver(context.Background(), &llm.Context{}, func(args any) error {
				return json.Unmarshal([]byte(tt.args), args)
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrSub)
		})
	}
}

func TestHostedToolNamesCaching(t *testing.T) {
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(Agent{
			ID:    "agent-id",
			Tools: []ChatTool{{Type: "north_tool", NorthTool: &HostedToolDefinition{Name: "web_scrape"}}},
		}))
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	for range 3 {
		assert.Equal(t, []string{"web_scrape"}, provider.hostedToolNames(context.Background(), "agent-id"))
	}
	assert.Equal(t, int32(1), fetches.Load(), "successful lookups must be cached")
}
