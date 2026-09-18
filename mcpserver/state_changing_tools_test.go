// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/testhelpers"
)

func newTestInMemoryServer(t *testing.T, allowStateChangingTools func() bool) *mcpserver.MattermostInMemoryMCPServer {
	t.Helper()
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: "http://localhost:8065",
			DevMode:     false,
		},
	}
	server, err := mcpserver.NewInMemoryServer(config, &testLogger{t: t}, nil, nil, allowStateChangingTools)
	require.NoError(t, err)
	return server
}

func listInMemoryToolNames(t *testing.T, server *mcpserver.MattermostInMemoryMCPServer) (names []string, byName map[string]*mcp.Tool) {
	t.Helper()
	session := testhelpers.CreateTestMCPSession(t, server.GetMCPServer())
	t.Cleanup(func() { _ = session.Close() })

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, listed)

	byName = make(map[string]*mcp.Tool, len(listed.Tools))
	names = make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		byName[tool.Name] = tool
	}
	return names, byName
}

func callInMemoryTool(t *testing.T, server *mcpserver.MattermostInMemoryMCPServer, name string) *mcp.CallToolResult {
	t.Helper()
	session := testhelpers.CreateTestMCPSession(t, server.GetMCPServer())
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "tools/call must return an MCP result, not a transport error")
	require.NotNil(t, result)
	return result
}

func toolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text
}

func TestInMemoryServerStateChangingToolsByLicense(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			checker := enterprisetest.CheckerAt(level)
			allow := func() bool { return checker.Allows(enterprise.CapStateChangingTools) }
			server := newTestInMemoryServer(t, allow)

			names, byName := listInMemoryToolNames(t, server)
			require.NotEmpty(t, names)
			require.Contains(t, names, "read_post")
			require.Contains(t, names, "get_me")
			require.Contains(t, names, "search_posts")

			allowed := checker.Allows(enterprise.CapStateChangingTools)
			if allowed {
				require.Contains(t, names, "create_post")
				require.Contains(t, names, "add_reaction")
				require.Contains(t, names, "dm")
				createPost := byName["create_post"]
				require.NotNil(t, createPost.Annotations)
				assert.False(t, createPost.Annotations.ReadOnlyHint)
			} else {
				require.NotContains(t, names, "create_post")
				require.NotContains(t, names, "add_reaction")
				require.NotContains(t, names, "dm")
				require.NotContains(t, names, "upload_file")
				for _, tool := range byName {
					if tool.Annotations != nil {
						assert.True(t, tool.Annotations.ReadOnlyHint, "listed tool %q must be read-only below Enterprise", tool.Name)
					}
				}
			}

			readPost := byName["read_post"]
			require.NotNil(t, readPost.Annotations)
			assert.True(t, readPost.Annotations.ReadOnlyHint)
		})
	}
}

func TestInMemoryServerStateChangingToolsCall(t *testing.T) {
	t.Run("state-changing call below Enterprise returns license error", func(t *testing.T) {
		checker := enterprisetest.CheckerAt(enterprise.LevelProfessional)
		server := newTestInMemoryServer(t, func() bool { return checker.Allows(enterprise.CapStateChangingTools) })

		result := callInMemoryTool(t, server, "create_post")
		require.True(t, result.IsError)
		assert.Contains(t, toolResultText(t, result), "Enterprise")
		assert.Contains(t, toolResultText(t, result), "State-changing Mattermost tools")
	})

	t.Run("read-only call below Enterprise proceeds to the resolver", func(t *testing.T) {
		checker := enterprisetest.CheckerAt(enterprise.LevelProfessional)
		server := newTestInMemoryServer(t, func() bool { return checker.Allows(enterprise.CapStateChangingTools) })

		result := callInMemoryTool(t, server, "get_me")
		require.True(t, result.IsError)
		assert.NotContains(t, toolResultText(t, result), "Enterprise")
		assert.NotContains(t, toolResultText(t, result), "State-changing Mattermost tools")
	})

	t.Run("state-changing call at Enterprise proceeds to the resolver", func(t *testing.T) {
		checker := enterprisetest.CheckerAt(enterprise.LevelEnterprise)
		server := newTestInMemoryServer(t, func() bool { return checker.Allows(enterprise.CapStateChangingTools) })

		result := callInMemoryTool(t, server, "create_post")
		require.True(t, result.IsError)
		assert.NotContains(t, toolResultText(t, result), "State-changing Mattermost tools are available at Enterprise")
	})
}

func TestInMemoryServerNilPredicateFailsClosed(t *testing.T) {
	server := newTestInMemoryServer(t, nil)
	session := testhelpers.CreateTestMCPSession(t, server.GetMCPServer())
	t.Cleanup(func() { _ = session.Close() })

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "read_post")
	require.NotContains(t, names, "create_post")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_post",
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "tools/call must return an MCP result, not a transport error")
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(t, result), "Enterprise")
}
