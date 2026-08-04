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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// sseHandler writes the given SSE payload lines as a streaming response and
// captures the decoded request body.
func sseHandler(t *testing.T, capturedRequest *ChatRequest, sseLines []string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		if capturedRequest != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(capturedRequest))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range sseLines {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
	}
}

func newTestProvider(serverURL string, botModel string) *Provider {
	return New(
		llm.ServiceConfig{
			Type:             llm.ServiceTypeNorth,
			APIURL:           serverURL,
			APIKey:           "test-token",
			DefaultModel:     "service-agent-id",
			InputTokenLimit:  128000,
			OutputTokenLimit: 4096,
		},
		llm.BotConfig{Model: botModel},
	)
}

// collectEvents drains a stream into a slice.
func collectEvents(t *testing.T, stream *llm.TextStreamResult) []llm.TextStreamEvent {
	t.Helper()
	var events []llm.TextStreamEvent
	for event := range stream.Stream {
		events = append(events, event)
	}
	return events
}

func eventsOfType(events []llm.TextStreamEvent, eventType llm.EventType) []llm.TextStreamEvent {
	var filtered []llm.TextStreamEvent
	for _, event := range events {
		if event.Type == eventType {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func joinTextEvents(events []llm.TextStreamEvent, eventType llm.EventType) string {
	var text strings.Builder
	for _, event := range eventsOfType(events, eventType) {
		if chunk, ok := event.Value.(string); ok {
			text.WriteString(chunk)
		}
	}
	return text.String()
}

func TestChatCompletionRequestMapping(t *testing.T) {
	basePosts := []llm.Post{
		{Role: llm.PostRoleSystem, Message: "system prompt"},
		{Role: llm.PostRoleUser, Message: "hello"},
		{Role: llm.PostRoleBot, Message: "hi there"},
		{Role: llm.PostRoleUser, Message: "   "}, // whitespace-only: skipped
		{Role: llm.PostRoleUser, Message: "follow-up"},
	}
	wantMessages := []ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "follow-up"},
	}

	tests := []struct {
		name          string
		botModel      string
		opts          []llm.LanguageModelOption
		wantAgentID   string // empty means no agent field
		wantThinking  string // empty means no thinking field
		wantMaxTokens int
	}{
		{
			name:          "bot model carries the agent ID",
			botModel:      "bot-agent-id",
			wantAgentID:   "bot-agent-id",
			wantMaxTokens: 4096,
		},
		{
			name:          "service default model used when bot has none",
			botModel:      "",
			wantAgentID:   "service-agent-id",
			wantMaxTokens: 4096,
		},
		{
			name:          "per-call model override wins",
			botModel:      "bot-agent-id",
			opts:          []llm.LanguageModelOption{llm.WithModel("override-agent-id")},
			wantAgentID:   "override-agent-id",
			wantMaxTokens: 4096,
		},
		{
			name:          "reasoning disabled maps to thinking disabled",
			botModel:      "bot-agent-id",
			opts:          []llm.LanguageModelOption{llm.WithReasoningDisabled()},
			wantAgentID:   "bot-agent-id",
			wantThinking:  "disabled",
			wantMaxTokens: 4096,
		},
		{
			name:          "max generated tokens override",
			botModel:      "bot-agent-id",
			opts:          []llm.LanguageModelOption{llm.WithMaxGeneratedTokens(512)},
			wantAgentID:   "bot-agent-id",
			wantMaxTokens: 512,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured ChatRequest
			server := httptest.NewServer(sseHandler(t, &captured, []string{
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			}))
			defer server.Close()

			provider := newTestProvider(server.URL, tt.botModel)
			stream, err := provider.ChatCompletion(context.Background(), llm.CompletionRequest{Posts: basePosts}, tt.opts...)
			require.NoError(t, err)
			collectEvents(t, stream)

			assert.Equal(t, wantMessages, captured.Messages)
			assert.True(t, captured.Stream)
			assert.True(t, captured.Stateless, "requests must be stateless; the plugin resends the full thread")
			assert.Equal(t, tt.wantMaxTokens, captured.MaxTokens)

			if tt.wantAgentID == "" {
				assert.Nil(t, captured.Agent)
			} else {
				require.NotNil(t, captured.Agent)
				assert.Equal(t, tt.wantAgentID, captured.Agent.ID)
			}

			if tt.wantThinking == "" {
				assert.Nil(t, captured.Thinking)
			} else {
				require.NotNil(t, captured.Thinking)
				assert.Equal(t, tt.wantThinking, captured.Thinking.Type)
			}
		})
	}
}

