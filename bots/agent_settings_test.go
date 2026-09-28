// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// newModelRecordingAnthropicServer answers every Messages call with a short
// streamed reply and records the model each request asked for.
func newModelRecordingAnthropicServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		mu.Lock()
		models = append(models, req.Model)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			fmt.Sprintf(`{"type":"message_start","message":{"model":%q,"id":"msg","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, req.Model),
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", event)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), models...)
	}
}

// TestEnsureBotsAgentsOnSharedClientSendOwnSettings drives two agents on one
// real Bifrost client and checks each request carries its own agent's model,
// so sharing the client neither drops an agent's settings nor leaks them to
// another agent.
func TestEnsureBotsAgentsOnSharedClientSendOwnSettings(t *testing.T) {
	srv, requestedModels := newModelRecordingAnthropicServer(t)

	agents := dbAgents(2, "anthropic-svc")
	agents[0].Model = "claude-opus-4-1"
	store := &stubAgentStore{agents: agents}
	cfg := &mockConfig{services: []llm.ServiceConfig{{
		ID:           "anthropic-svc",
		Name:         "Anthropic",
		Type:         llm.ServiceTypeAnthropic,
		APIKey:       "anthropic-key",
		APIURL:       srv.URL,
		DefaultModel: "claude-sonnet-4-6",
	}}}
	mmBots := newEnsureBotsHarness(t, cfg, store, enterpriseAdvancedLicense())
	require.NoError(t, mmBots.EnsureBots())

	for _, name := range []string{"agent1", "agent2"} {
		bot := mmBots.GetBotByUsername(name)
		require.NotNil(t, bot, name)
		answer, err := bot.LLM().ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
			Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
			Context: &llm.Context{RequestingUser: &model.User{Id: model.NewId()}},
		})
		require.NoError(t, err, name)
		require.Equal(t, "ok", answer, name)
	}

	require.Equal(t, []string{"claude-opus-4-1", "claude-sonnet-4-6"}, requestedModels())
}
