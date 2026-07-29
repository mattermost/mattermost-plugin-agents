// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/stretchr/testify/require"
)

const (
	// Each tool exists in only one catalog, so tests fail if the wrong catalog is in effect.
	saToolName   = "mattermost__sa_tool"
	userToolName = "mattermost__user_tool"
)

// AutoEnableNewMCPTools keeps every catalog tool in the store so allowed_tools is the only filter.
func (e *TestEnvironment) setupBridgeCatalogBot(useServiceAccountAuth bool) {
	e.setupTestBot(llm.BotConfig{
		Name:                  "testbot",
		DisplayName:           "Test Bot",
		UserAccessLevel:       llm.UserAccessLevelAll,
		AutoEnableNewMCPTools: true,
		UseServiceAccountAuth: useServiceAccountAuth,
	})
}

func (e *TestEnvironment) setBridgeFakeLLM(fakeLLM *FakeLLM) {
	for _, bot := range e.bots.GetAllBots() {
		bot.SetLLMForTest(fakeLLM)
	}
}

func TestBridgeAgentCompletionCatalogSelection(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	testCases := []struct {
		name             string
		serviceAccount   bool
		allowedTool      string
		wantSACalls      []string
		wantUserCalls    []string
		wantToolAuthMode string
	}{
		{
			name:             "service account agent uses the agent bot's catalog",
			serviceAccount:   true,
			allowedTool:      saToolName,
			wantSACalls:      []string{testBotUserID},
			wantToolAuthMode: llm.ToolAuthModeServiceAccount,
		},
		{
			name:          "normal agent uses the requesting user's catalog",
			allowedTool:   userToolName,
			wantUserCalls: []string{testUserID},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			provider := e.setupBridgeMCPProviderSA(
				[]llm.Tool{bridgeMCPTool("mattermost", "user_tool", embeddedOrigin)},
				[]llm.Tool{bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin)},
			)
			e.setupBridgeCatalogBot(tc.serviceAccount)

			fakeLLM := NewFakeLLM("done")
			fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", tc.allowedTool, "done")
			e.setBridgeFakeLLM(fakeLLM)

			client := e.CreateBridgeClient()
			result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "use the tool"}},
				AllowedTools: []string{tc.allowedTool},
				UserID:       testUserID,
			})
			require.NoError(t, err)
			require.Equal(t, "done", result)

			require.Equal(t, tc.wantSACalls, provider.saCalls)
			require.Equal(t, tc.wantUserCalls, provider.userCalls)

			require.Len(t, fakeLLM.AllRequests, 2)
			require.Equal(t, 1, findAutoApprovedToolUse(fakeLLM.AllRequests[1], tc.allowedTool))

			llmContext := fakeLLM.LastRequest().Context
			require.NotNil(t, llmContext)
			require.Equal(t, testBotUserID, llmContext.BotUserID)
			require.Equal(t, "testbot", llmContext.BotUsername)
			require.Equal(t, "Test Bot", llmContext.BotName)
			require.Equal(t, tc.wantToolAuthMode, llmContext.ToolAuthMode)
			require.NotNil(t, llmContext.RequestingUser)
			require.Equal(t, testUserID, llmContext.RequestingUser.Id)
		})
	}
}

// With no service-account-credentialed servers the catalog is empty, and never falls
// back to the requesting user's catalog.
func TestBridgeSAAgentCompletionFailsClosedWithoutSAServers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// Non-empty user catalog proves fail-closed does not fall back to it.
	provider := e.setupBridgeMCPProviderSA(
		[]llm.Tool{bridgeMCPTool("mattermost", "user_tool", embeddedOrigin)},
		nil,
	)
	e.setupBridgeCatalogBot(true)

	fakeLLM := NewFakeLLM("done")
	e.setBridgeFakeLLM(fakeLLM)

	client := e.CreateBridgeClient()
	_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
		Posts:        []bridgeclient.Post{{Role: "user", Message: "use the tool"}},
		AllowedTools: []string{saToolName},
		UserID:       testUserID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eligible tools available for this agent")
	require.Empty(t, provider.userCalls, "must not consult the user catalog")
	require.Empty(t, fakeLLM.AllRequests, "must not call the LLM")
}

func TestBridgeGetAgentToolsUsesServiceAccountCatalog(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	provider := e.setupBridgeMCPProviderSA(
		[]llm.Tool{bridgeMCPTool("mattermost", "user_tool", embeddedOrigin)},
		[]llm.Tool{bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin)},
	)
	e.setupBridgeCatalogBot(true)

	client := e.CreateBridgeClient()
	tools, err := client.GetAgentTools(testBotUserID, testUserID)
	require.NoError(t, err)

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	require.Equal(t, []string{saToolName}, names)
	require.Equal(t, []string{testBotUserID}, provider.saCalls)
	require.Empty(t, provider.userCalls)
}
