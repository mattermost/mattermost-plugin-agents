// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationToolsValidation(t *testing.T) {
	provider := newTestProvider(t, "https://mm.example.com")
	client := newTestClient("https://mm.example.com")
	mcpCtx := &MCPToolContext{Client: client, Ctx: t.Context()}

	_, err := provider.toolGetBot(mcpCtx, GetBotArgs{BotUserID: "bad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a valid ID")
}

func TestToolGetBot(t *testing.T) {
	botID := model.NewId()
	ownerID := model.NewId()
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v4/bots/%s", botID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Bot{UserId: botID, Username: "helperbot", DisplayName: "Helper", OwnerId: ownerID})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolGetBot(mcpCtx, GetBotArgs{BotUserID: botID})
	require.NoError(t, err)
	assert.Contains(t, out, "helperbot")
	assert.Contains(t, out, "Helper")
}

func TestToolListBots(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/bots", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*model.Bot{{UserId: model.NewId(), Username: "bot1", OwnerId: model.NewId()}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	provider := newTestProvider(t, ts.URL)
	mcpCtx := &MCPToolContext{Client: newTestClient(ts.URL), Ctx: t.Context()}

	out, err := provider.toolListBots(mcpCtx, ListBotsArgs{})
	require.NoError(t, err)
	assert.Contains(t, out, "bot1")
}
