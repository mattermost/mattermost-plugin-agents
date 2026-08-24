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
	"sync/atomic"
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
			for i := range tt.wantFinal {
				tt.wantFinal[i].ProviderRoute = string(schemas.Anthropic)
			}
			assert.Equal(t, tt.wantFinal, final,
				"the final snapshot must carry every tool of the round in arrival order")
		})
	}
}

// TestStreamResponsesCapturesFallbackFileRoute verifies that the runtime-only
// route stored beside a provider file id identifies the account that actually
// served a failed-over completion.
func TestStreamResponsesCapturesFallbackFileRoute(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"overloaded_error"}}`, http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAnthropicSSE(w, []string{
			`{"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_fallback","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_fallback","name":"bash_code_execution","input":{"command":"echo output"}}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"bash_code_execution_tool_result","tool_use_id":"srvtoolu_fallback","content":{"type":"bash_code_execution_result","stdout":"","stderr":"","return_code":0,"content":[{"type":"bash_code_execution_output","file_id":"file_fallback"}]}}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
			`{"type":"message_stop"}`,
		})
	}))
	defer fallback.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider: schemas.Anthropic, APIKey: "primary-key", APIURL: primary.URL,
			DefaultModel: "claude-sonnet-4-6", StreamingTimeout: 10 * time.Second,
		},
		Fallbacks: []FallbackEntry{{
			ID: "backup",
			ProviderSettings: ProviderSettings{
				Provider: schemas.Anthropic, APIKey: "fallback-key", APIURL: fallback.URL,
				DefaultModel: "claude-sonnet-4-6", StreamingTimeout: 10 * time.Second,
			},
		}},
		EnabledNativeTools: []string{llm.NativeToolCodeInterpreter},
	})
	require.NoError(t, err)
	defer llmClient.Shutdown()

	result, err := llmClient.ChatCompletion(context.Background(), llm.CompletionRequest{
		Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "make a file"}},
	})
	require.NoError(t, err)

	var final []llm.ServerToolUse
	for event := range result.Stream {
		if event.Type == llm.EventTypeServerToolUse {
			final = event.Value.([]llm.ServerToolUse)
		}
		if event.Type == llm.EventTypeError {
			t.Fatalf("unexpected stream error: %v", event.Value)
		}
	}

	require.Len(t, final, 1)
	require.Equal(t, []string{"file_fallback"}, final[0].FileIDs)
	require.Equal(t, string(schemas.Anthropic)+"::backup", final[0].ProviderRoute)
}

func flatten(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// TestProviderServices pins which capabilities each provider hands to the
// tools layer. This is resolved on the concrete client because the bot's
// LanguageModel is a decorator chain that hides it.
func TestProviderServices(t *testing.T) {
	tests := []struct {
		name             string
		provider         schemas.ModelProvider
		wantFileDownload bool
	}{
		{name: "anthropic serves file content", provider: schemas.Anthropic, wantFileDownload: true},
		{name: "openai has no usable file retrieval yet", provider: schemas.OpenAI, wantFileDownload: false},
		{name: "gemini has no file retrieval", provider: schemas.Gemini, wantFileDownload: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmClient, err := New(Config{
				ProviderSettings: ProviderSettings{
					Provider:         tt.provider,
					APIKey:           "test-key",
					DefaultModel:     "test-model",
					StreamingTimeout: 10 * time.Second,
				},
			})
			require.NoError(t, err)
			defer llmClient.Shutdown()

			services := llmClient.ProviderServices()
			require.NotNil(t, services)
			assert.Equal(t, tt.wantFileDownload, services.CanDownloadFiles())
			if tt.wantFileDownload {
				assert.Same(t, llmClient, services.FileDownloader)
			}
		})
	}
}

// TestDownloadProviderFile drives the real Bifrost client against a mock
// Anthropic Files API and asserts the content, MIME type, auth header and
// URL path — the contract AttachSandboxFile depends on.
func TestDownloadProviderFile(t *testing.T) {
	fileBytes := []byte("col1,col2\n1,2\n")
	var gotPath, gotAPIKey string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		switch r.URL.Path {
		case "/v1/files/file_011abc":
			// Metadata: the only place the sandbox's file name is available.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file_011abc","type":"file","filename":"results.csv","size_bytes":14,"mime_type":"text/csv","created_at":"2026-01-01T00:00:00Z","downloadable":true}`))
		case "/v1/files/file_011abc/content":
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write(fileBytes)
		default:
			http.Error(w, `{"type":"error","error":{"type":"not_found_error","message":"file not found"}}`, http.StatusNotFound)
		}
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
	})
	require.NoError(t, err)
	defer llmClient.Shutdown()

	// Tools reach the downloader through ProviderServices, so exercise it the
	// same way rather than using the concrete client directly.
	downloader := llmClient.ProviderServices().FileDownloader
	require.NotNil(t, downloader, "an Anthropic client must expose file download")

	tests := []struct {
		name    string
		ref     llm.ProviderFileReference
		wantErr bool
		verify  func(t *testing.T, file llm.ProviderFile)
	}{
		{
			name: "successful download returns metadata and content",
			ref:  llm.ProviderFileReference{ID: "file_011abc", ProviderRoute: string(schemas.Anthropic)},
			verify: func(t *testing.T, file llm.ProviderFile) {
				assert.Equal(t, fileBytes, file.Content)
				assert.Equal(t, "text/csv", file.ContentType)
				assert.Equal(t, "results.csv", file.Name, "the sandbox's own file name must survive to the caller")
				assert.Equal(t, "/v1/files/file_011abc/content", gotPath)
				assert.Equal(t, "test-key", gotAPIKey, "download must use the service credentials")
			},
		},
		{
			name:    "provider failure is returned",
			ref:     llm.ProviderFileReference{ID: "file_missing", ProviderRoute: string(schemas.Anthropic)},
			wantErr: true,
		},
		{
			name:    "empty file id is rejected",
			ref:     llm.ProviderFileReference{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, downloadErr := downloader.DownloadProviderFile(context.Background(), tt.ref)
			if tt.wantErr {
				require.Error(t, downloadErr)
				return
			}
			require.NoError(t, downloadErr)
			tt.verify(t, file)
		})
	}
}

