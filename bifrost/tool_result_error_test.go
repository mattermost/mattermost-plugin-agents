// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// bodyRecorder keeps the last JSON request body a fake provider received.
type bodyRecorder struct {
	mu   sync.Mutex
	body map[string]any
}

func (r *bodyRecorder) handler(t *testing.T, status int, errBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(req.Body)
		if !assert.NoError(t, err) {
			return
		}
		var body map[string]any
		if assert.NoError(t, json.Unmarshal(raw, &body)) {
			r.mu.Lock()
			r.body = body
			r.mu.Unlock()
		}
		http.Error(w, errBody, status)
	}
}

func (r *bodyRecorder) last() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body
}

func objects(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// Failed, rejected, and never-run tool calls must reach the model flagged as
// errors where the wire supports it, and every service in a fallback chain
// must get a request it accepts.
func TestToolResultErrorFlagReachesEveryProviderInChain(t *testing.T) {
	posts := []llm.Post{
		{Role: llm.PostRoleSystem, Message: "system"},
		{Role: llm.PostRoleUser, Message: "look it up"},
		{Role: llm.PostRoleBot, ToolUse: []llm.ToolCall{
			{ID: "call_ok", Name: "lookup", Arguments: []byte(`{}`), Result: "found it", Status: llm.ToolCallStatusSuccess},
			{ID: "call_auto", Name: "lookup", Arguments: []byte(`{}`), Result: "found it", Status: llm.ToolCallStatusAutoApproved},
			{ID: "call_err", Name: "lookup", Arguments: []byte(`{}`), Result: "boom", Status: llm.ToolCallStatusError},
			{ID: "call_rej", Name: "lookup", Arguments: []byte(`{}`), Result: "rejected by user", Status: llm.ToolCallStatusRejected},
		}},
		{Role: llm.PostRoleUser, Message: "and now?"},
	}
	wantError := map[string]bool{"call_ok": false, "call_auto": false, "call_err": true, "call_rej": true}

	tests := []struct {
		name         string
		useResponses bool
		// assertOpenAI checks the OpenAI-wire primary's request body.
		assertOpenAI func(t *testing.T, body map[string]any)
	}{
		{
			name: "chat completions",
			assertOpenAI: func(t *testing.T, body map[string]any) {
				seen := 0
				for _, msg := range objects(body["messages"]) {
					if msg["role"] != "tool" {
						continue
					}
					seen++
					assert.NotContains(t, msg, "is_error", "the OpenAI wire has no is_error field")
				}
				assert.Equal(t, len(wantError), seen)
			},
		},
		{
			name:         "responses",
			useResponses: true,
			assertOpenAI: func(t *testing.T, body map[string]any) {
				seen := 0
				for _, item := range objects(body["input"]) {
					if item["type"] != "function_call_output" {
						continue
					}
					seen++
					callID, _ := item["call_id"].(string)
					assert.NotContains(t, item, "error", "OpenAI rejects input[N].error")
					if wantError[callID] {
						assert.Equal(t, "incomplete", item["status"], callID)
					} else {
						assert.NotContains(t, item, "status", callID)
					}
				}
				assert.Equal(t, len(wantError), seen)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var openAI, anthropic bodyRecorder
			openAIServer := httptest.NewServer(openAI.handler(t, http.StatusBadRequest, `{"error":{"message":"unavailable"}}`))
			defer openAIServer.Close()
			anthropicServer := httptest.NewServer(anthropic.handler(t, http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error"}}`))
			defer anthropicServer.Close()

			primary := llm.ServiceConfig{
				ID:                "primary",
				Type:              llm.ServiceTypeOpenAICompatible,
				APIKey:            "key",
				APIURL:            openAIServer.URL,
				DefaultModel:      "gpt-4o",
				UseResponsesAPI:   tc.useResponses,
				FallbackServiceID: "fallback",
			}
			fallback := llm.ServiceConfig{
				ID:           "fallback",
				Type:         llm.ServiceTypeAnthropic,
				APIKey:       "anthropic-key",
				APIURL:       anthropicServer.URL,
				DefaultModel: "claude-sonnet-4-20250514",
			}
			lm, err := NewFromServiceConfig(primary, llm.BotConfig{ID: "bot", ServiceID: primary.ID}, []llm.ServiceConfig{fallback})
			require.NoError(t, err)
			defer lm.Shutdown()

			_, _ = lm.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{Posts: posts})

			openAIBody := openAI.last()
			require.NotNil(t, openAIBody, "the primary must be called")
			tc.assertOpenAI(t, openAIBody)

			anthropicBody := anthropic.last()
			require.NotNil(t, anthropicBody, "the Anthropic fallback must be called")
			seen := 0
			for _, msg := range objects(anthropicBody["messages"]) {
				for _, block := range objects(msg["content"]) {
					if block["type"] != "tool_result" {
						continue
					}
					seen++
					id, _ := block["tool_use_id"].(string)
					isError, _ := block["is_error"].(bool)
					assert.Equal(t, wantError[id], isError, id)
				}
			}
			assert.Equal(t, len(wantError), seen)
		})
	}
}
