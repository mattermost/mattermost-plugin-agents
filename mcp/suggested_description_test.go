// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

func TestSuggestRemoteServerDescription(t *testing.T) {
	longText := strings.Repeat("word ", config.MaxMCPServerDescriptionRunes)

	tests := []struct {
		name         string
		description  string
		instructions string
		requireAuth  bool
		wantText     string
		wantSource   string
		wantOAuth    bool
	}{
		{
			name:         "uses the server's own description",
			description:  "Tickets and\nsprints",
			instructions: "Call search first.",
			wantText:     "Tickets and sprints",
			wantSource:   SuggestedDescriptionSourceDescription,
		},
		{
			name:         "falls back to the server's instructions",
			instructions: "Search and update tickets.\nAlways confirm before closing.",
			wantText:     "Search and update tickets. Always confirm before closing.",
			wantSource:   SuggestedDescriptionSourceInstructions,
		},
		{
			name: "returns nothing when the server describes itself in neither way",
		},
		{
			name:        "caps long text to the configured limit",
			description: longText,
			wantSource:  SuggestedDescriptionSourceDescription,
		},
		{
			name:        "reports that the user must sign in first",
			description: "Tickets",
			requireAuth: true,
			wantOAuth:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "tickets", Version: "1.0", Description: tt.description},
				&mcp.ServerOptions{Instructions: tt.instructions})
			addTestMCPTool(server, "search")
			streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.requireAuth && r.Header.Get("Authorization") == "" {
					w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				streamable.ServeHTTP(w, r)
			}))
			t.Cleanup(httpServer.Close)

			got, err := SuggestRemoteServerDescription(context.Background(), "admin-id",
				ServerConfig{Name: "Tickets", BaseURL: httpServer.URL, Enabled: true},
				newTestLogService(), newTestOAuthManager(), httpServer.Client())

			if tt.wantOAuth {
				_, isOAuth := errors.AsType[*OAuthNeededError](err)
				require.True(t, isOAuth, "expected an OAuthNeededError, got %v", err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSource, got.Source)
			if tt.wantText != "" {
				assert.Equal(t, tt.wantText, got.Text)
			}
			assert.LessOrEqual(t, utf8.RuneCountInString(got.Text), config.MaxMCPServerDescriptionRunes)
			if tt.wantSource == "" {
				assert.Empty(t, got.Text)
			}
		})
	}
}