// TestDownloadProviderFileUsesCapturedFallbackRoute verifies that provider
// file references do not silently switch back to primary credentials after a
// completion was served by a fallback account.
func TestDownloadProviderFileUsesCapturedFallbackRoute(t *testing.T) {
	var primaryRequests int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&primaryRequests, 1)
		http.Error(w, "wrong provider route", http.StatusUnauthorized)
	}))
	defer primary.Close()

	var fallbackAPIKey string
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAPIKey = r.Header.Get("x-api-key")
		switch r.URL.Path {
		case "/v1/files/file_fallback":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file_fallback","type":"file","filename":"fallback.txt","size_bytes":8,"mime_type":"text/plain","created_at":"2026-01-01T00:00:00Z","downloadable":true}`))
		case "/v1/files/file_fallback/content":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("fallback"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fallback.Close()

	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider: schemas.Anthropic, APIKey: "primary-key", APIURL: primary.URL,
			DefaultModel: "claude-sonnet-4-6", StreamingTimeout: 10 * time.Second,
		},
		Fallbacks: []FallbackEntry{{
			ID: "backup",
			ProviderSettings: ProviderSettings{
				Provider: schemas.Anthropic, APIKey: "fallback-key", APIURL: fallback.URL,
				DefaultModel: "claude-sonnet-4-6", StreamingTimeout: 10 * time.Second,
			},
		}},
	})
	require.NoError(t, err)
	defer llmClient.Shutdown()

	file, err := llmClient.DownloadProviderFile(context.Background(), llm.ProviderFileReference{
		ID:            "file_fallback",
		ProviderRoute: string(schemas.Anthropic) + "::backup",
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback.txt", file.Name)
	assert.Equal(t, []byte("fallback"), file.Content)
	assert.Equal(t, "fallback-key", fallbackAPIKey)
	assert.Zero(t, atomic.LoadInt32(&primaryRequests), "primary credentials must not be used for a fallback-owned file")
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

// TestCodeExecutionRequestCarriesFilesAPIBeta pins the header that makes sandbox
// output files reachable at all: Anthropic reports the file ids a code-execution
// result produced only when the completion request opts into the Files API beta.
// Without it the results carry an empty file list, the observed-id allowlist stays
// empty, and every AttachSandboxFile call is rejected as an unobserved id.
func TestCodeExecutionRequestCarriesFilesAPIBeta(t *testing.T) {
	tests := []struct {
		name        string
		nativeTools []string
		wantBeta    bool
	}{
		{
			name:        "code execution enabled",
			nativeTools: []string{llm.NativeToolCodeInterpreter},
			wantBeta:    true,
		},
		{
			name:        "code execution not enabled",
			nativeTools: []string{llm.NativeToolWebSearch},
			wantBeta:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBeta atomic.Value // string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBeta.Store(r.Header.Get("anthropic-beta"))
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer backend.Close()

			service := llm.ServiceConfig{
				ID:           "anthropic-svc",
				Type:         llm.ServiceTypeAnthropic,
				APIKey:       "test-key",
				APIURL:       backend.URL,
				DefaultModel: "claude-sonnet-4-6",
			}
			botCfg := llm.BotConfig{ID: "bot-1", ServiceID: service.ID, EnabledNativeTools: tt.nativeTools}

			llmClient, err := NewFromServiceConfig(service, botCfg, nil)
			require.NoError(t, err)
			defer llmClient.Shutdown()

			stream, err := llmClient.ChatCompletion(
				context.Background(),
				llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "make a file"}}},
			)
			require.NoError(t, err)
			for range stream.Stream { //nolint:revive // drain so the request completes
			}

			beta, _ := gotBeta.Load().(string)
			if tt.wantBeta {
				assert.Contains(t, beta, "files-api-2025-04-14",
					"code-execution requests must opt into the Files API beta or results carry no file ids")
			} else {
				assert.NotContains(t, beta, "files-api-2025-04-14")
			}
		})
	}
}
