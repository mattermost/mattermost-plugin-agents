// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClient_CreateClient tests creating a client and verifying its properties
func TestClient_CreateClient(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Verify client was created with correct properties
	assert.NotNil(t, client.session, "Client should have a session")
	assert.NotEmpty(t, client.Tools(), "Client should have tools")
	assert.Equal(t, user.Id, client.userID, "Client should have correct user ID")
	assert.Equal(t, session.Id, client.sessionID, "Client should have correct session ID")
}

// TestClient_CreateClient_InvalidSession tests session validation in CreateClient
func TestClient_CreateClient_InvalidSession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()
	user, _ := suite.CreateUserAndSession(t)

	// Create mock plugin API WITHOUT adding the session (so Get will fail)
	mockPluginAPI := newMockPluginAPI()

	// Create a real pluginapi.Client to get a proper LogService
	pluginAPIClient := pluginapi.NewClient(mockPluginAPI, nil)

	// Create wrapper
	wrapper := &embeddedServerWrapper{
		server: suite.embeddedServer,
		api:    mockPluginAPI,
	}

	// Create embedded server client
	embeddedClient := NewEmbeddedServerClient(wrapper, pluginAPIClient.Log, pluginAPIClient)

	// Attempt to create client with invalid session - should fail
	_, err := embeddedClient.CreateClient(ctx, user.Id, "invalid-session-id")
	require.Error(t, err, "Should fail with invalid session")
	assert.Contains(t, err.Error(), "session", "Error should mention session issue")
}

// TestClient_CallTool_ReadChannel tests the Client.CallTool() method with proper output validation
func TestClient_CallTool_ReadChannel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	// Setup test data
	user, session := suite.CreateUserAndSession(t)
	teams, _, err := suite.adminClient.GetTeamsForUser(ctx, user.Id, "")
	require.NoError(t, err)
	require.NotEmpty(t, teams)

	channel := suite.CreateChannel(t, teams[0].Id, user.Id)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Call tool
	result, err := client.CallTool(ctx, "read_channel", map[string]any{
		"channel_id": channel.Id,
	})
	require.NoError(t, err, "CallTool should succeed")

	// Validate output content based on what read_channel tool returns
	assert.NotEmpty(t, result, "Result should not be empty")
	assert.Contains(t, result, "Found", "Result should contain 'Found' (part of output format)")
	assert.Contains(t, result, "posts in channel", "Result should mention posts in channel")

	t.Logf("Tool result:\n%s", result)
}

// TestClient_CallTool_SearchUsers tests search_users with output validation
func TestClient_CallTool_SearchUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Call tool
	result, err := client.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "CallTool should succeed")

	// Validate output content
	assert.NotEmpty(t, result, "Result should not be empty")
	assert.Contains(t, result, user.Username, "Result should contain username")
	assert.Contains(t, result, user.Email, "Result should contain email")

	t.Logf("Search results:\n%s", result)
}

// TestClient_CallTool_ReadPost tests read_post with proper output validation
func TestClient_CallTool_ReadPost(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	// Setup test data
	user, session := suite.CreateUserAndSession(t)
	teams, _, err := suite.adminClient.GetTeamsForUser(ctx, user.Id, "")
	require.NoError(t, err)
	require.NotEmpty(t, teams)

	channel := suite.CreateChannel(t, teams[0].Id, user.Id)
	testMessage := "Test message content for validation"
	post := suite.CreatePost(t, channel.Id, user.Id, testMessage)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Call tool
	result, err := client.CallTool(ctx, "read_post", map[string]any{
		"post_id": post.Id,
	})
	require.NoError(t, err, "CallTool should succeed")

	// Validate output content
	assert.NotEmpty(t, result, "Result should not be empty")
	assert.Contains(t, result, post.Id, "Result should contain post ID")
	assert.Contains(t, result, testMessage, "Result should contain post message")
	// Note: Post was created by admin client, so author will be "admin"
	assert.Contains(t, result, "admin", "Result should contain author username (admin)")

	t.Logf("Post content:\n%s", result)
}

