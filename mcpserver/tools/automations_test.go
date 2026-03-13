// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutomationSchemaGeneration(t *testing.T) {
	tests := []struct {
		name           string
		schema         func() any
		expectedFields []string
	}{
		{
			name:           "ListAutomationsArgs schema",
			schema:         func() any { return llm.NewJSONSchemaFromStruct[ListAutomationsArgs]() },
			expectedFields: []string{"automation_id", "channel_id", "query", "enabled"},
		},
		{
			name:           "CreateAutomationArgs schema",
			schema:         func() any { return llm.NewJSONSchemaFromStruct[CreateAutomationArgs]() },
			expectedFields: []string{"name", "enabled", "trigger", "actions"},
		},
		{
			name:           "UpdateAutomationArgs schema",
			schema:         func() any { return llm.NewJSONSchemaFromStruct[UpdateAutomationArgs]() },
			expectedFields: []string{"automation_id", "name", "enabled", "trigger", "actions"},
		},
		{
			name:           "DeleteAutomationArgs schema",
			schema:         func() any { return llm.NewJSONSchemaFromStruct[DeleteAutomationArgs]() },
			expectedFields: []string{"automation_id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := tt.schema()
			require.NotNil(t, schema)

			// Marshal to JSON and check fields exist
			data, err := json.Marshal(schema)
			require.NoError(t, err)

			var schemaMap map[string]any
			require.NoError(t, json.Unmarshal(data, &schemaMap))

			properties, ok := schemaMap["properties"].(map[string]any)
			require.True(t, ok, "schema should have properties")

			for _, field := range tt.expectedFields {
				assert.Contains(t, properties, field, "schema should contain field %s", field)
			}
		})
	}
}

func TestAutomationAPIURL(t *testing.T) {
	provider := &MattermostToolProvider{
		mmInternalServerURL: "http://localhost:8065",
	}

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "flows list",
			path:     "/flows",
			expected: "http://localhost:8065/plugins/com.mattermost.channel-automation/api/v1/flows",
		},
		{
			name:     "flows by id",
			path:     "/flows/abc123",
			expected: "http://localhost:8065/plugins/com.mattermost.channel-automation/api/v1/flows/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, provider.automationAPIURL(tt.path))
		})
	}
}

func TestIsAutomationTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"list_automations", "list_automations", true},
		{"create_automation", "create_automation", true},
		{"update_automation", "update_automation", true},
		{"delete_automation", "delete_automation", true},
		{"read_channel", "read_channel", false},
		{"create_post", "create_post", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsAutomationTool(tt.toolName))
		})
	}
}

func TestValidateTrigger(t *testing.T) {
	tests := []struct {
		name    string
		trigger AutomationTrigger
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no trigger set",
			trigger: AutomationTrigger{},
			wantErr: true,
			errMsg:  "trigger is required",
		},
		{
			name:    "message_posted trigger",
			trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch1"}},
			wantErr: false,
		},
		{
			name:    "schedule trigger",
			trigger: AutomationTrigger{Schedule: &ScheduleConfig{ChannelID: "ch1", Interval: "daily"}},
			wantErr: false,
		},
		{
			name:    "membership_changed trigger",
			trigger: AutomationTrigger{MembershipChanged: &MembershipChangedConfig{ChannelID: "ch1"}},
			wantErr: false,
		},
		{
			name:    "channel_created trigger",
			trigger: AutomationTrigger{ChannelCreated: &ChannelCreatedConfig{}},
			wantErr: false,
		},
		{
			name: "multiple triggers set",
			trigger: AutomationTrigger{
				MessagePosted: &MessagePostedConfig{ChannelID: "ch1"},
				Schedule:      &ScheduleConfig{ChannelID: "ch1", Interval: "daily"},
			},
			wantErr: true,
			errMsg:  "exactly one type set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTrigger(tt.trigger)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTriggerHelpers(t *testing.T) {
	tests := []struct {
		name     string
		trigger  AutomationTrigger
		wantType string
		wantChID string
	}{
		{
			name:     "message_posted",
			trigger:  AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch1"}},
			wantType: "message_posted",
			wantChID: "ch1",
		},
		{
			name:     "schedule",
			trigger:  AutomationTrigger{Schedule: &ScheduleConfig{ChannelID: "ch2", Interval: "daily"}},
			wantType: "schedule",
			wantChID: "ch2",
		},
		{
			name:     "membership_changed",
			trigger:  AutomationTrigger{MembershipChanged: &MembershipChangedConfig{ChannelID: "ch3"}},
			wantType: "membership_changed",
			wantChID: "ch3",
		},
		{
			name:     "channel_created",
			trigger:  AutomationTrigger{ChannelCreated: &ChannelCreatedConfig{}},
			wantType: "channel_created",
			wantChID: "",
		},
		{
			name:     "empty trigger",
			trigger:  AutomationTrigger{},
			wantType: "unknown",
			wantChID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantType, triggerTypeName(tt.trigger))
			assert.Equal(t, tt.wantChID, triggerChannelID(tt.trigger))
		})
	}
}

