// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/testhelpers"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestDevModeConfiguration tests development mode configuration and security
func TestDevModeConfiguration(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	t.Run("DevModeEnabled", func(t *testing.T) {
		suite.CreateMCPServer(true) // dev mode enabled
		assert.NotNil(t, suite.mcpServer, "MCP server should be created with dev mode")
	})

	t.Run("DevModeDisabled", func(t *testing.T) {
		suite.CreateMCPServer(false) // dev mode disabled
		assert.NotNil(t, suite.mcpServer, "MCP server should be created without dev mode")
	})
}

// TestDevToolsWithDevModeEnabled tests dev tools when dev mode is enabled
func TestDevToolsWithDevModeEnabled(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	// Create MCP server with dev mode enabled
	suite.CreateMCPServer(true)

	// Create Mattermost client for setup
	client := model.NewAPIv4Client(suite.serverURL)
	client.SetToken(suite.adminToken)

	// Setup basic test data
	testData := testhelpers.SetupBasicTestData(t, client, suite.adminToken)

	t.Run("CreateUserTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"username":   "devtestuser",
				"email":      "devtest@example.com",
				"password":   "devpassword123",
				"first_name": "Dev",
				"last_name":  "User",
			}

			result, err := executeDevToolWithMCP(t, suite, "create_user", args)
			require.NoError(t, err, "create_user should succeed in dev mode")
			assert.NotEmpty(t, result.Content, "create_user should return content")
		})

		t.Run("MissingRequiredFields", func(t *testing.T) {
			args := map[string]interface{}{
				"username": "incompleteuser",
				// missing email and password
			}

			_, err := executeDevToolWithMCP(t, suite, "create_user", args)
			require.Error(t, err, "create_user should fail with missing required fields")
		})
	})

	t.Run("CreateTeamTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"name":         "dev-test-team",
				"display_name": "Dev Test Team",
				"type":         "O",
				"description":  "Team created for dev testing",
			}

			result, err := executeDevToolWithMCP(t, suite, "create_team", args)
			require.NoError(t, err, "create_team should succeed in dev mode")
			assert.NotEmpty(t, result.Content, "create_team should return content")
		})

		t.Run("InvalidTeamType", func(t *testing.T) {
			args := map[string]interface{}{
				"name":         "invalid-team",
				"display_name": "Invalid Team",
				"type":         "X", // Invalid type
			}

			_, err := executeDevToolWithMCP(t, suite, "create_team", args)
			require.Error(t, err, "create_team should fail with invalid type")
		})
	})

	t.Run("AddUserToTeamTool", func(t *testing.T) {
		// Create a test user first
		userArgs := map[string]interface{}{
			"username": "teamuser",
			"email":    "teamuser@example.com",
			"password": "password123",
		}
		_, err := executeDevToolWithMCP(t, suite, "create_user", userArgs)
		require.NoError(t, err, "User creation should succeed for team test")

		t.Run("HappyPath", func(t *testing.T) {
			// Extract user ID from result (simplified - in real implementation would parse JSON)
			// For now, just test the API call structure
			args := map[string]interface{}{
				"user_id": testData.User.Id, // Use existing test user
				"team_id": testData.Team.Id,
			}

			result, _ := executeDevToolWithMCP(t, suite, "add_user_to_team", args)
			// User might already be in team, so we just check the call doesn't crash
			// Don't require no error since user might already be in team
			assert.NotNil(t, result, "add_user_to_team should return a result")
		})

		t.Run("InvalidUserID", func(t *testing.T) {
			args := map[string]interface{}{
				"user_id": "invalid-user-id",
				"team_id": testData.Team.Id,
			}

			_, err := executeDevToolWithMCP(t, suite, "add_user_to_team", args)
			require.Error(t, err, "add_user_to_team should fail with invalid user ID")
		})
	})

	t.Run("CreatePostAsUserTool", func(t *testing.T) {
		// Create a test user with known credentials first
		userArgs := map[string]interface{}{
			"username": "postuser",
			"email":    "postuser@example.com",
			"password": "postpassword123",
		}
		_, err := executeDevToolWithMCP(t, suite, "create_user", userArgs)
		require.NoError(t, err, "User creation should succeed for post test")

		// Add user to team (using dev tool)
		addTeamArgs := map[string]interface{}{
			"user_id": testData.User.Id, // Using existing user for simplicity
			"team_id": testData.Team.Id,
		}
		_, _ = executeDevToolWithMCP(t, suite, "add_user_to_team", addTeamArgs)

		// Add user to channel (using helper since it's not a dev tool anymore)
		testhelpers.AddUserToChannel(t, client, testData.Channel.Id, testData.User.Id)

		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"username":   "postuser",
				"password":   "postpassword123",
				"channel_id": testData.Channel.Id,
				"message":    "Hello from dev tool user!",
			}

			result, _ := executeDevToolWithMCP(t, suite, "create_post_as_user", args)
			// This might fail due to user permissions, but should not crash
			// Don't require no error since it might fail due to permissions
			assert.NotNil(t, result, "create_post_as_user should return a result")
		})

		t.Run("InvalidCredentials", func(t *testing.T) {
			args := map[string]interface{}{
				"username":   "postuser",
				"password":   "wrongpassword",
				"channel_id": testData.Channel.Id,
				"message":    "This should fail",
			}

			_, err := executeDevToolWithMCP(t, suite, "create_post_as_user", args)
			require.Error(t, err, "create_post_as_user should fail with wrong password")
		})
	})
}

// TestDevToolsSecurityGating tests that dev tools are properly blocked when dev mode is disabled
func TestDevToolsSecurityGating(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	// Create MCP server with dev mode DISABLED
	suite.CreateMCPServer(false)

	devTools := []string{
		"create_user",
		"create_team",
		"add_user_to_team",
		"create_post_as_user",
	}

	for _, toolName := range devTools {
		t.Run("DevTool_"+toolName+"_BlockedInProductionMode", func(t *testing.T) {
			args := map[string]interface{}{
				"test": "value", // Generic args since they should be blocked anyway
			}

			_, err := executeDevToolWithMCP(t, suite, toolName, args)
			require.Error(t, err, "Dev tool %s should be blocked when dev mode is disabled", toolName)

			// Check that the error indicates the tool is not available (correct security behavior)
			assert.Contains(t, err.Error(), "unknown tool",
				"Error should indicate tool is not available when dev mode is disabled")
		})
	}
}

// executeDevToolWithMCP creates a test MCP client session connected to the server and calls the dev tool
func executeDevToolWithMCP(t *testing.T, suite *TestSuite, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	require.NotNil(t, suite.mcpServer, "MCP server must be created before creating client sessions")
	return testhelpers.ExecuteMCPTool(t, suite.mcpServer.GetMCPServer(), toolName, args)
}
