// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validateAutomationTriggerForTest mimics channel-automation plugin validation for triggers.
func validateAutomationTriggerForTest(tr AutomationTrigger) string {
	n := 0
	if tr.MessagePosted != nil {
		n++
	}
	if tr.Schedule != nil {
		n++
	}
	if tr.MembershipChanged != nil {
		n++
	}
	if tr.ChannelCreated != nil {
		n++
	}
	if tr.UserJoinedTeam != nil {
		n++
	}
	if n == 0 {
		return "trigger is required"
	}
	if n > 1 {
		return "exactly one type set"
	}
	return ""
}

// newTestAutomationServer creates an httptest server that mimics the channel-automation plugin API.
func newTestAutomationServer(t *testing.T, automations []Automation) *httptest.Server {
	t.Helper()

	automationMap := make(map[string]Automation)
	for _, a := range automations {
		automationMap[a.ID] = a
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/plugins/com.mattermost.channel-automation/api/v1/automations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			all := make([]Automation, 0, len(automationMap))
			filterChID := r.URL.Query().Get("channel_id")
			for _, a := range automationMap {
				if filterChID != "" && triggerChannelID(a.Trigger) != filterChID {
					continue
				}
				all = append(all, a)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(all)

		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var automation Automation
			if err := json.Unmarshal(body, &automation); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if msg := validateAutomationTriggerForTest(automation.Trigger); msg != "" {
				http.Error(w, msg, http.StatusBadRequest)
				return
			}
			automation.ID = "new-automation-id"
			automationMap[automation.ID] = automation
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(automation)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/plugins/com.mattermost.channel-automation/api/v1/automations/", func(w http.ResponseWriter, r *http.Request) {
		// Extract ID from path: /plugins/.../automations/{id}
		id := r.URL.Path[len("/plugins/com.mattermost.channel-automation/api/v1/automations/"):]

		switch r.Method {
		case http.MethodGet:
			automation, ok := automationMap[id]
			if !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(automation)

		case http.MethodPut:
			if _, ok := automationMap[id]; !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var automation Automation
			if err := json.Unmarshal(body, &automation); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			automation.ID = id
			automationMap[id] = automation
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(automation)

		case http.MethodDelete:
			if _, ok := automationMap[id]; !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			delete(automationMap, id)
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/plugins/com.mattermost.channel-automation/api/v1/automation-instructions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload := automationInstructionsAPIResponse{
			Instructions: "Channel automations are trigger-action workflows.\n\nTRIGGERS:\n- message_posted\n\nACTION SELECTION:\n- send_message",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})

	// Mattermost API v4 endpoint stubs needed by Client4
	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "test-user-id"})
	})

	return httptest.NewServer(mux)
}

func TestAutomationListAutomations(t *testing.T) {
	id1 := model.NewId()
	id2 := model.NewId()
	chID1 := model.NewId()
	chID2 := model.NewId()
	sample := []Automation{
		{
			ID:      id1,
			Name:    "Welcome Bot",
			Enabled: true,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: chID1, IncludeThreadReplies: true}},
			Actions: []AutomationAction{{ID: "summarize", AIPrompt: &AIPromptActionConfig{
				Prompt:       "Summarize",
				ProviderType: "agent",
				ProviderID:   "bot1",
				AllowedTools: []string{"search_posts"},
				Guardrails:   &AutomationGuardrails{ChannelIDs: []string{chID1}},
			}}},
		},
		{
			ID:      id2,
			Name:    "Bug Triage",
			Enabled: false,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: chID2}},
			Actions: []AutomationAction{{ID: "summarize", AIPrompt: &AIPromptActionConfig{Prompt: "Summarize", ProviderType: "agent", ProviderID: "bot1"}}},
		},
	}

	ts := newTestAutomationServer(t, sample)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

	t.Run("list all", func(t *testing.T) {
		result, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{})
		require.NoError(t, err)
		assert.Contains(t, result, "Welcome Bot")
		assert.Contains(t, result, "Bug Triage")
		assert.Contains(t, result, `"include_thread_replies": true`)
		assert.Contains(t, result, `"guardrails": {`)
		assert.Contains(t, result, `"channel_ids": [`)
		assert.Contains(t, result, chID1)
	})

	t.Run("get by id", func(t *testing.T) {
		result, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{AutomationID: id1})
		require.NoError(t, err)
		assert.Contains(t, result, "Welcome Bot")
		assert.NotContains(t, result, "Bug Triage")
		assert.Contains(t, result, `"include_thread_replies": true`)
		assert.Contains(t, result, `"guardrails": {`)
		assert.Contains(t, result, chID1)
	})

	t.Run("filter by channel_id", func(t *testing.T) {
		result, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{ChannelID: chID2})
		require.NoError(t, err)
		assert.Contains(t, result, "Bug Triage")
		assert.NotContains(t, result, "Welcome Bot")
	})

	t.Run("get by id not found", func(t *testing.T) {
		missingID := model.NewId()
		_, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{AutomationID: missingID})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation not found")
	})

	t.Run("get by invalid id", func(t *testing.T) {
		_, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{AutomationID: "bad-id"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation_id must be a valid ID")
	})
}

