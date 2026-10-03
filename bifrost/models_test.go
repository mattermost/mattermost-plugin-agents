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

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestConvertBifrostModels(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	strPtr := func(s string) *string { return &s }

	input := []schemas.Model{
		{
			ID:              "anthropic/claude-sonnet-4-5",
			Name:            strPtr("Claude Sonnet 4.5"),
			MaxInputTokens:  intPtr(200000),
			MaxOutputTokens: intPtr(8192),
			ContextLength:   intPtr(200000),
		},
		{
			// Cohere / Mistral / Groq publish ContextLength only; the converter
			// must use it as the InputTokenLimit so the UI can auto-fill.
			ID:            "cohere/command-r",
			ContextLength: intPtr(128000),
		},
		{
			// Provider gave us nothing — pointers stay nil.
			ID: "custom-model",
		},
	}

	got := convertBifrostModels(input)
	require.Len(t, got, 3)

	assert.Equal(t, "claude-sonnet-4-5", got[0].ID)
	assert.Equal(t, "Claude Sonnet 4.5", got[0].DisplayName)
	require.NotNil(t, got[0].InputTokenLimit)
	assert.Equal(t, 200000, *got[0].InputTokenLimit)
	require.NotNil(t, got[0].OutputTokenLimit)
	assert.Equal(t, 8192, *got[0].OutputTokenLimit)
	require.NotNil(t, got[0].ContextLength)
	assert.Equal(t, 200000, *got[0].ContextLength)

	assert.Equal(t, "command-r", got[1].ID)
	assert.Equal(t, "command-r", got[1].DisplayName)
	require.NotNil(t, got[1].InputTokenLimit, "InputTokenLimit must fall back to ContextLength")
	assert.Equal(t, 128000, *got[1].InputTokenLimit)
	assert.Nil(t, got[1].OutputTokenLimit, "MaxOutputTokens not provided → nil")
	require.NotNil(t, got[1].ContextLength)
	assert.Equal(t, 128000, *got[1].ContextLength)

	assert.Equal(t, "custom-model", got[2].ID)
	assert.Nil(t, got[2].InputTokenLimit)
	assert.Nil(t, got[2].OutputTokenLimit)
	assert.Nil(t, got[2].ContextLength)
}

func TestFetchNorthModels(t *testing.T) {
	const apiKey = "north-test-key"
	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name             string
		status           int
		pages            []map[string]any
		oversizedBody    bool
		wantIDs          []string
		wantDisplay      []string
		wantInput        *int
		wantOutput       *int
		wantErrSubstring string
		wantAuth         bool
		wantPageCount    int
	}{
		{
			name: "success with pagination",
			pages: []map[string]any{
				{
					"data": []map[string]any{
						{
							"id":             "ignored",
							"name":           "command-a",
							"display_name":   "Command A",
							"context_length": 128000,
							"max_tokens":     8192,
						},
					},
					"next_token": "page-2",
				},
				{
					"data": []map[string]any{
						{
							"name":         "command-b",
							"display_name": "",
						},
					},
					"next_token": nil,
				},
			},
			wantIDs:       []string{"command-a", "command-b"},
			wantDisplay:   []string{"Command A", "command-b"},
			wantInput:     intPtr(128000),
			wantOutput:    intPtr(8192),
			wantAuth:      true,
			wantPageCount: 2,
		},
		{
			name:             "404 error",
			status:           http.StatusNotFound,
			wantErrSubstring: "404",
			wantAuth:         true,
		},
		{
			name:             "oversized body",
			oversizedBody:    true,
			wantErrSubstring: fmt.Sprintf("exceeded %d bytes", northModelsMaxBodyBytes),
			wantAuth:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var page int
			var sawAuth string
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawAuth = r.Header.Get("Authorization")
				paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "application/json", r.Header.Get("Accept"))
				require.Equal(t, "/api/v2/models", r.URL.Path)

				if tt.status != 0 {
					w.WriteHeader(tt.status)
					_, _ = fmt.Fprintf(w, `{"error":%q}`, apiKey)
					return
				}
				if tt.oversizedBody {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(strings.Repeat("a", northModelsMaxBodyBytes+1)))
					return
				}

				idx := page
				page++
				if idx >= len(tt.pages) {
					http.Error(w, "unexpected extra page", http.StatusInternalServerError)
					return
				}
				if idx == 0 {
					assert.Empty(t, r.URL.Query().Get("next_token"))
				} else {
					assert.Equal(t, "page-2", r.URL.Query().Get("next_token"))
				}
				assert.Equal(t, "100", r.URL.Query().Get("limit"))
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(tt.pages[idx]))
			}))
			defer server.Close()

			models, err := FetchModelsForService(context.Background(), llm.ServiceConfig{
				Type:   llm.ServiceTypeNorth,
				APIKey: apiKey,
				APIURL: server.URL,
			})

			if tt.wantAuth {
				assert.Equal(t, "Bearer "+apiKey, sawAuth)
			}
			if tt.wantErrSubstring != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstring)
				assert.NotContains(t, err.Error(), apiKey)
				return
			}
			require.NoError(t, err)
			require.Len(t, models, len(tt.wantIDs))
			for i, id := range tt.wantIDs {
				assert.Equal(t, id, models[i].ID)
				assert.Equal(t, tt.wantDisplay[i], models[i].DisplayName)
			}
			if tt.wantInput != nil {
				require.NotNil(t, models[0].InputTokenLimit)
				assert.Equal(t, *tt.wantInput, *models[0].InputTokenLimit)
				require.NotNil(t, models[0].ContextLength)
				assert.Equal(t, *tt.wantInput, *models[0].ContextLength)
			}
			if tt.wantOutput != nil {
				require.NotNil(t, models[0].OutputTokenLimit)
				assert.Equal(t, *tt.wantOutput, *models[0].OutputTokenLimit)
			}
			if tt.wantPageCount > 0 {
				assert.Len(t, paths, tt.wantPageCount)
			}
		})
	}
}
