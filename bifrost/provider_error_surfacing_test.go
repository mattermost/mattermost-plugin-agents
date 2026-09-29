// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// TestProviderErrorDetailReachesCaller drives in-band stream errors through
// Bifrost and checks the caller sees the provider's message and code.
func TestProviderErrorDetailReachesCaller(t *testing.T) {
	const quotaMessage = "You exceeded your current quota, please check your plan and billing details."

	tests := []struct {
		name            string
		useResponsesAPI bool
		handler         http.HandlerFunc
		wantSubstrings  []string
		wantAbsent      []string
	}{
		{
			name:            "Responses API in-band SSE error event on HTTP 200",
			useResponsesAPI: true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
				fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"sequence_number\":1,\"error\":{\"type\":\"insufficient_quota\",\"code\":\"insufficient_quota\",\"message\":%q,\"param\":null}}\n\n", quotaMessage)
			},
			wantSubstrings: []string{"bifrost stream error", quotaMessage, "code=insufficient_quota", "provider=openai"},
		},
		{
			name:            "Responses API in-band SSE error event without a message still reports its type",
			useResponsesAPI: true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"sequence_number\":0,\"details\":\"upstream exploded\"}\n\n")
			},
			wantSubstrings: []string{"bifrost stream error", "type=error"},
			wantAbsent:     []string{"upstream exploded"},
		},
		{
			name:            "Chat Completions in-band SSE error object",
			useResponsesAPI: false,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"insufficient_quota\",\"param\":null,\"code\":\"insufficient_quota\"}}\n\n", quotaMessage)
			},
			wantSubstrings: []string{quotaMessage, "code=insufficient_quota"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			// Native OpenAI always uses the Responses API; OpenAI-compatible with
			// the toggle off is how the plugin reaches /v1/chat/completions.
			serviceType := llm.ServiceTypeOpenAICompatible
			if tt.useResponsesAPI {
				serviceType = llm.ServiceTypeOpenAI
			}
			service := llm.ServiceConfig{
				ID:              "svc",
				Type:            serviceType,
				APIKey:          "key",
				APIURL:          server.URL,
				DefaultModel:    "gpt-test",
				UseResponsesAPI: tt.useResponsesAPI,
			}
			bot := llm.BotConfig{ID: "bot-1", ServiceID: service.ID, DisableTools: true}

			llmInstance, err := NewFromServiceConfig(service, bot, nil)
			require.NoError(t, err)
			defer llmInstance.Shutdown()

			_, err = llmInstance.ChatCompletionNoStream(
				context.Background(),
				llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}}},
				llm.WithToolsDisabled(),
			)
			require.Error(t, err)
			for _, want := range tt.wantSubstrings {
				require.Contains(t, err.Error(), want)
			}
			for _, absent := range tt.wantAbsent {
				require.NotContains(t, err.Error(), absent)
			}
		})
	}
}
