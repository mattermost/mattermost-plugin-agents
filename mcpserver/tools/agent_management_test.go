// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestToolCreateAgentUsesPluginRoutes(t *testing.T) {
	t.Parallel()

	var createdBody agentAPIRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "BEARER test-token", r.Header.Get(model.HeaderAuth))

		switch r.URL.Path {
		case "/plugins/mattermost-ai/services":
			require.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode([]serviceInfoResponse{{
				ID:   "svc-anthropic",
				Name: "Anthropic Claude",
			}}))
		case "/plugins/mattermost-ai/agents":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createdBody))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(llm.BotConfig{
				ID:                      "agent-id",
				Name:                    createdBody.Username,
				DisplayName:             createdBody.DisplayName,
				CustomInstructions:      createdBody.CustomInstructions,
				ServiceID:               createdBody.ServiceID,
				ChannelAccessLevel:      llm.ChannelAccessLevel(createdBody.ChannelAccessLevel),
				ChannelIDs:              createdBody.ChannelIDs,
				UserAccessLevel:         llm.UserAccessLevel(createdBody.UserAccessLevel),
				UserIDs:                 createdBody.UserIDs,
				TeamIDs:                 createdBody.TeamIDs,
				AdminUserIDs:            createdBody.AdminUserIDs,
				EnabledMCPTools:         createdBody.EnabledMCPTools,
				Model:                   createdBody.Model,
				EnableVision:            createdBody.EnableVision,
				DisableTools:            createdBody.DisableTools,
				EnabledNativeTools:      createdBody.EnabledNativeTools,
				ReasoningEnabled:        createdBody.ReasoningEnabled,
				ReasoningEffort:         createdBody.ReasoningEffort,
				ThinkingBudget:          createdBody.ThinkingBudget,
				StructuredOutputEnabled: createdBody.StructuredOutputEnabled,
			}))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	result, err := provider.toolCreateAgent(mcpContext, jsonArgsGetter(t, CreateAgentToolArgs{
		DisplayName: "Release Notes Agent",
		Username:    "@release-bot",
		ServiceName: "Anthropic Claude",
	}))
	require.NoError(t, err)

	var createdAgent llm.BotConfig
	require.NoError(t, json.Unmarshal([]byte(result), &createdAgent))
	assert.Equal(t, "agent-id", createdAgent.ID)
	assert.Equal(t, "release-bot", createdBody.Username)
	assert.Equal(t, "svc-anthropic", createdBody.ServiceID)
	assert.Equal(t, int(llm.ChannelAccessLevelAll), createdBody.ChannelAccessLevel)
	assert.Equal(t, int(llm.UserAccessLevelAll), createdBody.UserAccessLevel)
	assert.True(t, createdBody.EnableVision)
	assert.False(t, createdBody.DisableTools)
	assert.True(t, createdBody.ReasoningEnabled)
	assert.Equal(t, "medium", createdBody.ReasoningEffort)
	assert.Equal(t, []string{"web_search"}, createdBody.EnabledNativeTools)
	assert.Nil(t, createdBody.EnabledMCPTools)
}

