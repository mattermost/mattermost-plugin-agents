// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// chatToolCallSSE streams a chat completion that ends in a tool call, with
// usage in a trailing chunk after the finish reason (as OpenAI sends it).
func chatToolCallSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}]}`+"\n\n")
	fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
	fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// responsesToolCallSSE streams a Responses API turn that ends in a tool call.
func responsesToolCallSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: response.output_item.added\ndata: "+`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"","status":"in_progress"}}`+"\n\n")
	fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: "+`{"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"fc_1","delta":"{}"}`+"\n\n")
	fmt.Fprint(w, "event: response.completed\ndata: "+`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}","status":"completed"}],"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}`+"\n\n")
}

// chatIDlessToolCallsSSE streams two tool calls without IDs, as some
// OpenAI-compatible servers do, ending with finishReason or, when it is empty,
// without a finish reason.
func chatIDlessToolCallsSSE(finishReason string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"type":"function","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`+"\n\n")
		if finishReason != "" {
			fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"`+finishReason+`"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

func TestStreamedToolCallsWithoutIDsGetUniqueIDs(t *testing.T) {
	tests := []struct {
		name  string
		write func(http.ResponseWriter)
	}{
		{name: "tool_calls finish reason", write: chatIDlessToolCallsSSE("tool_calls")},
		{name: "stream ends without a finish reason", write: chatIDlessToolCallsSSE("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.write(w)
			}))
			defer server.Close()

			service := llm.ServiceConfig{
				ID:           "svc",
				Type:         llm.ServiceTypeOpenAICompatible,
				APIKey:       "key",
				APIURL:       server.URL,
				DefaultModel: "model",
			}
			lm, err := NewFromServiceConfig(service, llm.BotConfig{ID: "bot", ServiceID: service.ID}, nil)
			require.NoError(t, err)
			defer lm.Shutdown()

			stream, err := lm.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			})
			require.NoError(t, err)

			var toolCalls []llm.ToolCall
			for event := range stream.Stream {
				switch event.Type {
				case llm.EventTypeToolCalls:
					toolCalls = event.Value.([]llm.ToolCall)
				case llm.EventTypeError:
					t.Fatalf("stream error: %v", event.Value)
				}
			}

			require.Len(t, toolCalls, 2)
			require.Equal(t, []string{"lookup", "search"}, []string{toolCalls[0].Name, toolCalls[1].Name})
			require.NotEmpty(t, toolCalls[0].ID)
			require.NotEmpty(t, toolCalls[1].ID)
			require.NotEqual(t, toolCalls[0].ID, toolCalls[1].ID)
		})
	}
}

func TestToolCallRoundReportsUsage(t *testing.T) {
	tests := []struct {
		name         string
		useResponses bool
		path         string
		write        func(http.ResponseWriter)
		wantReason   string
	}{
		{name: "chat completions", path: "/v1/chat/completions", write: chatToolCallSSE, wantReason: "tool_calls"},
		{name: "responses", useResponses: true, path: "/v1/responses", write: responsesToolCallSSE, wantReason: "tool_calls"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			previous := otel.GetTracerProvider()
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			otel.SetTracerProvider(tp)
			t.Cleanup(func() {
				_ = tp.Shutdown(context.Background())
				otel.SetTracerProvider(previous)
			})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
					return
				}
				tc.write(w)
			}))
			defer server.Close()

			service := llm.ServiceConfig{
				ID:              "svc",
				Type:            llm.ServiceTypeOpenAICompatible,
				APIKey:          "key",
				APIURL:          server.URL,
				DefaultModel:    "model",
				UseResponsesAPI: tc.useResponses,
			}
			lm, err := NewFromServiceConfig(service, llm.BotConfig{ID: "bot", ServiceID: service.ID}, nil)
			require.NoError(t, err)
			defer lm.Shutdown()

			ctx := telemetry.WithLLMRound(context.Background(), 2)
			stream, err := lm.ChatCompletion(ctx, llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			})
			require.NoError(t, err)

			var usage *llm.TokenUsage
			var sawToolCalls bool
			for event := range stream.Stream {
				switch event.Type {
				case llm.EventTypeUsage:
					u := event.Value.(llm.TokenUsage)
					usage = &u
				case llm.EventTypeToolCalls:
					sawToolCalls = true
				case llm.EventTypeError:
					t.Fatalf("stream error: %v", event.Value)
				}
			}
			require.True(t, sawToolCalls)
			require.NotNil(t, usage, "a tool-call round must still report usage")
			require.Equal(t, int64(120), usage.InputTokens)
			require.Equal(t, int64(30), usage.OutputTokens)

			var attrs map[string]any
			for _, s := range exporter.GetSpans() {
				if s.Name != "llm chat completion" {
					continue
				}
				require.Nil(t, attrs, "expected one llm chat completion span")
				attrs = map[string]any{}
				for _, a := range s.Attributes {
					attrs[string(a.Key)] = a.Value.AsInterface()
				}
			}
			require.NotNil(t, attrs)
			require.Equal(t, int64(120), attrs[string(telemetry.LLMInputTokens)])
			require.Equal(t, tc.wantReason, attrs[string(telemetry.LLMFinishReason)])
			require.Equal(t, int64(2), attrs[string(telemetry.LLMRound)])
			require.Contains(t, attrs, string(telemetry.LLMTimeToFirstTokenMs))
		})
	}
}