func TestGetAutomationInstructions(t *testing.T) {
	ts := newTestAutomationServer(t, nil)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{
		Ctx:    context.Background(),
		Client: client,
	}
	result, err := provider.toolGetAutomationInstructions(mcpCtx, struct{}{})
	require.NoError(t, err)
	assert.Contains(t, result, "TRIGGERS:")
	assert.Contains(t, result, "ACTION SELECTION:")
}

func TestAutomationCreate(t *testing.T) {
	ts := newTestAutomationServer(t, nil)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

	t.Run("create with message_posted trigger", func(t *testing.T) {
		args := CreateAutomationArgs{
			Name:    "Test Automation",
			Enabled: true,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "abcdefghijklmnopqrstuvwxyz", IncludeThreadReplies: true}},
			Actions: []AutomationAction{{ID: "prompt", AIPrompt: &AIPromptActionConfig{
				Prompt:       "Hello!",
				ProviderType: "agent",
				ProviderID:   "bot1",
				AllowedTools: []string{"search_posts"},
				Guardrails:   &AutomationGuardrails{ChannelIDs: []string{"abcdefghijklmnopqrstuvwxyz"}},
			}}},
		}

		result, err := provider.toolCreateAutomation(mcpCtx, args)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully created automation")
		assert.Contains(t, result, "Test Automation")
		assert.Contains(t, result, "new-automation-id")
		assert.Contains(t, result, `"include_thread_replies": true`)
		assert.Contains(t, result, `"guardrails": {`)
		assert.Contains(t, result, `"channel_ids": [`)
	})

	t.Run("create missing name", func(t *testing.T) {
		args := CreateAutomationArgs{
			Name:    "",
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "abcdefghijklmnopqrstuvwxyz"}},
		}

		_, err := provider.toolCreateAutomation(mcpCtx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name cannot be empty")
	})

	t.Run("create missing trigger", func(t *testing.T) {
		args := CreateAutomationArgs{
			Name:    "Test",
			Trigger: AutomationTrigger{},
		}

		_, err := provider.toolCreateAutomation(mcpCtx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trigger is required")
	})

	t.Run("create multiple triggers", func(t *testing.T) {
		args := CreateAutomationArgs{
			Name: "Test",
			Trigger: AutomationTrigger{
				MessagePosted: &MessagePostedConfig{ChannelID: "ch1"},
				Schedule:      &ScheduleConfig{ChannelID: "ch1", Interval: "daily"},
			},
		}

		_, err := provider.toolCreateAutomation(mcpCtx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one type set")
	})
}

func TestAutomationUpdate(t *testing.T) {
	id := model.NewId()
	chID := model.NewId()
	sample := []Automation{
		{ID: id, Name: "Original", Enabled: true, Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: chID}}},
	}

	ts := newTestAutomationServer(t, sample)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

	t.Run("update success", func(t *testing.T) {
		args := UpdateAutomationArgs{
			AutomationID: id,
			Name:         "Updated Name",
			Enabled:      false,
			Trigger:      AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "abcdefghijklmnopqrstuvwxyz", IncludeThreadReplies: true}},
			Actions: []AutomationAction{{ID: "prompt", AIPrompt: &AIPromptActionConfig{
				Prompt:       "Hello!",
				ProviderType: "agent",
				ProviderID:   "bot1",
				AllowedTools: []string{"search_posts"},
				Guardrails:   &AutomationGuardrails{ChannelIDs: []string{chID}},
			}}},
		}

		result, err := provider.toolUpdateAutomation(mcpCtx, args)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully updated automation")
		assert.Contains(t, result, "Updated Name")
		assert.Contains(t, result, `"include_thread_replies": true`)
		assert.Contains(t, result, `"guardrails": {`)
		assert.Contains(t, result, chID)
	})

	t.Run("update not found", func(t *testing.T) {
		missingID := model.NewId()
		args := UpdateAutomationArgs{
			AutomationID: missingID,
			Name:         "X",
			Trigger:      AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "abcdefghijklmnopqrstuvwxyz"}},
		}

		_, err := provider.toolUpdateAutomation(mcpCtx, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation not found")
	})

	t.Run("update invalid automation_id", func(t *testing.T) {
		_, err := provider.toolUpdateAutomation(mcpCtx, UpdateAutomationArgs{Name: "X"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation_id must be a valid ID")
	})
}

func TestAutomationDelete(t *testing.T) {
	id := model.NewId()
	sample := []Automation{
		{ID: id, Name: "To Delete", Enabled: true},
	}

	ts := newTestAutomationServer(t, sample)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

	t.Run("delete success", func(t *testing.T) {
		result, err := provider.toolDeleteAutomation(mcpCtx, DeleteAutomationArgs{AutomationID: id})
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully deleted automation")
		assert.Contains(t, result, id)
	})

	t.Run("delete not found", func(t *testing.T) {
		missingID := model.NewId()
		_, err := provider.toolDeleteAutomation(mcpCtx, DeleteAutomationArgs{AutomationID: missingID})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation not found")
	})

	t.Run("delete invalid automation_id", func(t *testing.T) {
		_, err := provider.toolDeleteAutomation(mcpCtx, DeleteAutomationArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation_id must be a valid ID")
	})
}

func TestAutomationErrorHandling(t *testing.T) {
	t.Run("403 forbidden", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v4/users/me" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "test-user-id"})
				return
			}
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		}))
		defer ts.Close()

		provider := newTestProvider(t, ts.URL)
		client := newTestClient(ts.URL)
		mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

		_, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission")
	})

	t.Run("connection error", func(t *testing.T) {
		// Use an unreachable URL
		provider := newTestProvider(t, "http://127.0.0.1:1")
		client := newTestClient("http://127.0.0.1:1")
		mcpCtx := &MCPToolContext{Ctx: context.Background(), Client: client}

		_, err := provider.toolListAutomations(mcpCtx, ListAutomationsArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not installed or not reachable")
	})
}