func TestActionTypeName(t *testing.T) {
	tests := []struct {
		name     string
		action   AutomationAction
		wantType string
	}{
		{
			name:     "send_message",
			action:   AutomationAction{ID: "a1", SendMessage: &SendMessageActionConfig{Body: "hi"}},
			wantType: "send_message",
		},
		{
			name:     "ai_prompt",
			action:   AutomationAction{ID: "a2", AIPrompt: &AIPromptActionConfig{Prompt: "hello"}},
			wantType: "ai_prompt",
		},
		{
			name:     "empty action",
			action:   AutomationAction{ID: "a3"},
			wantType: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantType, actionTypeName(tt.action))
		})
	}
}

func TestFilterAutomationFlows(t *testing.T) {
	flows := []AutomationFlow{
		{ID: "1", Name: "Welcome Message", Enabled: true, Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch1"}}},
		{ID: "2", Name: "Bug Report", Enabled: false, Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch2"}}},
		{ID: "3", Name: "Welcome Notification", Enabled: true, Trigger: AutomationTrigger{MembershipChanged: &MembershipChangedConfig{ChannelID: "ch1"}}},
		{ID: "4", Name: "Daily Standup", Enabled: true, Trigger: AutomationTrigger{Schedule: &ScheduleConfig{ChannelID: "ch3", Interval: "daily"}}},
	}

	boolTrue := true
	boolFalse := false

	tests := []struct {
		name        string
		channelID   string
		query       string
		enabled     *bool
		expectedIDs []string
	}{
		{
			name:        "no filters returns all",
			expectedIDs: []string{"1", "2", "3", "4"},
		},
		{
			name:        "filter by channel_id",
			channelID:   "ch1",
			expectedIDs: []string{"1", "3"},
		},
		{
			name:        "filter by name query",
			query:       "welcome",
			expectedIDs: []string{"1", "3"},
		},
		{
			name:        "filter by enabled true",
			enabled:     &boolTrue,
			expectedIDs: []string{"1", "3", "4"},
		},
		{
			name:        "filter by enabled false",
			enabled:     &boolFalse,
			expectedIDs: []string{"2"},
		},
		{
			name:        "combined channel and query",
			channelID:   "ch1",
			query:       "message",
			expectedIDs: []string{"1"},
		},
		{
			name:        "no match",
			query:       "nonexistent",
			expectedIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterAutomationFlows(flows, tt.channelID, tt.query, tt.enabled)

			ids := make([]string, len(result))
			for i, f := range result {
				ids[i] = f.ID
			}
			assert.Equal(t, tt.expectedIDs, ids)
		})
	}
}

// newTestAutomationServer creates an httptest server that mimics the channel-automation plugin API.
func newTestAutomationServer(t *testing.T, flows []AutomationFlow) *httptest.Server {
	t.Helper()

	flowMap := make(map[string]AutomationFlow)
	for _, f := range flows {
		flowMap[f.ID] = f
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/plugins/com.mattermost.channel-automation/api/v1/flows", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			allFlows := make([]AutomationFlow, 0, len(flowMap))
			for _, f := range flowMap {
				allFlows = append(allFlows, f)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(allFlows)

		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var flow AutomationFlow
			if err := json.Unmarshal(body, &flow); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			flow.ID = "new-flow-id"
			flowMap[flow.ID] = flow
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(flow)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/plugins/com.mattermost.channel-automation/api/v1/flows/", func(w http.ResponseWriter, r *http.Request) {
		// Extract ID from path: /plugins/.../flows/{id}
		id := r.URL.Path[len("/plugins/com.mattermost.channel-automation/api/v1/flows/"):]

		switch r.Method {
		case http.MethodGet:
			flow, ok := flowMap[id]
			if !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(flow)

		case http.MethodPut:
			if _, ok := flowMap[id]; !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var flow AutomationFlow
			if err := json.Unmarshal(body, &flow); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			flow.ID = id
			flowMap[id] = flow
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(flow)

		case http.MethodDelete:
			if _, ok := flowMap[id]; !ok {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			delete(flowMap, id)
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Mattermost API v4 endpoint stubs needed by Client4
	mux.HandleFunc("/api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "test-user-id"})
	})

	return httptest.NewServer(mux)
}

func newTestProvider(t *testing.T, serverURL string) *MattermostToolProvider {
	t.Helper()
	return &MattermostToolProvider{
		logger:              &testLogger{t: t},
		mmInternalServerURL: serverURL,
	}
}

func newTestClient(serverURL string) *model.Client4 {
	client := model.NewAPIv4Client(serverURL)
	client.SetToken("test-token")
	return client
}

func TestAutomationListFlows(t *testing.T) {
	sampleFlows := []AutomationFlow{
		{
			ID:      "flow1",
			Name:    "Welcome Bot",
			Enabled: true,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch-abc"}},
			Actions: []AutomationAction{{ID: "greet", SendMessage: &SendMessageActionConfig{Body: "Hello!"}}},
		},
		{
			ID:      "flow2",
			Name:    "Bug Triage",
			Enabled: false,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch-def"}},
			Actions: []AutomationAction{{ID: "summarize", AIPrompt: &AIPromptActionConfig{Prompt: "Summarize", ProviderType: "agent", ProviderID: "bot1"}}},
		},
	}

	ts := newTestAutomationServer(t, sampleFlows)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client}

	t.Run("list all", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Welcome Bot")
		assert.Contains(t, result, "Bug Triage")
	})

	t.Run("get by id", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"automation_id":"flow1"}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Welcome Bot")
		assert.NotContains(t, result, "Bug Triage")
	})

	t.Run("filter by channel_id", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"channel_id":"ch-def"}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Bug Triage")
		assert.NotContains(t, result, "Welcome Bot")
	})

	t.Run("get by id not found", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"automation_id":"nonexistent"}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "Automation not found")
	})
}

