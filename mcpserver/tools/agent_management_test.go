// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
