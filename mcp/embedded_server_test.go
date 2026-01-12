// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedServer_InvalidSession tests that invalid session IDs are properly rejected
func TestEmbeddedServer_InvalidSession(t *testing.T) {
	suite := GetSharedTestSuite(t)
	suite.SetupEmbeddedServer()

	user, _ := suite.CreateUserAndSession(t)

	// Create token resolver that rejects all session IDs
	tokenResolver := func(sessionID string) (string, error) {
		return "", fmt.Errorf("invalid session ID")
	}

	// Attempt to create connection with invalid session resolver
	_, err := suite.embeddedServer.CreateConnectionForUser(
		user.Id,
		"invalid-session-id",
		tokenResolver,
	)

	// Should fail during session validation
	require.Error(t, err, "Should reject invalid session")
	assert.Contains(t, err.Error(), "invalid session", "Error should mention invalid session")
}

// TestEmbeddedServer_MissingSessionToken tests that missing/empty session tokens are rejected at connection time
func TestEmbeddedServer_MissingSessionToken(t *testing.T) {
	suite := GetSharedTestSuite(t)
	suite.SetupEmbeddedServer()

	user, session := suite.CreateUserAndSession(t)

	// Create token resolver that returns empty token
	tokenResolver := func(sessionID string) (string, error) {
		if sessionID == session.Id {
			return "", nil // Empty token
		}
		return "", fmt.Errorf("unknown session")
	}

	// Create connection - should FAIL because validation happens at connection time
	// When a sessionID and tokenResolver are provided, CreateConnectionForUser validates
	// the session immediately by calling GetMe() which will fail with an empty token
	_, err := suite.embeddedServer.CreateConnectionForUser(
		user.Id,
		session.Id,
		tokenResolver,
	)
	require.Error(t, err, "Connection creation should fail with empty token")
	assert.Contains(t, err.Error(), "invalid session token", "Error should mention invalid session")
}

// TestEmbeddedServer_MultipleConnectionsPerUser tests that a user can have multiple concurrent connections
func TestEmbeddedServer_MultipleConnectionsPerUser(t *testing.T) {
	suite := GetSharedTestSuite(t)
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Create two connections for the same user
	client1 := suite.CreateClient(t, user, session)
	defer client1.Close()

	client2 := suite.CreateClient(t, user, session)
	defer client2.Close()

	// Both clients should be able to call tools independently
	result1, err := client1.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "First client should work")
	assert.NotEmpty(t, result1)
	assert.Contains(t, result1, user.Username)

	result2, err := client2.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "Second client should work")
	assert.NotEmpty(t, result2)
	assert.Contains(t, result2, user.Username)
}

// TestEmbeddedServer_SessionLifecycle tests the full lifecycle of a session connection
// including auto-reconnect behavior
func TestEmbeddedServer_SessionLifecycle(t *testing.T) {
	suite := GetSharedTestSuite(t)
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// 1. Create connection
	client := suite.CreateClient(t, user, session)
	require.NotNil(t, client, "Client should be created")

	// 2. Use connection
	result, err := client.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "Tool call should work")
	assert.NotEmpty(t, result)
	assert.Contains(t, result, user.Username)

	// 3. Close connection
	err = client.Close()
	require.NoError(t, err, "Close should succeed")

	// 4. Verify auto-reconnect behavior - embedded clients automatically reconnect
	// This is by design in client.go lines 263-278: when ErrConnectionClosed is detected,
	// the client reconnects using the stored embeddedClient and sessionID
	result2, err := client.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "Tool call should succeed after auto-reconnect")
	assert.NotEmpty(t, result2)
	assert.Contains(t, result2, user.Username)

	t.Log("Successfully verified auto-reconnect behavior for embedded client")
}

// TestEmbeddedServer_TokenResolverCalledAsNeeded tests that the token resolver is called appropriately
func TestEmbeddedServer_TokenResolverCalledAsNeeded(t *testing.T) {
	suite := GetSharedTestSuite(t)
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Track how many times the resolver is called
	resolverCallCount := 0

	// Create token resolver that tracks calls
	tokenResolver := func(sessionID string) (string, error) {
		resolverCallCount++
		if sessionID == session.Id {
			return session.Token, nil
		}
		return "", fmt.Errorf("unknown session")
	}

	// Create connection
	clientTransport, err := suite.embeddedServer.CreateConnectionForUser(
		user.Id,
		session.Id,
		tokenResolver,
	)
	require.NoError(t, err)

	// Token resolver should be called during connection validation
	initialCallCount := resolverCallCount
	t.Logf("Token resolver called %d time(s) during connection", initialCallCount)
	require.Greater(t, initialCallCount, 0, "Token resolver should be called at least once")

	// Create MCP client
	mcpClient := mcp.NewClient(
		&mcp.Implementation{Name: "test", Version: "1.0"},
		&mcp.ClientOptions{},
	)

	mcpSession, err := mcpClient.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer mcpSession.Close()

	// Make a tool call - this should trigger additional token resolution
	_, err = mcpSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "search_users",
		Arguments: map[string]interface{}{
			"term": user.Username,
		},
	})
	require.NoError(t, err)

	finalCallCount := resolverCallCount
	t.Logf("Token resolver called %d time(s) total", finalCallCount)
	assert.Greater(t, finalCallCount, initialCallCount, "Token resolver should be called again for tool execution")
}
