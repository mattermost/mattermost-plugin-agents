// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Bridge tests for service-account agents. Catalog selection itself lives in
// llmcontext (getToolsStoreForUser); these tests pin the behavior the bridge
// endpoints expose: the SA catalog is used regardless of user_id, fail-closed
// exclusions are unreachable from allowed_tools, discovery mirrors execution,
// tool_hooks are rejected, and bridge contexts carry bot identity.

const (
	// A second bot's user ID, used as a caller-asserted user_id on a normal
	// agent (bridge callers may legitimately act on behalf of a bot).
	testSecondBotUserID = "botb12345678901234567890ab"

	// Tools that exist only in one of the two catalogs, so a test can only
	// pass when the expected catalog is the one in effect.
	saToolName       = "mattermost__sa_tool"
	userToolName     = "mattermost__user_tool"
	excludedToolName = "remote__excluded_tool"

	excludedServerOrigin = "https://excluded.example.com"
)

// setupBridgeCatalogBot registers the single bridge test bot, optionally
// service-account flagged. AutoEnableNewMCPTools keeps every catalog tool in
// the store so allowed_tools resolution is the only filter under test.
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

// TestBridgeAgentCompletionCatalogSelection pins which catalog a bridge agent
// completion builds: the agent's service-account catalog for effectively-SA
// agents (independent of user_id), the requesting user's catalog otherwise.
func TestBridgeAgentCompletionCatalogSelection(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	testCases := []struct {
		name           string
		serviceAccount bool
		unlicensed     bool
		userID         string
		allowedTool    string
		wantSACalls    []string
		wantUserCalls  []string
	}{
		{
			name:           "service account agent uses the agent bot's catalog",
			serviceAccount: true,
			userID:         testUserID,
			allowedTool:    saToolName,
			wantSACalls:    []string{testBotUserID},
		},
		{
			name:           "service account catalog is identical for a different user_id",
			serviceAccount: true,
			userID:         testOtherUserID,
			allowedTool:    saToolName,
			wantSACalls:    []string{testBotUserID},
		},
		{
			name:          "normal agent uses the requesting user's catalog",
			userID:        testUserID,
			allowedTool:   userToolName,
			wantUserCalls: []string{testUserID},
		},
		{
			name:          "normal agent with a bot user_id stays on the user catalog",
			userID:        testSecondBotUserID,
			allowedTool:   userToolName,
			wantUserCalls: []string{testSecondBotUserID},
		},
		{
			name:           "unlicensed service account agent behaves as a normal agent",
			serviceAccount: true,
			unlicensed:     true,
			userID:         testUserID,
			allowedTool:    userToolName,
			wantUserCalls:  []string{testUserID},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			if tc.unlicensed {
				e.OverrideLicense(nil)
			}

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
				UserID:       tc.userID,
			})
			require.NoError(t, err)
			require.Equal(t, "done", result)

			require.Equal(t, tc.wantSACalls, provider.saCalls)
			require.Equal(t, tc.wantUserCalls, provider.userCalls)

			require.Len(t, fakeLLM.AllRequests, 2)
			require.Equal(t, 1, findAutoApprovedToolUse(fakeLLM.AllRequests[1], tc.allowedTool))
		})
	}
}