func TestAutomationCreateFlow(t *testing.T) {
	ts := newTestAutomationServer(t, nil)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client}

	t.Run("create with message_posted trigger", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "Test Flow",
				"enabled": true,
				"trigger": {"message_posted": {"channel_id": "abcdefghijklmnopqrstuvwxyz"}},
				"actions": [{"id": "greet", "send_message": {"channel_id": "abcdefghijklmnopqrstuvwxyz", "body": "Hello!"}}]
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully created automation")
		assert.Contains(t, result, "Test Flow")
		assert.Contains(t, result, "new-flow-id")
	})

	t.Run("create with schedule trigger", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "Scheduled Flow",
				"enabled": true,
				"trigger": {"schedule": {"channel_id": "abcdefghijklmnopqrstuvwxyz", "interval": "daily"}},
				"actions": [{"id": "post", "send_message": {"channel_id": "abcdefghijklmnopqrstuvwxyz", "body": "Daily update"}}]
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully created automation")
		assert.Contains(t, result, "Scheduled Flow")
	})

	t.Run("create with ai_prompt action", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "AI Flow",
				"enabled": true,
				"trigger": {"message_posted": {"channel_id": "abcdefghijklmnopqrstuvwxyz"}},
				"actions": [
					{"id": "ask", "ai_prompt": {"prompt": "Summarize this", "provider_type": "agent", "provider_id": "bot123", "system_prompt": "You are helpful", "allowed_tools": ["search"], "tool_constraints": {"search": {"query": {"allowed_values": ["bugs", "features"]}}}}},
					{"id": "post", "send_message": {"channel_id": "abcdefghijklmnopqrstuvwxyz", "body": "Result: {{(index .Steps \"ask\").Message}}"}}
				]
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully created automation")
		assert.Contains(t, result, "AI Flow")
	})

	t.Run("create missing name", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "",
				"trigger": {"message_posted": {"channel_id": "abcdefghijklmnopqrstuvwxyz"}}
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Equal(t, "name is required", result)
	})

	t.Run("create missing trigger", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "Test",
				"trigger": {}
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "trigger is required")
	})

	t.Run("create multiple triggers", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"name": "Test",
				"trigger": {"message_posted": {"channel_id": "ch1"}, "schedule": {"channel_id": "ch1", "interval": "daily"}}
			}`), target)
		}

		result, err := provider.toolCreateAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "exactly one type set")
	})
}

func TestAutomationUpdateFlow(t *testing.T) {
	sampleFlows := []AutomationFlow{
		{ID: "flow1", Name: "Original", Enabled: true, Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch1"}}},
	}

	ts := newTestAutomationServer(t, sampleFlows)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client}

	t.Run("update success", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"automation_id": "flow1",
				"name": "Updated Name",
				"enabled": false,
				"trigger": {"message_posted": {"channel_id": "abcdefghijklmnopqrstuvwxyz"}},
				"actions": []
			}`), target)
		}

		result, err := provider.toolUpdateAutomation(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully updated automation")
		assert.Contains(t, result, "Updated Name")
	})

	t.Run("update not found", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{
				"automation_id": "nonexistent",
				"name": "X",
				"trigger": {"message_posted": {"channel_id": "abcdefghijklmnopqrstuvwxyz"}}
			}`), target)
		}

		result, err := provider.toolUpdateAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "Automation not found")
	})

	t.Run("update missing automation_id", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"name": "X"}`), target)
		}

		result, err := provider.toolUpdateAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Equal(t, "automation_id is required", result)
	})
}