func TestToolGetAgentsAndGetCustomPromptsUsePluginRoutes(t *testing.T) {
	t.Parallel()

	agents := []llm.BotConfig{
		{
			ID:                 "agent-1",
			Name:               "release-bot",
			DisplayName:        "Release Notes Agent",
			CustomInstructions: "Summarize releases.",
			ServiceID:          "svc-anthropic",
		},
		{
			ID:                 "agent-2",
			Name:               "support-bot",
			DisplayName:        "Support Agent",
			CustomInstructions: "Help users troubleshoot issues.",
			ServiceID:          "svc-openai",
		},
	}
	prompts := []customprompts.CustomPrompt{
		{
			ID:          "prompt-1",
			CreatorID:   "user-1",
			Name:        "Daily Summary",
			Description: "Summarizes the day.",
			Template:    "Summarize today.",
			IsShared:    true,
		},
		{
			ID:          "prompt-2",
			CreatorID:   "user-1",
			Name:        "Incident Report",
			Description: "Formats incidents.",
			Template:    "Format the incident.",
			IsShared:    false,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "BEARER test-token", r.Header.Get(model.HeaderAuth))
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/plugins/mattermost-ai/agents":
			require.Equal(t, http.MethodGet, r.Method)
			require.NoError(t, json.NewEncoder(w).Encode(agents))
		case "/plugins/mattermost-ai/custom-prompts":
			require.Equal(t, http.MethodGet, r.Method)
			require.NoError(t, json.NewEncoder(w).Encode(prompts))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	tests := []struct {
		name     string
		tool     func(*MCPToolContext, llm.ToolArgumentGetter) (string, error)
		args     any
		validate func(*testing.T, string)
	}{
		{
			name: "list all agents",
			tool: provider.toolGetAgents,
			args: GetAgentsToolArgs{},
			validate: func(t *testing.T, result string) {
				t.Helper()

				var listedAgents []llm.BotConfig
				require.NoError(t, json.Unmarshal([]byte(result), &listedAgents))
				require.Len(t, listedAgents, 2)
				assert.Equal(t, "agent-1", listedAgents[0].ID)
				assert.Equal(t, "release-bot", listedAgents[0].Name)
				assert.Equal(t, "agent-2", listedAgents[1].ID)
			},
		},
		{
			name: "list all prompts",
			tool: provider.toolGetCustomPrompts,
			args: GetCustomPromptToolArgs{},
			validate: func(t *testing.T, result string) {
				t.Helper()

				var listedPrompts []customprompts.CustomPrompt
				require.NoError(t, json.Unmarshal([]byte(result), &listedPrompts))
				require.Len(t, listedPrompts, 2)
				assert.Equal(t, "prompt-1", listedPrompts[0].ID)
				assert.Equal(t, "prompt-2", listedPrompts[1].ID)
				assert.Equal(t, "Incident Report", listedPrompts[1].Name)
			},
		},
		{
			name: "filter agent by username",
			tool: provider.toolGetAgents,
			args: GetAgentsToolArgs{AgentUsername: "release-bot"},
			validate: func(t *testing.T, result string) {
				t.Helper()

				var listedAgents []llm.BotConfig
				require.NoError(t, json.Unmarshal([]byte(result), &listedAgents))
				require.Len(t, listedAgents, 1)
				assert.Equal(t, "agent-1", listedAgents[0].ID)
				assert.Equal(t, "release-bot", listedAgents[0].Name)
			},
		},
		{
			name: "filter prompt by name",
			tool: provider.toolGetCustomPrompts,
			args: GetCustomPromptToolArgs{PromptName: "Incident Report"},
			validate: func(t *testing.T, result string) {
				t.Helper()

				var listedPrompts []customprompts.CustomPrompt
				require.NoError(t, json.Unmarshal([]byte(result), &listedPrompts))
				require.Len(t, listedPrompts, 1)
				assert.Equal(t, "prompt-2", listedPrompts[0].ID)
				assert.Equal(t, "Incident Report", listedPrompts[0].Name)
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			result, err := testCase.tool(mcpContext, jsonArgsGetter(t, testCase.args))
			require.NoError(t, err)
			testCase.validate(t, result)
		})
	}
}

func TestToolUpdateAgentMergesExistingState(t *testing.T) {
	t.Parallel()

	currentAgent := llm.BotConfig{
		ID:                      "agent-id",
		Name:                    "release-bot",
		DisplayName:             "Release Notes Agent",
		CustomInstructions:      "Keep answers short.",
		ServiceID:               "svc-anthropic",
		ChannelAccessLevel:      llm.ChannelAccessLevelAllow,
		ChannelIDs:              []string{"channel-1"},
		UserAccessLevel:         llm.UserAccessLevelBlock,
		UserIDs:                 []string{"user-1"},
		TeamIDs:                 []string{"team-1"},
		AdminUserIDs:            []string{"admin-1"},
		EnabledMCPTools:         nil,
		Model:                   "claude-3-7-sonnet",
		EnableVision:            true,
		DisableTools:            false,
		EnabledNativeTools:      []string{"web_search"},
		ReasoningEnabled:        true,
		ReasoningEffort:         "medium",
		ThinkingBudget:          0,
		StructuredOutputEnabled: false,
	}

	var updatedBody agentAPIRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "BEARER test-token", r.Header.Get(model.HeaderAuth))

		switch r.URL.Path {
		case "/plugins/mattermost-ai/agents":
			require.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode([]llm.BotConfig{currentAgent}))
		case "/plugins/mattermost-ai/services":
			require.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode([]serviceInfoResponse{{
				ID:   "svc-openai",
				Name: "OpenAI",
			}}))
		case "/plugins/mattermost-ai/agents/agent-id":
			require.Equal(t, http.MethodPut, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&updatedBody))
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(llm.BotConfig{
				ID:                      currentAgent.ID,
				Name:                    updatedBody.Username,
				DisplayName:             updatedBody.DisplayName,
				CustomInstructions:      updatedBody.CustomInstructions,
				ServiceID:               updatedBody.ServiceID,
				ChannelAccessLevel:      llm.ChannelAccessLevel(updatedBody.ChannelAccessLevel),
				ChannelIDs:              updatedBody.ChannelIDs,
				UserAccessLevel:         llm.UserAccessLevel(updatedBody.UserAccessLevel),
				UserIDs:                 updatedBody.UserIDs,
				TeamIDs:                 updatedBody.TeamIDs,
				AdminUserIDs:            updatedBody.AdminUserIDs,
				EnabledMCPTools:         updatedBody.EnabledMCPTools,
				Model:                   updatedBody.Model,
				EnableVision:            updatedBody.EnableVision,
				DisableTools:            updatedBody.DisableTools,
				EnabledNativeTools:      updatedBody.EnabledNativeTools,
				ReasoningEnabled:        updatedBody.ReasoningEnabled,
				ReasoningEffort:         updatedBody.ReasoningEffort,
				ThinkingBudget:          updatedBody.ThinkingBudget,
				StructuredOutputEnabled: updatedBody.StructuredOutputEnabled,
			}))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	newDisplayName := "Release Captain"
	newServiceName := "OpenAI"
	toolMode := agentToolPolicyAllowNone

	result, err := provider.toolUpdateAgent(mcpContext, jsonArgsGetter(t, UpdateAgentToolArgs{
		AgentUsername:       "release-bot",
		DisplayName:         &newDisplayName,
		ServiceName:         &newServiceName,
		EnabledMCPToolsMode: &toolMode,
	}))
	require.NoError(t, err)

	var updatedAgent llm.BotConfig
	require.NoError(t, json.Unmarshal([]byte(result), &updatedAgent))
	assert.Equal(t, "Release Captain", updatedAgent.DisplayName)
	assert.Equal(t, "release-bot", updatedBody.Username)
	assert.Equal(t, "svc-openai", updatedBody.ServiceID)
	assert.Equal(t, currentAgent.CustomInstructions, updatedBody.CustomInstructions)
	assert.Equal(t, currentAgent.ChannelIDs, updatedBody.ChannelIDs)
	assert.Equal(t, currentAgent.AdminUserIDs, updatedBody.AdminUserIDs)
	assert.Empty(t, updatedBody.EnabledMCPTools)
	assert.Equal(t, currentAgent.EnabledNativeTools, updatedBody.EnabledNativeTools)
}

