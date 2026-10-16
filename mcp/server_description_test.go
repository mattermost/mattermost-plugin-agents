// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestDescribeUnconnectedMCPServers(t *testing.T) {
	const jiraURL = "https://jira.example.com/mcp"

	toolConfigs := []ToolConfig{
		{Name: "delete_issue", Policy: ToolPolicyAsk, Enabled: false},
		{Name: "search", Policy: ToolPolicyAsk, Enabled: true, RetrievalDescriptionOverride: "Find\nJira issues"},
		{Name: "create_issue", Policy: ToolPolicyAsk, Enabled: true},
	}

	tests := []struct {
		name       string
		server     ServerConfig
		authErrors []llm.ToolAuthError
		want       []llm.UnconnectedMCPServer
	}{
		{
			name:   "uses the admin description and configured tools without disabled ones",
			server: ServerConfig{Name: "Jira", BaseURL: jiraURL, Enabled: true, Description: "Issues and\nsprints", ToolConfigs: toolConfigs},
			want: []llm.UnconnectedMCPServer{{
				Name:         "Jira",
				ServerOrigin: jiraURL,
				Description:  "Issues and sprints",
				Tools: []llm.ToolInfo{
					{Name: "create_issue", ServerOrigin: jiraURL},
					{Name: "search", Description: "Find Jira issues", ServerOrigin: jiraURL},
				},
			}},
		},
		{
			name:   "a server without admin metadata is described by name only",
			server: ServerConfig{Name: "Jira", BaseURL: jiraURL, Enabled: true},
			want:   []llm.UnconnectedMCPServer{{Name: "Jira", ServerOrigin: jiraURL}},
		},
		{
			name:   "deduplicates servers and keeps ones missing from config by name",
			server: ServerConfig{Name: "Jira", BaseURL: jiraURL, Enabled: true},
			authErrors: []llm.ToolAuthError{
				{ServerName: "Jira", ServerOrigin: jiraURL},
				{ServerName: "Jira", ServerOrigin: jiraURL},
				{ServerName: "Removed", ServerOrigin: "https://removed.example.com"},
			},
			want: []llm.UnconnectedMCPServer{
				{Name: "Jira", ServerOrigin: jiraURL},
				{Name: "Removed", ServerOrigin: "https://removed.example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &ClientManager{config: Config{Enabled: true, Servers: []ServerConfig{tt.server}}}

			authErrors := tt.authErrors
			if authErrors == nil {
				authErrors = []llm.ToolAuthError{{ServerName: tt.server.Name, ServerOrigin: tt.server.BaseURL}}
			}
			assert.Equal(t, tt.want, manager.DescribeUnconnectedMCPServers(authErrors))
		})
	}
}

// Another user's connection fills the shared tools cache, but nothing the
// server reported to that connection may describe the server to users who
// have not connected.
func TestDescribeUnconnectedMCPServersIgnoresWhatServersReport(t *testing.T) {
	const (
		serverDescription = "SERVER-SELF-DESCRIPTION"
		toolDescription   = "SERVER-TOOL-DESCRIPTION"
		instructions      = "SERVER-INSTRUCTIONS"
	)

	server := mcp.NewServer(&mcp.Implementation{Name: "tickets", Version: "1.0", Title: "SERVER-TITLE", Description: serverDescription},
		&mcp.ServerOptions{Instructions: instructions})
	server.AddTool(&mcp.Tool{Name: "search", Description: toolDescription, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(httpServer.Close)

	serverConfig := ServerConfig{Name: "Tickets", BaseURL: httpServer.URL, Enabled: true, Description: "Admin-written description"}
	cache := newTestToolsCache()
	client, err := newClient(context.Background(), "connected-user", serverConfig, clientParams{
		log:          newTestLogService(),
		oauthManager: newTestOAuthManager(),
		httpClient:   httpServer.Client(),
		toolsCache:   cache,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NotEmpty(t, cache.GetTools(serverConfig.Name), "the connection should have filled the shared tools cache")

	manager := &ClientManager{config: Config{Enabled: true, Servers: []ServerConfig{serverConfig}}, toolsCache: cache}
	described := manager.DescribeUnconnectedMCPServers([]llm.ToolAuthError{{ServerName: serverConfig.Name, ServerOrigin: serverConfig.BaseURL}})

	assert.Equal(t, []llm.UnconnectedMCPServer{{Name: "Tickets", ServerOrigin: httpServer.URL, Description: "Admin-written description"}}, described)
	raw, err := json.Marshal(described)
	require.NoError(t, err)
	for _, reported := range []string{serverDescription, toolDescription, instructions, "SERVER-TITLE"} {
		assert.NotContains(t, string(raw), reported)
	}
}

func TestPromptSafeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{name: "collapses newlines and runs of spaces", input: "  Search\n\n  issues\tfast ", maxRunes: 100, want: "Search issues fast"},
		{name: "escapes invisible spoofing characters", input: "Jira\u202eCloud", maxRunes: 100, want: "Jira[U+202E]Cloud"},
		{name: "truncates long text with an ellipsis", input: "abcdefghij", maxRunes: 5, want: "abcd…"},
		{name: "keeps text at the limit", input: "abcde", maxRunes: 5, want: "abcde"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, promptSafeText(tt.input, tt.maxRunes))
		})
	}
}

func TestConfiguredServerToolsCapsListSize(t *testing.T) {
	server := &ServerConfig{Name: "Big", BaseURL: "https://big.example.com", Enabled: true}
	for i := range maxKnownToolsPerServer + 10 {
		server.ToolConfigs = append(server.ToolConfigs, ToolConfig{Name: "tool_" + strings.Repeat("x", i), Policy: ToolPolicyAsk, Enabled: true})
	}

	assert.Len(t, configuredServerTools(server), maxKnownToolsPerServer)
}