func TestAutomationDeleteFlow(t *testing.T) {
	sampleFlows := []AutomationFlow{
		{ID: "flow1", Name: "To Delete", Enabled: true},
	}

	ts := newTestAutomationServer(t, sampleFlows)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	client := newTestClient(ts.URL)
	mcpCtx := &MCPToolContext{Client: client}

	t.Run("delete success", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"automation_id": "flow1"}`), target)
		}

		result, err := provider.toolDeleteAutomation(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Successfully deleted automation")
		assert.Contains(t, result, "flow1")
	})

	t.Run("delete not found", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{"automation_id": "nonexistent"}`), target)
		}

		result, err := provider.toolDeleteAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "Automation not found")
	})

	t.Run("delete missing automation_id", func(t *testing.T) {
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolDeleteAutomation(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Equal(t, "automation_id is required", result)
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
		mcpCtx := &MCPToolContext{Client: client}

		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "permission")
	})

	t.Run("connection error", func(t *testing.T) {
		// Use an unreachable URL
		provider := newTestProvider(t, "http://127.0.0.1:1")
		client := newTestClient("http://127.0.0.1:1")
		mcpCtx := &MCPToolContext{Client: client}

		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "not installed or not reachable")
	})

	t.Run("nil client", func(t *testing.T) {
		provider := newTestProvider(t, "http://localhost:8065")
		mcpCtx := &MCPToolContext{Client: nil}

		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAutomations(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Equal(t, "client not available", result)
	})
}

func TestAutomationGetToolsCount(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	tools := provider.getAutomationTools()
	assert.Len(t, tools, 4)

	expectedNames := []string{"list_automations", "create_automation", "update_automation", "delete_automation"}
	for i, name := range expectedNames {
		assert.Equal(t, name, tools[i].Name, "tool %d should be named %s", i, name)
		assert.NotEmpty(t, tools[i].Description)
		assert.NotNil(t, tools[i].Schema)
		assert.NotNil(t, tools[i].Resolver)
	}
}

