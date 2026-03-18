// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAIBotsServer(t *testing.T, bots []AIBotInfo) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/plugins/mattermost-ai/ai_bots", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AIBotsResponse{Bots: bots})
	})

	return httptest.NewServer(mux)
}

func newTestClient4(serverURL string) *model.Client4 {
	client := model.NewAPIv4Client(serverURL)
	client.AuthToken = "test-token"
	return client
}

func TestListAgents(t *testing.T) {
	sampleBots := []AIBotInfo{
		{ID: "bot1id12345678901234567", DisplayName: "Otto", Username: "otto"},
		{ID: "bot2id12345678901234567", DisplayName: "Claude", Username: "claude"},
	}

	ts := newTestAIBotsServer(t, sampleBots)
	defer ts.Close()

	t.Run("marks self agent", func(t *testing.T) {
		provider := newTestProvider(t, ts.URL)
		mcpCtx := &MCPToolContext{BotUserID: "bot1id12345678901234567", Client: newTestClient4(ts.URL)}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAgents(mcpCtx, argsGetter)
		require.NoError(t, err)
		assert.Contains(t, result, "This is YOU")
	})

	t.Run("unreachable server", func(t *testing.T) {
		provider := newTestProvider(t, "http://127.0.0.1:1")
		mcpCtx := &MCPToolContext{Client: newTestClient4("http://127.0.0.1:1")}
		argsGetter := func(target any) error {
			return json.Unmarshal([]byte(`{}`), target)
		}

		result, err := provider.toolListAgents(mcpCtx, argsGetter)
		require.Error(t, err)
		assert.Contains(t, result, "not reachable")
	})
}