// TestBridgeSAAgentCompletionWithAndWithoutSAServers covers a service-account
// agent whose configured servers do and do not supply service-account
// credentials. With none, the catalog is empty and the request fails closed —
// it never falls back to the requesting user's catalog.
func TestBridgeSAAgentCompletionWithAndWithoutSAServers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	testCases := []struct {
		name          string
		withSAServers bool
		streaming     bool
		wantErr       string
	}{
		{
			name:          "with service account servers the tool auto-runs",
			withSAServers: true,
		},
		{
			name:    "without service account servers the request fails closed",
			wantErr: "no eligible tools available for this agent",
		},
		{
			name:          "streaming shares the same preparation path",
			withSAServers: true,
			streaming:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			var saResolverCalls int
			var saTools []llm.Tool
			if tc.withSAServers {
				saTools = []llm.Tool{bridgeMCPToolRecording("mattermost", "sa_tool", embeddedOrigin, &saResolverCalls)}
			}

			// A non-empty user catalog proves the fail-closed case does not
			// fall back to per-user OAuth tools.
			provider := e.setupBridgeMCPProviderSA(
				[]llm.Tool{bridgeMCPTool("mattermost", "user_tool", embeddedOrigin)},
				saTools,
			)
			e.setupBridgeCatalogBot(true)

			fakeLLM := NewFakeLLM("done")
			fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", saToolName, "done")
			e.setBridgeFakeLLM(fakeLLM)

			client := e.CreateBridgeClient()
			request := bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "use the tool"}},
				AllowedTools: []string{saToolName},
				UserID:       testUserID,
			}

			var result string
			var err error
			if tc.streaming {
				var stream *llm.TextStreamResult
				stream, err = client.AgentCompletionStream(testBotUserID, request)
				if err == nil {
					result, err = stream.ReadAll()
				}
			} else {
				result, err = client.AgentCompletion(testBotUserID, request)
			}

			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Empty(t, provider.userCalls, "must not consult the user catalog")
				require.Empty(t, fakeLLM.AllRequests, "must not call the LLM")
				require.Zero(t, saResolverCalls)
				return
			}

			require.NoError(t, err)
			require.Equal(t, "done", result)
			require.Equal(t, 1, saResolverCalls)
			require.Empty(t, provider.userCalls)
			require.Len(t, fakeLLM.AllRequests, 2)
			require.Equal(t, 1, findAutoApprovedToolUse(fakeLLM.AllRequests[1], saToolName))
		})
	}
}

// TestBridgeSAAllowlistReferencingExcludedServerNeverExecutes pins that a tool
// from a server excluded from the service-account catalog (no SA credentials
// configured) cannot be reached through allowed_tools, by either name form.
func TestBridgeSAAllowlistReferencingExcludedServerNeverExecutes(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	testCases := []struct {
		name         string
		allowedTools []string
	}{
		{
			name:         "namespaced name of the excluded tool",
			allowedTools: []string{excludedToolName},
		},
		{
			name:         "bare name of the excluded tool",
			allowedTools: []string{"excluded_tool"},
		},
		{
			name:         "one eligible tool plus the excluded tool",
			allowedTools: []string{saToolName, excludedToolName},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			var excludedResolverCalls int
			provider := e.setupBridgeMCPProviderSA(
				[]llm.Tool{
					bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin),
					bridgeMCPToolRecording("remote", "excluded_tool", excludedServerOrigin, &excludedResolverCalls),
				},
				[]llm.Tool{bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin)},
			)
			e.setupBridgeCatalogBot(true)

			fakeLLM := NewFakeLLM("unused")
			e.setBridgeFakeLLM(fakeLLM)

			client := e.CreateBridgeClient()
			_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "use the excluded tool"}},
				AllowedTools: tc.allowedTools,
				UserID:       testUserID,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "not eligible or not available for this agent")
			require.Zero(t, excludedResolverCalls)
			require.Empty(t, fakeLLM.AllRequests)
			require.Empty(t, provider.userCalls)
		})
	}
}

