// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// writeAnthropicSSE writes raw Anthropic Messages API stream events as
// server-sent events. Each element is the JSON data payload; the SSE event
// name is read from its top-level "type" field the same way Anthropic emits
// it. Parsed with encoding/json so nested objects carrying their own "type"
// fields can't produce the wrong event name.
func writeAnthropicSSE(w http.ResponseWriter, events []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, data := range events {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			panic(fmt.Sprintf("invalid SSE fixture %q: %v", data, err))
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.Type, data)
	}
}

// TestStreamResponsesEmitsServerToolActivity drives recorded Anthropic
// Messages SSE (server_tool_use blocks and their result blocks, faithful to
// the wire format) through the real Bifrost client and asserts the plugin
// surfaces the activity as EventTypeServerToolUse events: an in_progress
// snapshot when the tool starts and a final snapshot carrying the interpreted
// result (query / URL+title / command+output / error), without disturbing the
// text stream.
func TestStreamResponsesEmitsServerToolActivity(t *testing.T) {
	messageStart := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`
	messageEnd := []string{
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40}}`,
		`{"type":"message_stop"}`,
	}
	textBlock := func(index int, text string) []string {
		return []string{
			fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index),
			fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}`, index, text),
			fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index),
		}
	}

	tests := []struct {
		name               string
		enabledNativeTools []string
		events             []string
		wantText           string
		wantFinal          []llm.ServerToolUse
	}{
		{
			name:               "bash code execution",
			enabledNativeTools: []string{llm.NativeToolCodeInterpreter},
			events: flatten(
				[]string{messageStart},
				textBlock(0, "Let me calculate."),
				[]string{
					`{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_bash","name":"bash_code_execution","input":{}}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"py"}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"thon3 -c statistics\"}"}}`,
					`{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"bash_code_execution_tool_result","tool_use_id":"srvtoolu_bash","content":{"type":"bash_code_execution_result","stdout":"Mean: 5.5\n","stderr":"","return_code":0,"content":[{"type":"bash_code_execution_output","file_id":"file_011abc"}]}}}`,
					`{"type":"content_block_stop","index":2}`,
				},
				textBlock(3, "The mean is 5.5."),
				messageEnd,
			),
			wantText: "Let me calculate.The mean is 5.5.",
			wantFinal: []llm.ServerToolUse{{
				ID:      "srvtoolu_bash",
				Tool:    llm.NativeToolCodeInterpreter,
				Status:  llm.ServerToolStatusSuccess,
				SubTool: "bash",
				Command: "python3 -c statistics",
				Output:  "Mean: 5.5\n",
				FileIDs: []string{"file_011abc"},
			}},
		},
		{
			name:               "bash code execution error",
			enabledNativeTools: []string{llm.NativeToolCodeInterpreter},
			events: flatten(
				[]string{messageStart},
				[]string{
					`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_err","name":"bash_code_execution","input":{}}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"boom\"}"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"bash_code_execution_tool_result","tool_use_id":"srvtoolu_err","content":{"type":"bash_code_execution_tool_result_error","error_code":"unavailable"}}}`,
					`{"type":"content_block_stop","index":1}`,
				},
				textBlock(2, "The sandbox is unavailable."),
				messageEnd,
			),
			wantText: "The sandbox is unavailable.",
			wantFinal: []llm.ServerToolUse{{
				ID:        "srvtoolu_err",
				Tool:      llm.NativeToolCodeInterpreter,
				Status:    llm.ServerToolStatusError,
				SubTool:   "bash",
				Command:   "boom",
				ErrorCode: "unavailable",
			}},
		},
		{
			name:               "web search",
			enabledNativeTools: []string{llm.NativeToolWebSearch},
			events: flatten(
				[]string{messageStart},
				[]string{
					`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_ws","name":"web_search","input":{"query":"latest mattermost release"}}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_ws","content":[{"type":"web_search_result","title":"Mattermost releases","url":"https://example.com/releases","encrypted_content":"enc1"}]}}`,
					`{"type":"content_block_stop","index":1}`,
				},
				textBlock(2, "Found it."),
				messageEnd,
			),
			wantText: "Found it.",
			wantFinal: []llm.ServerToolUse{{
				ID:     "srvtoolu_ws",
				Tool:   llm.NativeToolWebSearch,
				Status: llm.ServerToolStatusSuccess,
				Query:  "latest mattermost release",
			}},
		},
		{
			name:               "web fetch",
			enabledNativeTools: []string{llm.NativeToolWebFetch},
			events: flatten(
				[]string{messageStart},
				[]string{
					`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_wf","name":"web_fetch","input":{"url":"https://example.com/doc"}}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_wf","content":{"type":"web_fetch_result","url":"https://example.com/doc","retrieved_at":"2026-07-30T12:00:00Z","content":{"type":"document","title":"Example Doc","source":{"type":"text","media_type":"text/plain","data":"body"},"citations":{"enabled":false}}}}}`,
					`{"type":"content_block_stop","index":1}`,
				},
				textBlock(2, "Fetched."),
				messageEnd,
			),
			wantText: "Fetched.",
			wantFinal: []llm.ServerToolUse{{
				ID:     "srvtoolu_wf",
				Tool:   llm.NativeToolWebFetch,
				Status: llm.ServerToolStatusSuccess,
				URL:    "https://example.com/doc",
				Title:  "Example Doc",
			}},
		},
		{
			// The snapshot is cumulative: a second tool in the same response
			// must append to the activity, not replace the earlier entry.
			name:               "web search then web fetch accumulate in order",
			enabledNativeTools: []string{llm.NativeToolWebSearch, llm.NativeToolWebFetch},
			events: flatten(
				[]string{messageStart},
				[]string{
					`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_ws2","name":"web_search","input":{"query":"release notes url"}}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_ws2","content":[{"type":"web_search_result","title":"Docs","url":"https://example.com/docs","encrypted_content":"enc2"}]}}`,
					`{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"server_tool_use","id":"srvtoolu_wf2","name":"web_fetch","input":{"url":"https://example.com/docs"}}}`,
					`{"type":"content_block_stop","index":2}`,
					`{"type":"content_block_start","index":3,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_wf2","content":{"type":"web_fetch_result","url":"https://example.com/docs","retrieved_at":"2026-07-30T12:00:00Z","content":{"type":"document","title":"Docs","source":{"type":"text","media_type":"text/plain","data":"body"},"citations":{"enabled":false}}}}}`,
					`{"type":"content_block_stop","index":3}`,
				},
				textBlock(4, "Both done."),
				messageEnd,
			),
			wantText: "Both done.",
			wantFinal: []llm.ServerToolUse{
				{
					ID:     "srvtoolu_ws2",
					Tool:   llm.NativeToolWebSearch,
					Status: llm.ServerToolStatusSuccess,
					Query:  "release notes url",
				},
				{
					ID:     "srvtoolu_wf2",
					Tool:   llm.NativeToolWebFetch,
					Status: llm.ServerToolStatusSuccess,
					URL:    "https://example.com/docs",
					Title:  "Docs",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeAnthropicSSE(w, tt.events)
			}))
			defer backend.Close()

			llmClient, err := New(Config{
				ProviderSettings: ProviderSettings{
					Provider:         schemas.Anthropic,
					APIKey:           "test-key",
					APIURL:           backend.URL,
					DefaultModel:     "claude-sonnet-4-6",
					StreamingTimeout: 10 * time.Second,
				},
				EnabledNativeTools: tt.enabledNativeTools,
			})
			require.NoError(t, err)
			defer llmClient.Shutdown()

			result, err := llmClient.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
			})
			require.NoError(t, err)

			var text strings.Builder
			var snapshots [][]llm.ServerToolUse
			sawEnd := false
			for event := range result.Stream {
				switch event.Type {
				case llm.EventTypeText:
					text.WriteString(event.Value.(string))
				case llm.EventTypeServerToolUse:
					uses, ok := event.Value.([]llm.ServerToolUse)
					require.True(t, ok, "EventTypeServerToolUse value must be []llm.ServerToolUse")
					snapshots = append(snapshots, uses)
				case llm.EventTypeError:
					t.Fatalf("unexpected stream error: %v", event.Value)
				case llm.EventTypeEnd:
					sawEnd = true
				}
			}

			assert.True(t, sawEnd, "stream must end cleanly")
			assert.Equal(t, tt.wantText, text.String(), "server tool handling must not disturb the text stream")

			require.NotEmpty(t, snapshots, "server tool activity must be surfaced")
			first := snapshots[0]
			require.NotEmpty(t, first)
			assert.Equal(t, llm.ServerToolStatusInProgress, first[0].Status,
				"the first snapshot arrives when the tool starts, before the result")

			final := snapshots[len(snapshots)-1]
			assert.Equal(t, tt.wantFinal, final,
				"the final snapshot must carry every tool of the round in arrival order")
		})
	}
}

func flatten(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// TestMapServerToolStatus pins the item-status mapping. "incomplete" (e.g. an
// OpenAI code_interpreter_call cut off by max tokens) must be terminal: mapping
// it to in-progress leaves a spinner in the UI after the stream ends.
func TestMapServerToolStatus(t *testing.T) {
	tests := []struct {
		name   string
		status *string
		want   string
	}{
		{"nil defaults to in progress", nil, llm.ServerToolStatusInProgress},
		{"in_progress stays in progress", Ptr("in_progress"), llm.ServerToolStatusInProgress},
		{"completed maps to success", Ptr("completed"), llm.ServerToolStatusSuccess},
		{"failed maps to error", Ptr("failed"), llm.ServerToolStatusError},
		{"incomplete is terminal error", Ptr("incomplete"), llm.ServerToolStatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mapServerToolStatus(tt.status))
		})
	}
}