func TestToolCreateAndUpdateCustomPromptUsePluginRoutes(t *testing.T) {
	t.Parallel()

	var createdPromptBody map[string]any
	var updatedPromptBody map[string]any
	listCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "BEARER test-token", r.Header.Get(model.HeaderAuth))

		switch r.URL.Path {
		case "/plugins/mattermost-ai/custom-prompts":
			switch r.Method {
			case http.MethodPost:
				require.NoError(t, json.NewDecoder(r.Body).Decode(&createdPromptBody))
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(customprompts.CustomPrompt{
					ID:          "prompt-1",
					CreatorID:   "user-1",
					Name:        createdPromptBody["name"].(string),
					Description: createdPromptBody["description"].(string),
					Template:    createdPromptBody["template"].(string),
					IsShared:    createdPromptBody["is_shared"].(bool),
				}))
			case http.MethodGet:
				listCalls++
				w.Header().Set("Content-Type", "application/json")
				if listCalls == 1 {
					require.NoError(t, json.NewEncoder(w).Encode([]customprompts.CustomPrompt{{
						ID:          "prompt-1",
						CreatorID:   "user-1",
						Name:        "Daily Summary",
						Description: "Original description",
						Template:    "Original template",
						IsShared:    false,
					}}))
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]customprompts.CustomPrompt{{
					ID:          "prompt-1",
					CreatorID:   "user-1",
					Name:        "Daily Summary",
					Description: "",
					Template:    "Updated template",
					IsShared:    true,
				}}))
			default:
				t.Fatalf("unexpected method %s for %s", r.Method, r.URL.Path)
			}
		case "/plugins/mattermost-ai/custom-prompts/prompt-1":
			require.Equal(t, http.MethodPut, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&updatedPromptBody))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	createResult, err := provider.toolCreateCustomPrompt(mcpContext, jsonArgsGetter(t, CreateCustomPromptToolArgs{
		Name:     "Daily Summary",
		Template: "Summarize the latest channel activity.",
	}))
	require.NoError(t, err)

	var createdPrompt customprompts.CustomPrompt
	require.NoError(t, json.Unmarshal([]byte(createResult), &createdPrompt))
	assert.Equal(t, "prompt-1", createdPrompt.ID)
	assert.Equal(t, false, createdPromptBody["is_shared"])

	updatedTemplate := "Updated template"
	updatedDescription := ""
	shared := true

	updateResult, err := provider.toolUpdateCustomPrompt(mcpContext, jsonArgsGetter(t, UpdateCustomPromptToolArgs{
		PromptName:  "Daily Summary",
		Template:    &updatedTemplate,
		Description: &updatedDescription,
		IsShared:    &shared,
	}))
	require.NoError(t, err)

	var updatedPrompt customprompts.CustomPrompt
	require.NoError(t, json.Unmarshal([]byte(updateResult), &updatedPrompt))
	assert.Equal(t, "Updated template", updatedPrompt.Template)
	assert.Equal(t, "", updatedPromptBody["description"])
	assert.Equal(t, "Updated template", updatedPromptBody["template"])
	assert.Equal(t, true, updatedPromptBody["is_shared"])
}