// TestBridgeGetAgentToolsServiceAccountMirrorsExecution pins that bridge
// discovery resolves the same catalog as execution, including for
// service-account agents called without user_id.
func TestBridgeGetAgentToolsServiceAccountMirrorsExecution(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	setupProvider := func(e *TestEnvironment) *fakeBridgeMCPToolProvider {
		return e.setupBridgeMCPProviderSA(
			[]llm.Tool{
				bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin),
				bridgeMCPTool("remote", "excluded_tool", excludedServerOrigin),
			},
			[]llm.Tool{bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin)},
		)
	}

	testCases := []struct {
		name           string
		serviceAccount bool
		userID         string
		wantNames      []string
		wantSACalls    []string
		wantUserCalls  []string
	}{
		{
			name:           "service account agent with user_id",
			serviceAccount: true,
			userID:         testUserID,
			wantNames:      []string{saToolName},
			wantSACalls:    []string{testBotUserID},
		},
		{
			name:           "service account agent without user_id",
			serviceAccount: true,
			userID:         "",
			wantNames:      []string{saToolName},
			wantSACalls:    []string{testBotUserID},
		},
		{
			name:      "normal agent without user_id has no catalog identity",
			userID:    "",
			wantNames: []string{},
		},
		{
			name:          "normal agent with user_id sees the full user catalog",
			userID:        testUserID,
			wantNames:     []string{saToolName, excludedToolName},
			wantUserCalls: []string{testUserID},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			provider := setupProvider(e)
			e.setupBridgeCatalogBot(tc.serviceAccount)

			client := e.CreateBridgeClient()
			tools, err := client.GetAgentTools(testBotUserID, tc.userID)
			require.NoError(t, err)

			names := make([]string, 0, len(tools))
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			require.Equal(t, tc.wantNames, names)
			require.Equal(t, tc.wantSACalls, provider.saCalls)
			require.Equal(t, tc.wantUserCalls, provider.userCalls)
		})
	}

	t.Run("every discovered tool is executable", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		setupProvider(e)
		e.setupBridgeCatalogBot(true)

		client := e.CreateBridgeClient()
		tools, err := client.GetAgentTools(testBotUserID, testUserID)
		require.NoError(t, err)
		require.NotEmpty(t, tools)

		discovered := make([]string, 0, len(tools))
		for _, tool := range tools {
			discovered = append(discovered, tool.Name)
		}

		fakeLLM := NewFakeLLM("done")
		fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", discovered[0], "done")
		e.setBridgeFakeLLM(fakeLLM)

		result, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
			Posts:        []bridgeclient.Post{{Role: "user", Message: "use the tools"}},
			AllowedTools: discovered,
			UserID:       testUserID,
		})
		require.NoError(t, err)
		require.Equal(t, "done", result)
	})
}