// TestClient_CallTool_InvalidToolName tests error handling for non-existent tools
func TestClient_CallTool_InvalidToolName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Call non-existent tool
	_, err := client.CallTool(ctx, "non_existent_tool", map[string]any{})
	require.Error(t, err, "Should fail with non-existent tool")
}

// TestClient_CallTool_InvalidArguments tests error handling for invalid arguments
func TestClient_CallTool_InvalidArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Call tool with missing required argument
	result, err := client.CallTool(ctx, "read_channel", map[string]any{
		// Missing channel_id
	})

	// Tool might return error or error content
	if err != nil {
		assert.Contains(t, err.Error(), "channel_id", "Error should mention missing channel_id")
	} else {
		// Some tools return error content instead of error
		assert.Contains(t, strings.ToLower(result), "channel_id", "Result should mention missing channel_id")
	}
}

// TestClient_Reconnection tests the automatic reconnection logic in Client.CallTool()
func TestClient_Reconnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	ctx := context.Background()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// First call should work
	result1, err := client.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "First call should succeed")
	assert.Contains(t, result1, user.Username, "First result should contain username")

	// Close the underlying session to simulate connection loss
	client.session.Close()

	// Second call should automatically reconnect and succeed
	// This tests the reconnection logic in Client.CallTool()
	result2, err := client.CallTool(ctx, "search_users", map[string]any{
		"term": user.Username,
	})
	require.NoError(t, err, "Second call should succeed after reconnection")
	assert.Contains(t, result2, user.Username, "Second result should contain username")

	t.Log("Successfully reconnected and executed tool after connection loss")
}

// TestClient_Tools tests that Client.Tools() returns the correct tool list
func TestClient_Tools(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	user, session := suite.CreateUserAndSession(t)

	// Create client
	client := suite.CreateClient(t, user, session)
	defer client.Close()

	// Get tools using Client.Tools() method
	tools := client.Tools()
	require.NotEmpty(t, tools, "Should have tools")

	// Verify expected tools are present
	expectedTools := []string{
		"read_channel",
		"get_channel_info",
		"search_posts",
		"search_users",
		"read_post",
	}

	toolNames := make(map[string]bool)
	for name := range tools {
		toolNames[name] = true
	}

	for _, expected := range expectedTools {
		assert.True(t, toolNames[expected], "Should have tool: %s", expected)
	}

	// Verify each tool has proper metadata
	for name, tool := range tools {
		assert.NotEmpty(t, tool.Description, "Tool %s should have description", name)
		assert.NotNil(t, tool.InputSchema, "Tool %s should have input schema", name)
	}
}

// TestClientManager_GetToolsForUser tests the ClientManager.GetToolsForUser() method
func TestClientManager_GetToolsForUser(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	suite := SetupEmbeddedTestSuite(t)
	defer suite.TearDown()
	suite.SetupEmbeddedServer()

	user, session := suite.CreateUserAndSession(t)

	// Create ClientManager
	manager := suite.CreateClientManager(t, session)
	defer manager.Close()

	// Call GetToolsForUser
	tools, errors := manager.GetToolsForUser(user.Id)

	// Should succeed with no errors
	assert.Nil(t, errors, "Should have no errors")
	require.NotEmpty(t, tools, "Should have tools")

	// Verify tools structure (llm.Tool format)
	for _, tool := range tools {
		assert.NotEmpty(t, tool.Name, "Tool should have name")
		assert.NotEmpty(t, tool.Description, "Tool should have description")
		assert.NotNil(t, tool.Schema, "Tool should have schema")
		assert.NotNil(t, tool.Resolver, "Tool should have resolver function")
	}

	t.Logf("GetToolsForUser returned %d tools", len(tools))
}