func TestToolUpdateMutationsRequireIdentifiers(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	tests := []struct {
		name        string
		tool        func(*MCPToolContext, llm.ToolArgumentGetter) (string, error)
		args        any
		wantMessage string
		wantError   string
	}{
		{
			name:        "agent update requires identifier",
			tool:        provider.toolUpdateAgent,
			args:        UpdateAgentToolArgs{},
			wantMessage: "agent_id or agent_username is required",
			wantError:   "agent_id or agent_username is required",
		},
		{
			name:        "custom prompt update requires identifier",
			tool:        provider.toolUpdateCustomPrompt,
			args:        UpdateCustomPromptToolArgs{},
			wantMessage: "prompt_id or prompt_name is required",
			wantError:   "prompt_id or prompt_name is required",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			message, err := testCase.tool(mcpContext, jsonArgsGetter(t, testCase.args))
			require.Error(t, err)
			assert.Equal(t, testCase.wantMessage, message)
			assert.Contains(t, err.Error(), testCase.wantError)
		})
	}
}

func TestResolveServiceIDRequiresIdentifierBeforeFetch(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	defer server.Close()

	serviceID, err := resolveServiceID(newTestMCPToolContext(server.URL), "", "")
	require.Error(t, err)
	assert.Empty(t, serviceID)
	assert.Equal(t, 0, requests)
	assert.Contains(t, err.Error(), "service_id or service_name is required")
}

func TestToolUpdateCustomPromptReturnsFallbackWhenReloadFails(t *testing.T) {
	t.Parallel()

	var updatedPromptBody map[string]any
	listCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "BEARER test-token", r.Header.Get(model.HeaderAuth))

		switch r.URL.Path {
		case "/plugins/mattermost-ai/custom-prompts":
			require.Equal(t, http.MethodGet, r.Method)
			listCalls++
			if listCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode([]customprompts.CustomPrompt{{
					ID:          "prompt-1",
					CreatorID:   "user-1",
					Name:        "Daily Summary",
					Description: "Original description",
					Template:    "Original template",
					IsShared:    false,
				}}))
				return
			}
			http.Error(w, "reload failed", http.StatusInternalServerError)
		case "/plugins/mattermost-ai/custom-prompts/prompt-1":
			require.Equal(t, http.MethodPut, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&updatedPromptBody))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	updatedTemplate := "Updated template"
	updatedDescription := ""
	shared := true

	result, err := provider.toolUpdateCustomPrompt(mcpContext, jsonArgsGetter(t, UpdateCustomPromptToolArgs{
		PromptID:    "prompt-1",
		Template:    &updatedTemplate,
		Description: &updatedDescription,
		IsShared:    &shared,
	}))
	require.NoError(t, err)

	var updatedPrompt customprompts.CustomPrompt
	require.NoError(t, json.Unmarshal([]byte(result), &updatedPrompt))
	assert.Equal(t, "prompt-1", updatedPrompt.ID)
	assert.Equal(t, "Daily Summary", updatedPrompt.Name)
	assert.Equal(t, "", updatedPrompt.Description)
	assert.Equal(t, "Updated template", updatedPrompt.Template)
	assert.True(t, updatedPrompt.IsShared)
	assert.Equal(t, "Updated template", updatedPromptBody["template"])
}

func TestToolGetAgentsRequiresVisibleMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/plugins/mattermost-ai/agents", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]llm.BotConfig{{
			ID:          "agent-1",
			Name:        "release-bot",
			DisplayName: "Release Notes Agent",
			ServiceID:   "svc-anthropic",
		}}))
	}))
	defer server.Close()

	provider := &MattermostToolProvider{logger: &testLogger{t: t}}
	mcpContext := newTestMCPToolContext(server.URL)

	_, err := provider.toolGetAgents(mcpContext, jsonArgsGetter(t, GetAgentsToolArgs{
		AgentID: "missing-agent",
	}))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "agent_id \"missing-agent\" was not found"))
}

func newTestMCPToolContext(serverURL string) *MCPToolContext {
	client := model.NewAPIv4Client(serverURL)
	client.SetToken("test-token")
	return &MCPToolContext{
		Ctx:        context.Background(),
		Client:     client,
		AccessMode: AccessModeRemote,
	}
}

func jsonArgsGetter(t *testing.T, args any) llm.ToolArgumentGetter {
	t.Helper()

	return func(target interface{}) error {
		t.Helper()
		data, err := json.Marshal(args)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, target)
	}
}
