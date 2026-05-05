// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestUserClientsGetToolsNamespacesDuplicateBareNames(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"github": testClientWithTools("GitHub", "https://api.githubcopilot.com", "search"),
			"jira":   testClientWithTools("Jira", "https://mcp.atlassian.com", "search"),
		},
	}

	tools := userClients.GetTools()

	requireToolNames(t, tools, "github__search", "jira__search")
}

func TestUserClientsGetToolsResolverUsesBareToolName(t *testing.T) {
	server := newTestMCPServer(0, "search")
	session := connectInMemoryTestSession(t, server)
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"jira": {
				session: session,
				config:  ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
				tools: map[string]*gomcp.Tool{
					"search": {
						Name:        "search",
						Description: "Search Jira",
					},
				},
			},
		},
	}

	tools := userClients.GetTools()
	requireToolNames(t, tools, "jira__search")

	result, err := tools[0].Resolver(&llm.Context{}, func(args any) error {
		*(args.(*map[string]any)) = map[string]any{}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "search ok\n", result)
}

func TestUserClientsGetToolsEmbeddedToolNamesUseMattermostSlug(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			EmbeddedClientKey: testClientWithTools(EmbeddedClientKey, EmbeddedClientKey, "search_users"),
		},
	}

	tools := userClients.GetTools()

	requireToolNames(t, tools, "mattermost__search_users")
}

func TestUserClientsGetToolsDeterministicSlugCollision(t *testing.T) {
	userClients := &UserClients{
		userID: "user-id",
		clients: map[string]*Client{
			"server-a": testClientWithTools("Jira!", "https://a.example.com", "search"),
			"server-b": testClientWithTools("Jira", "https://b.example.com", "search"),
		},
	}
	expectedDedupedName := "jira_" + shortSlugHash("https://b.example.com") + "__search"

	first := userClients.GetTools()
	second := userClients.GetTools()

	requireToolNames(t, first, "jira__search", expectedDedupedName)
	requireToolNames(t, second, "jira__search", expectedDedupedName)
}

func TestUserClientsGetToolsPreservesRediscoveryBeforeRead(t *testing.T) {
	server := newTestMCPServer(0, "old_tool")
	session := connectInMemoryTestSession(t, server)
	client := &Client{
		session:    session,
		config:     ServerConfig{Name: "Jira", BaseURL: "https://mcp.atlassian.com", Enabled: true},
		tools:      make(map[string]*gomcp.Tool),
		toolsDirty: true,
		userID:     "user-id",
		log:        newTestLogService(),
	}
	userClients := &UserClients{
		userID: "user-id",
		log:    newTestLogService(),
		clients: map[string]*Client{
			"jira": client,
		},
	}

	addTestMCPTool(server, "new_tool")
	require.NoError(t, client.ensureDiscoveredTools(context.Background()))
	client.toolsMu.Lock()
	client.toolsDirty = true
	client.tools = make(map[string]*gomcp.Tool)
	client.toolsMu.Unlock()

	tools := userClients.GetTools()

	requireToolNames(t, tools, "jira__new_tool", "jira__old_tool")
}

func testClientWithTools(name, baseURL string, toolNames ...string) *Client {
	tools := make(map[string]*gomcp.Tool, len(toolNames))
	for _, toolName := range toolNames {
		tools[toolName] = &gomcp.Tool{
			Name:        toolName,
			Description: "Test tool " + toolName,
		}
	}
	return &Client{
		config: ServerConfig{
			Name:    name,
			BaseURL: baseURL,
			Enabled: true,
		},
		tools: tools,
	}
}