func TestAutomationPluginInstalled(t *testing.T) {
	t.Run("plugin installed returns true", func(t *testing.T) {
		ts := newTestAutomationServer(t, nil)
		defer ts.Close()

		provider := newTestProvider(t, ts.URL)
		assert.True(t, provider.isAutomationPluginInstalled())
	})

	t.Run("plugin not installed returns false", func(t *testing.T) {
		// Server that 404s on plugin routes
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer ts.Close()

		provider := newTestProvider(t, ts.URL)
		assert.False(t, provider.isAutomationPluginInstalled())
	})

	t.Run("server unreachable returns false", func(t *testing.T) {
		provider := newTestProvider(t, "http://127.0.0.1:1")
		assert.False(t, provider.isAutomationPluginInstalled())
	})

	t.Run("plugin returns 401 still counts as installed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer ts.Close()

		provider := newTestProvider(t, ts.URL)
		assert.True(t, provider.isAutomationPluginInstalled())
	})
}

func TestHandleAutomationHTTPError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		body           string
		automationID   string
		expectedResult string
	}{
		{
			name:           "400 bad request with body",
			statusCode:     http.StatusBadRequest,
			body:           "invalid trigger configuration",
			expectedResult: "bad request: invalid trigger configuration",
		},
		{
			name:           "400 bad request empty body falls back to error",
			statusCode:     http.StatusBadRequest,
			body:           "",
			expectedResult: "bad request: test error",
		},
		{
			name:           "401 unauthorized",
			statusCode:     http.StatusUnauthorized,
			automationID:   "",
			expectedResult: "you don't have permission to manage automations for this channel",
		},
		{
			name:           "403 forbidden",
			statusCode:     http.StatusForbidden,
			automationID:   "",
			expectedResult: "you don't have permission to manage automations for this channel",
		},
		{
			name:           "404 with automation id",
			statusCode:     http.StatusNotFound,
			automationID:   "abc123",
			expectedResult: `automation not found with ID "abc123"`,
		},
		{
			name:           "404 without automation id",
			statusCode:     http.StatusNotFound,
			automationID:   "",
			expectedResult: "not installed or not reachable",
		},
		{
			name:           "500 server error",
			statusCode:     http.StatusInternalServerError,
			automationID:   "",
			expectedResult: "automation API returned status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var respBody io.ReadCloser
			if tt.body != "" {
				respBody = io.NopCloser(strings.NewReader(tt.body))
			} else {
				respBody = http.NoBody
			}
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       respBody,
			}

			err := handleAutomationHTTPError(resp, fmt.Errorf("test error"), tt.automationID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedResult)
		})
	}

	t.Run("nil response (connection error)", func(t *testing.T) {
		err := handleAutomationHTTPError(nil, fmt.Errorf("connection refused"), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not installed or not reachable")
	})

	t.Run("400 with nil error and empty body", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       http.NoBody,
		}
		err := handleAutomationHTTPError(resp, nil, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad request: invalid request")
	})
}

func TestAutomationErrorDetail(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "AppError uses Message field",
			err:      model.NewAppError("test", "schedule trigger start_at must be a future UTC timestamp", nil, "", http.StatusBadRequest),
			expected: "schedule trigger start_at must be a future UTC timestamp",
		},
		{
			name:     "plain error passes through",
			err:      fmt.Errorf("connection refused"),
			expected: "connection refused",
		},
		{
			name:     "wrapped non-JSON body error passes through",
			err:      fmt.Errorf("failed to decode JSON payload into AppError. Body: some validation error : invalid character 's' looking for beginning of value"),
			expected: "failed to decode JSON payload into AppError. Body: some validation error : invalid character 's' looking for beginning of value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, automationErrorDetail(tt.err))
		})
	}
}