func TestChatCompletionEmptyAgentIDOmitsAgent(t *testing.T) {
	var captured ChatRequest
	server := httptest.NewServer(sseHandler(t, &captured, []string{
		`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
	}))
	defer server.Close()

	provider := New(
		llm.ServiceConfig{Type: llm.ServiceTypeNorth, APIURL: server.URL, APIKey: "test-token"},
		llm.BotConfig{},
	)
	stream, err := provider.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})
	require.NoError(t, err)
	collectEvents(t, stream)

	assert.Nil(t, captured.Agent, "empty agent ID must fall back to North's default agent")
}

func TestChatCompletionStreamTranslation(t *testing.T) {
	tests := []struct {
		name     string
		sseLines []string
		verify   func(t *testing.T, events []llm.TextStreamEvent)
	}{
		{
			name: "text deltas stream as text and end",
			sseLines: []string{
				`{"type":"stream-start","conversation_id":"c1"}`,
				`{"type":"message-start","delta":{"message":{"role":"assistant"}}}`,
				`{"type":"content-start","index":0,"delta":{"message":{"content":{"type":"text","text":""}}}}`,
				`{"type":"content-delta","index":0,"delta":{"message":{"content":{"type":"text","text":"Hello"}}}}`,
				`{"type":"content-delta","index":0,"delta":{"message":{"content":{"type":"text","text":" world"}}}}`,
				`{"type":"content-end","index":0}`,
				`{"type":"message-end","delta":{"finish_reason":"COMPLETE","usage":{"tokens":{"input_tokens":10,"output_tokens":5}}}}`,
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				assert.Equal(t, "Hello world", joinTextEvents(events, llm.EventTypeText))
				usageEvents := eventsOfType(events, llm.EventTypeUsage)
				require.Len(t, usageEvents, 1)
				usage, ok := usageEvents[0].Value.(llm.TokenUsage)
				require.True(t, ok)
				assert.Equal(t, int64(10), usage.InputTokens)
				assert.Equal(t, int64(5), usage.OutputTokens)
				assert.Equal(t, llm.EventTypeEnd, events[len(events)-1].Type)
			},
		},
		{
			name: "thinking content maps to reasoning before text",
			sseLines: []string{
				`{"type":"content-delta","delta":{"message":{"content":{"type":"thinking","thinking":"pondering"}}}}`,
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"answer"}}}}`,
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				assert.Equal(t, "pondering", joinTextEvents(events, llm.EventTypeReasoning))
				reasoningEnd := eventsOfType(events, llm.EventTypeReasoningEnd)
				require.Len(t, reasoningEnd, 1)
				data, ok := reasoningEnd[0].Value.(llm.ReasoningData)
				require.True(t, ok)
				assert.Equal(t, "pondering", data.Text)
				assert.Equal(t, "answer", joinTextEvents(events, llm.EventTypeText))

				// Reasoning must close before text starts.
				var order []llm.EventType
				for _, event := range events {
					order = append(order, event.Type)
				}
				assert.Equal(t, []llm.EventType{
					llm.EventTypeReasoning, llm.EventTypeReasoningEnd, llm.EventTypeText, llm.EventTypeEnd,
				}, order)
			},
		},
		{
			name: "north tool activity narrated as reasoning",
			sseLines: []string{
				`{"type":"tool-plan-delta","delta":{"message":{"tool_plan":"I will search."}}}`,
				`{"type":"tool-call-start","index":0,"delta":{"message":{"tool_calls":{"display_name":"Web Search","function":{"name":"web_search","arguments":"{}"}}}}}`,
				`{"type":"tool-call-end","index":0}`,
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"found it"}}}}`,
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				reasoning := joinTextEvents(events, llm.EventTypeReasoning)
				assert.Contains(t, reasoning, "I will search.")
				assert.Contains(t, reasoning, "Running North tool: Web Search")
				assert.Equal(t, "found it", joinTextEvents(events, llm.EventTypeText))
				assert.Empty(t, eventsOfType(events, llm.EventTypeToolCalls),
					"delegated provider must never emit tool call events")
			},
		},
		{
			name: "citations with URLs become annotations",
			sseLines: []string{
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"Go 1.24 is out."}}}}`,
				`{"type":"citation-start","index":0,"delta":{"message":{"citations":{"text":"Go 1.24","start":0,"end":7,"sources":[{"type":"document","id":"d1","document":{"url":"https://example.com/go","title":"Go releases"}}]}}}}`,
				`{"type":"citation-end","index":0}`,
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				annotationEvents := eventsOfType(events, llm.EventTypeAnnotations)
				require.Len(t, annotationEvents, 1)
				annotations, ok := annotationEvents[0].Value.([]llm.Annotation)
				require.True(t, ok)
				require.Len(t, annotations, 1)
				assert.Equal(t, llm.AnnotationTypeURLCitation, annotations[0].Type)
				assert.Equal(t, "https://example.com/go", annotations[0].URL)
				assert.Equal(t, "Go releases", annotations[0].Title)
				assert.Equal(t, 0, annotations[0].StartIndex)
				assert.Equal(t, 7, annotations[0].EndIndex)
			},
		},
		{
			name: "debug events are dropped",
			sseLines: []string{
				`{"type":"debug","prompt":"SECRET RAW PROMPT"}`,
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"clean"}}}}`,
				`{"type":"debug","raw_generation":"SECRET RAW GENERATION"}`,
				`{"type":"stream-end","delta":{"finish_reason":"COMPLETE"}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				for _, event := range events {
					if value, ok := event.Value.(string); ok {
						assert.NotContains(t, value, "SECRET")
					}
				}
				assert.Equal(t, "clean", joinTextEvents(events, llm.EventTypeText))
			},
		},
		{
			name: "stream-end error becomes an error event",
			sseLines: []string{
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"partial"}}}}`,
				`{"type":"stream-end","delta":{"finish_reason":"ERROR","error":{"error_code":"AGENT_NOT_FOUND","message":"Agent not found."}}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				errorEvents := eventsOfType(events, llm.EventTypeError)
				require.Len(t, errorEvents, 1)
				err, ok := errorEvents[0].Value.(error)
				require.True(t, ok)
				assert.Contains(t, err.Error(), "Agent not found.")
				assert.Empty(t, eventsOfType(events, llm.EventTypeEnd))
			},
		},
		{
			name: "stream cut without terminal event errors",
			sseLines: []string{
				`{"type":"content-delta","delta":{"message":{"content":{"type":"text","text":"partial"}}}}`,
			},
			verify: func(t *testing.T, events []llm.TextStreamEvent) {
				errorEvents := eventsOfType(events, llm.EventTypeError)
				require.Len(t, errorEvents, 1)
				err, ok := errorEvents[0].Value.(error)
				require.True(t, ok)
				assert.Contains(t, err.Error(), "stream ended unexpectedly")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(sseHandler(t, nil, tt.sseLines))
			defer server.Close()

			provider := newTestProvider(server.URL, "agent-id")
			stream, err := provider.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			})
			require.NoError(t, err)
			tt.verify(t, collectEvents(t, stream))
		})
	}
}