// TestPrepareAgentBridgeCompletionToolHooksRejectedForServiceAccountAgents pins
// that tool_hooks is rejected for effectively-SA agents before any hook key is
// issued, and that an unlicensed SA-flagged agent keeps working hooks.
func TestPrepareAgentBridgeCompletionToolHooksRejectedForServiceAccountAgents(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const bare = "search_posts"
	namespaced := llm.NamespaceMCPToolName("mattermost", bare)

	t.Run("licensed service account agent is rejected", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.setupBridgeMCPProviderSA(nil, []llm.Tool{bridgeMCPTool("mattermost", bare, embeddedOrigin)})
		e.setupBridgeCatalogBot(true)

		// No KVSetWithOptions expectation is registered: plugintest.API fails
		// the test if a before-hook key write is attempted.
		_, _, _, _, beforeHookKeys, statusCode, err := e.api.prepareAgentBridgeCompletion(
			context.Background(),
			testBotUserID,
			bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "Hi"}},
				AllowedTools: []string{bare},
				UserID:       testUserID,
				ToolHooks: map[string]bridgeclient.ToolHookConfig{
					bare: {BeforeCallback: "/hooks/before"},
				},
			},
			"com.example.caller",
			llm.OperationBridgeAgent,
			llm.SubTypeNoStream,
		)
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, statusCode)
		require.Contains(t, err.Error(), "tool_hooks is not supported for agents using service account authentication")
		require.Empty(t, beforeHookKeys)
	})

	t.Run("unlicensed service account agent keeps working hooks", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)

		e.OverrideLicense(nil)
		e.setupBridgeMCPProviderSA([]llm.Tool{bridgeMCPTool("mattermost", bare, embeddedOrigin)}, nil)
		e.setupBridgeCatalogBot(true)

		var storedKey string
		var storedEntry mcp.BeforeHookEntry
		e.mockAPI.On(
			"KVSetWithOptions",
			mock.MatchedBy(func(key string) bool {
				storedKey = key
				return strings.HasPrefix(key, "beforeHook:")
			}),
			mock.MatchedBy(func(data []byte) bool {
				if err := json.Unmarshal(data, &storedEntry); err != nil {
					return false
				}
				return storedEntry.ToolName == bare
			}),
			mock.MatchedBy(func(opts model.PluginKVSetOptions) bool {
				return opts.ExpireInSeconds == int64(mcp.BeforeHookKeyTTL.Seconds())
			}),
		).Return(true, (*model.AppError)(nil)).Once()

		_, llmRequest, _, _, beforeHookKeys, statusCode, err := e.api.prepareAgentBridgeCompletion(
			context.Background(),
			testBotUserID,
			bridgeclient.CompletionRequest{
				Posts:        []bridgeclient.Post{{Role: "user", Message: "Hi"}},
				AllowedTools: []string{bare},
				UserID:       testUserID,
				ToolHooks: map[string]bridgeclient.ToolHookConfig{
					bare: {BeforeCallback: "/hooks/before"},
				},
			},
			"com.example.caller",
			llm.OperationBridgeAgent,
			llm.SubTypeNoStream,
		)
		require.NoError(t, err)
		require.Equal(t, 0, statusCode)
		require.Equal(t, []string{storedKey}, beforeHookKeys)
		require.Equal(t, testUserID, storedEntry.UserID)

		scopedTool := llmRequest.Context.Tools.GetTool(namespaced)
		require.NotNil(t, scopedTool)
		hooks, ok := scopedTool.CallMetadata["tool_hooks"].(map[string]any)
		require.True(t, ok)
		entry, ok := hooks[bare].(map[string]any)
		require.True(t, ok, "tool_hooks metadata must be keyed by the bare name")
		require.Equal(t, storedKey, entry["before_hook_key"])
	})
}

// TestBridgeAgentCompletionContextCarriesBotIdentity pins that bridge agent
// completions hand the LLM a context with the agent's bot identity, so token
// usage attribution resolves the acting identity instead of "unknown".
func TestBridgeAgentCompletionContextCarriesBotIdentity(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	testCases := []struct {
		name             string
		serviceAccount   bool
		allowedTool      string
		wantToolAuthMode string
	}{
		{
			name: "plain completion without tools",
		},
		{
			name:             "service account agent with tools",
			serviceAccount:   true,
			allowedTool:      saToolName,
			wantToolAuthMode: llm.ToolAuthModeServiceAccount,
		},
		{
			name:        "normal agent with tools",
			allowedTool: userToolName,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.setupBridgeMCPProviderSA(
				[]llm.Tool{bridgeMCPTool("mattermost", "user_tool", embeddedOrigin)},
				[]llm.Tool{bridgeMCPTool("mattermost", "sa_tool", embeddedOrigin)},
			)
			e.setupBridgeCatalogBot(tc.serviceAccount)

			fakeLLM := NewFakeLLM("done")
			request := bridgeclient.CompletionRequest{
				Posts:  []bridgeclient.Post{{Role: "user", Message: "hello"}},
				UserID: testUserID,
			}
			if tc.allowedTool != "" {
				fakeLLM.StreamEventSequence = fakeLLMAutoRunSequence("tc1", tc.allowedTool, "done")
				request.AllowedTools = []string{tc.allowedTool}
			}
			e.setBridgeFakeLLM(fakeLLM)

			client := e.CreateBridgeClient()
			_, err := client.AgentCompletion(testBotUserID, request)
			require.NoError(t, err)

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
