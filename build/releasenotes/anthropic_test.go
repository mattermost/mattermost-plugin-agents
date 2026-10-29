// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLLM(url string) *llmClient {
	c := newLLMClient("test-key", "test-model")
	c.BaseURL = url
	c.Backoff = time.Millisecond
	c.Effort = ""
	return c
}

func textResponse(w http.ResponseWriter, stop, text string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"stop_reason": stop,
		"content": []map[string]string{
			{"type": "thinking", "text": "ignored"},
			{"type": "text", "text": text},
		},
	})
}

func TestLLMComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, anthropicVersion, r.Header.Get("anthropic-version"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		var req map[string]any
		assert.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "test-model", req["model"])
		assert.Equal(t, "sys", req["system"])
		assert.EqualValues(t, 123, req["max_tokens"])
		assert.NotContains(t, req, "temperature", "newer models reject sampling parameters")
		assert.NotContains(t, req, "output_config")
		textResponse(w, "end_turn", "hello")
	}))
	defer srv.Close()

	out, err := newTestLLM(srv.URL).complete(t.Context(), "sys", []message{{Role: "user", Content: "hi"}}, 123)
	require.NoError(t, err)
	assert.Equal(t, "hello", out, "only text blocks are returned")
}

func TestLLMCompleteSendsEffortWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OutputConfig struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "medium", req.OutputConfig.Effort)
		textResponse(w, "end_turn", "ok")
	}))
	defer srv.Close()

	c := newTestLLM(srv.URL)
	c.Effort = "medium"
	_, err := c.complete(t.Context(), "", []message{{Role: "user", Content: "hi"}}, 10)
	require.NoError(t, err)
}

func TestLLMCompleteErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		handler   http.HandlerFunc
		wantErr   string
		wantCalls int32
	}{
		"bad request is not retried": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"bad model"}}`)
			},
			wantErr:   "invalid_request_error",
			wantCalls: 1,
		},
		"overload is retried then fails": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(529)
				fmt.Fprint(w, `{"error":{"type":"overloaded_error","message":"busy"}}`)
			},
			wantErr:   "overloaded_error",
			wantCalls: maxAttempts,
		},
		"truncated output": {
			handler:   func(w http.ResponseWriter, r *http.Request) { textResponse(w, "max_tokens", "partial") },
			wantErr:   "max_tokens",
			wantCalls: 1,
		},
		"refusal": {
			handler:   func(w http.ResponseWriter, r *http.Request) { textResponse(w, "refusal", "") },
			wantErr:   "refusal",
			wantCalls: 1,
		},
		"no text": {
			handler:   func(w http.ResponseWriter, r *http.Request) { textResponse(w, "end_turn", "  ") },
			wantErr:   "no text",
			wantCalls: 1,
		},
		"invalid json": {
			handler:   func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not json") },
			wantErr:   "decoding",
			wantCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				tc.handler(w, r)
			}))
			defer srv.Close()

			_, err := newTestLLM(srv.URL).complete(t.Context(), "", []message{{Role: "user", Content: "hi"}}, 10)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Equal(t, tc.wantCalls, calls.Load())
		})
	}
}
