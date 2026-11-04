// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

func TestHandleGetMCPSuggestedDescription(t *testing.T) {
	const serverText = "SERVER-SUPPLIED-ERROR-DETAIL"

	describing := gomcp.NewServer(&gomcp.Implementation{Name: "tickets", Version: "1.0", Description: "Search and update support tickets."}, nil)
	describing.AddTool(&gomcp.Tool{Name: "search", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			return &gomcp.CallToolResult{}, nil
		})
	describingHTTP := httptest.NewServer(gomcp.NewStreamableHTTPHandler(
		func(*http.Request) *gomcp.Server { return describing },
		&gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	t.Cleanup(describingHTTP.Close)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, serverText, http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)

	tests := []struct {
		name            string
		serverID        string
		wantStatus      int
		wantDescription string
	}{
		{name: "drafts the description the server reports", serverID: "tickets-id", wantStatus: http.StatusOK, wantDescription: "Search and update support tickets."},
		{name: "unknown server is not found", serverID: "missing-id", wantStatus: http.StatusNotFound},
		{name: "unreachable server fails without echoing server text", serverID: "broken-id", wantStatus: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			allowAnyPluginAPILogging(mockAPI)
			api.config.(*testConfigImpl).mcpConfig = mcp.Config{
				Enabled: true,
				Servers: []mcp.ServerConfig{
					{ID: "tickets-id", Name: "Tickets", BaseURL: describingHTTP.URL, Enabled: true},
					{ID: "broken-id", Name: "Broken", BaseURL: broken.URL, Enabled: true},
				},
			}
			api.mcpClientManager.(*mockMCPClientManager).httpClient = &http.Client{}

			req := httptest.NewRequest(http.MethodGet, "/admin/mcp/"+tt.serverID+"/suggested-description", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")
			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, resp.StatusCode, string(body))
			assert.NotContains(t, string(body), serverText)
			if tt.wantStatus != http.StatusOK {
				return
			}

			var got MCPSuggestedDescriptionResponse
			require.NoError(t, json.Unmarshal(body, &got))
			assert.Equal(t, tt.wantDescription, got.Description)
			assert.Equal(t, mcp.SuggestedDescriptionSourceDescription, got.Source)
			assert.False(t, got.NeedsOAuth)
		})
	}
}
