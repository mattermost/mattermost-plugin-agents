// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBridgeServer(t *testing.T, agents []BridgeAgentInfo) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/plugins/mattermost-ai/bridge/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		// Verify inter-plugin auth header
		if r.Header.Get("Mattermost-Plugin-ID") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(BridgeAgentsResponse{Agents: agents})
	})

	return httptest.NewServer(mux)
}

func TestListAgents(t *testing.T) {
	sampleAgents := []BridgeAgentInfo{
		{ID: "bot1id12345678901234567", DisplayName: "Otto", Username: "otto", ServiceType: "openai", IsDefault: true},
		{ID: "bot2id12345678901234567", DisplayName: "Claude", Username: "claude", ServiceType: "anthropic", IsDefault: false},
	}

	ts := newTestBridgeServer(t, sampleAgents)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)

	t.Run("lists all agents", func(t *testing.T) {
		mcpCtx := &MCPToolContext{BotUserID: ""}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAgents(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "Otto")
		assert.Contains(t, result, "bot1id12345678901234567")
		assert.Contains(t, result, "Claude")
		assert.Contains(t, result, "bot2id12345678901234567")
		assert.Contains(t, result, "(default agent)")
		assert.Contains(t, result, "openai")
		assert.Contains(t, result, "anthropic")
	})

	t.Run("marks self agent", func(t *testing.T) {
		mcpCtx := &MCPToolContext{BotUserID: "bot1id12345678901234567"}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAgents(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "This is YOU")
	})

	t.Run("no agents configured", func(t *testing.T) {
		emptyTS := newTestBridgeServer(t, []BridgeAgentInfo{})
		defer emptyTS.Close()

		emptyProvider := newTestProvider(t, emptyTS.URL)
		mcpCtx := &MCPToolContext{}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := emptyProvider.toolListAgents(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "No agents")
	})

	t.Run("bridge unreachable", func(t *testing.T) {
		unreachableProvider := newTestProvider(t, "http://127.0.0.1:1")
		mcpCtx := &MCPToolContext{}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := unreachableProvider.toolListAgents(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "not reachable")
	})
}

func TestFetchBridgeAgents(t *testing.T) {
	t.Run("requires plugin ID header", func(t *testing.T) {
		// Server that rejects requests without the header
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Mattermost-Plugin-ID") == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BridgeAgentsResponse{Agents: []BridgeAgentInfo{
				{ID: "testid", DisplayName: "Test"},
			}})
		}))
		defer ts.Close()

		provider := newTestProvider(t, ts.URL)
		agents, err := provider.fetchBridgeAgents()
		require.NoError(t, err)
		require.Len(t, agents, 1)
		assert.Equal(t, "testid", agents[0].ID)
	})
}

func TestGetAgentToolsCount(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	agentTools := provider.getAgentTools()
	assert.Len(t, agentTools, 1)
	assert.Equal(t, "list_agents", agentTools[0].Name)
	assert.NotEmpty(t, agentTools[0].Description)
	assert.NotNil(t, agentTools[0].Schema)
	assert.NotNil(t, agentTools[0].Resolver)
}