func TestChatCompletionNoStream(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantText   string
		wantErrSub string
	}{
		{
			name: "assistant text extracted from typed content",
			handler: func(w http.ResponseWriter, r *http.Request) {
				var captured ChatRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				assert.False(t, captured.Stream)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{
					"conversation_id": "c1",
					"finish_reason": "COMPLETE",
					"messages": [{
						"role": "assistant",
						"content": [
							{"type": "thinking", "thinking": "hmm"},
							{"type": "text", "text": "Hello"},
							{"type": "text", "text": " North"}
						]
					}]
				}`)
			},
			wantText: "Hello North",
		},
		{
			name: "string content tolerated",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"conversation_id":"c1","messages":[{"role":"assistant","content":"plain"}]}`)
			},
			wantText: "plain",
		},
		{
			name: "response-level error surfaces",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"conversation_id":"c1","messages":[],"error":{"error_code":"PROVIDER_API_ERROR","message":"model provider failed"}}`)
			},
			wantErrSub: "model provider failed",
		},
		{
			name: "structured HTTP error decoded",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error_type":"authentication_error","error_code":"INVALID_TOKEN","message":"Token expired.","request_id":"r1","status_code":401,"is_retryable":false}`)
			},
			wantErrSub: "Token expired.",
		},
		{
			name: "unstructured HTTP error reports status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, "gateway exploded")
			},
			wantErrSub: "HTTP 502",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			provider := newTestProvider(server.URL, "agent-id")
			text, err := provider.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			})
			if tt.wantErrSub != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSub)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantText, text)
		})
	}
}