func TestFormatAutomationFlow(t *testing.T) {
	t.Run("send_message action", func(t *testing.T) {
		flow := AutomationFlow{
			ID:      "test-id",
			Name:    "Test Flow",
			Enabled: true,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch-123"}},
			Actions: []AutomationAction{
				{ID: "send", SendMessage: &SendMessageActionConfig{ChannelID: "ch-456", Body: "Hello"}},
			},
		}

		result := formatAutomationFlow(flow)
		assert.Contains(t, result, "Test Flow")
		assert.Contains(t, result, "test-id")
		assert.Contains(t, result, "true")
		assert.Contains(t, result, "message_posted")
		assert.Contains(t, result, "ch-123")
		assert.Contains(t, result, "send_message")
		assert.Contains(t, result, "ch-456")
		assert.Contains(t, result, "Hello")
	})

	t.Run("ai_prompt action", func(t *testing.T) {
		flow := AutomationFlow{
			ID:      "ai-id",
			Name:    "AI Flow",
			Enabled: true,
			Trigger: AutomationTrigger{MessagePosted: &MessagePostedConfig{ChannelID: "ch-1"}},
			Actions: []AutomationAction{
				{ID: "ask", AIPrompt: &AIPromptActionConfig{
					Prompt:       "Summarize",
					SystemPrompt: "Be helpful",
					ProviderType: "agent",
					ProviderID:   "bot1",
					AllowedTools: []string{"search"},
					ToolConstraints: ToolConstraints{
						"search": {"query": ParamConstraint{AllowedValues: []string{"bugs"}}},
					},
				}},
			},
		}

		result := formatAutomationFlow(flow)
		assert.Contains(t, result, "ai_prompt")
		assert.Contains(t, result, "Summarize")
		assert.Contains(t, result, "Be helpful")
		assert.Contains(t, result, "search")
		assert.Contains(t, result, "tool_constraints=<configured>")
	})

	t.Run("schedule trigger with interval", func(t *testing.T) {
		flow := AutomationFlow{
			ID:      "sched-id",
			Name:    "Scheduled",
			Enabled: true,
			Trigger: AutomationTrigger{Schedule: &ScheduleConfig{ChannelID: "ch-1", Interval: "daily"}},
		}

		result := formatAutomationFlow(flow)
		assert.Contains(t, result, "schedule")
		assert.Contains(t, result, "interval=daily")
	})

	t.Run("channel_created trigger no channel", func(t *testing.T) {
		flow := AutomationFlow{
			ID:      "cc-id",
			Name:    "On Channel Create",
			Enabled: true,
			Trigger: AutomationTrigger{ChannelCreated: &ChannelCreatedConfig{}},
		}

		result := formatAutomationFlow(flow)
		assert.Contains(t, result, "channel_created")
		assert.NotContains(t, result, "channel=")
	})
}

func TestFormatAutomationFlows(t *testing.T) {
	flows := []AutomationFlow{
		{ID: "1", Name: "Flow A"},
		{ID: "2", Name: "Flow B"},
	}

	result := formatAutomationFlows(flows)
	assert.Contains(t, result, "Found 2 automation(s)")
	assert.Contains(t, result, "1. ")
	assert.Contains(t, result, "2. ")
	assert.Contains(t, result, "Flow A")
	assert.Contains(t, result, "Flow B")
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
			expectedResult: "Bad request: invalid trigger configuration",
		},
		{
			name:           "400 bad request empty body falls back to error",
			statusCode:     http.StatusBadRequest,
			body:           "",
			expectedResult: "Bad request: test error",
		},
		{
			name:           "401 unauthorized",
			statusCode:     http.StatusUnauthorized,
			automationID:   "",
			expectedResult: "You don't have permission to manage automations for this channel",
		},
		{
			name:           "403 forbidden",
			statusCode:     http.StatusForbidden,
			automationID:   "",
			expectedResult: "You don't have permission to manage automations for this channel",
		},
		{
			name:           "404 with automation id",
			statusCode:     http.StatusNotFound,
			automationID:   "abc123",
			expectedResult: "Automation not found with ID 'abc123'",
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
			expectedResult: "not installed or not reachable",
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

			result, err := handleAutomationHTTPError(resp, fmt.Errorf("test error"), tt.automationID)
			require.Error(t, err)
			assert.Contains(t, result, tt.expectedResult)
		})
	}

	t.Run("nil response (connection error)", func(t *testing.T) {
		result, err := handleAutomationHTTPError(nil, fmt.Errorf("connection refused"), "")
		require.Error(t, err)
		assert.Contains(t, result, "not installed or not reachable")
	})

	t.Run("400 with nil error and empty body", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       http.NoBody,
		}
		result, err := handleAutomationHTTPError(resp, nil, "")
		require.Error(t, err)
		assert.Contains(t, result, "Bad request: invalid request")
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