func TestChatCompletionHTTPErrorReturnsDirectly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error_type":"authentication_error","error_code":"INVALID_TOKEN","message":"Token expired.","request_id":"r1","status_code":401,"is_retryable":false}`)
	}))
	defer server.Close()

	provider := newTestProvider(server.URL, "agent-id")
	_, err := provider.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Token expired.")
}

func TestCountTokensUnsupported(t *testing.T) {
	provider := newTestProvider("http://localhost", "agent-id")
	_, err := provider.CountTokens(context.Background(), llm.CompletionRequest{})
	assert.ErrorIs(t, err, llm.ErrUnsupportedTokenCount)
}

func TestTokenLimits(t *testing.T) {
	provider := newTestProvider("http://localhost", "agent-id")
	assert.Equal(t, 128000, provider.InputTokenLimit())
	assert.Equal(t, 4096, provider.OutputTokenLimit())
}

func TestAnnotationsFromCitations(t *testing.T) {
	tests := []struct {
		name      string
		fullText  string
		citations []Citation
		want      []llm.Annotation
	}{
		{
			name:     "citation located with UTF-16 indices",
			fullText: "héllo — Go 1.24 shipped",
			citations: []Citation{{
				Text: "Go 1.24",
				Sources: []CitationSource{{
					Type:     "document",
					Document: map[string]any{"url": "https://example.com", "title": "Release"},
				}},
			}},
			want: []llm.Annotation{{
				Type:       llm.AnnotationTypeURLCitation,
				StartIndex: 8, // "héllo — " is 8 UTF-16 code units
				EndIndex:   15,
				URL:        "https://example.com",
				Title:      "Release",
				CitedText:  "Go 1.24",
				Index:      1,
			}},
		},
		{
			name:     "citation without URL skipped",
			fullText: "some text",
			citations: []Citation{{
				Text:    "some text",
				Sources: []CitationSource{{Type: "document", Document: map[string]any{"title": "no url"}}},
			}},
			want: nil,
		},
		{
			name:     "cited text not present skipped",
			fullText: "different content",
			citations: []Citation{{
				Text:    "missing",
				Sources: []CitationSource{{Type: "document", Document: map[string]any{"url": "https://example.com"}}},
			}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, annotationsFromCitations(tt.fullText, tt.citations))
		})
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain api base", in: "https://host.example/api", want: "https://host.example/api"},
		{name: "trailing slash stripped", in: "https://host.example/api/", want: "https://host.example/api"},
		{name: "v1 suffix stripped", in: "https://host.example/api/v1", want: "https://host.example/api"},
		{name: "v1 with trailing slash stripped", in: "https://host.example/api/v1/", want: "https://host.example/api"},
		{name: "surrounding whitespace stripped", in: "  https://host.example/api ", want: "https://host.example/api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeBaseURL(tt.in))
		})
	}
}
