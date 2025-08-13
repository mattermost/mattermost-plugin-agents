// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/testhelpers"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestMCPToolsIntegration tests MCP tools against a real Mattermost instance
func TestMCPToolsIntegration(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	// Create MCP server
	suite.CreateMCPServer(false)

	// Create Mattermost client for setup
	client := model.NewAPIv4Client(suite.serverURL)
	client.SetToken(suite.adminToken)

	// Setup test data
	testData := testhelpers.SetupBasicTestData(t, client, suite.adminToken)

	t.Run("CreatePostTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": testData.Channel.Id,
				"message":    "Hello from MCP integration test!",
			}

			result := executeToolWithMCP(t, suite, "create_post", args)
			assert.False(t, result.IsError, "create_post should succeed")
			assert.NotEmpty(t, result.Content, "create_post should return content")

			// Verify the post was actually created
			posts, _, err := client.GetPostsForChannel(context.Background(), testData.Channel.Id, 0, 10, "", false, false)
			require.NoError(t, err)
			found := false
			for _, post := range posts.Posts {
				if post.Message == "Hello from MCP integration test!" {
					found = true
					break
				}
			}
			assert.True(t, found, "Test post should be found in channel")
		})

		t.Run("InvalidChannelID", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": "invalid-channel-id",
				"message":    "This should fail",
			}

			result := executeToolWithMCP(t, suite, "create_post", args)
			assert.True(t, result.IsError, "create_post with invalid channel should fail")
		})

		t.Run("MissingParameters", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": testData.Channel.Id,
				// missing message
			}

			result := executeToolWithMCP(t, suite, "create_post", args)
			assert.True(t, result.IsError, "create_post without message should fail")
		})
	})

	t.Run("ReadChannelTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			// Create a test post first
			testPost := testhelpers.CreateTestPost(t, client, testData.Channel.Id, "Test message for reading")

			args := map[string]interface{}{
				"channel_id": testData.Channel.Id,
				"limit":      10,
			}

			result := executeToolWithMCP(t, suite, "read_channel", args)
			assert.False(t, result.IsError, "read_channel should succeed")
			assert.NotEmpty(t, result.Content, "read_channel should return content")

			// Check that our test post appears in the results
			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, testPost.Id, "Response should contain the test post ID")
				}
			}
		})

		t.Run("InvalidChannelID", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": "invalid-channel-id",
				"limit":      10,
			}

			result := executeToolWithMCP(t, suite, "read_channel", args)
			assert.True(t, result.IsError, "read_channel with invalid channel should fail")
		})
	})

	t.Run("GetChannelInfoTool", func(t *testing.T) {
		t.Run("HappyPathWithChannelID", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": testData.Channel.Id,
			}

			result := executeToolWithMCP(t, suite, "get_channel_info", args)
			assert.False(t, result.IsError, "get_channel_info should succeed")
			assert.NotEmpty(t, result.Content, "get_channel_info should return content")

			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, testData.Channel.Id, "Response should contain channel ID")
					assert.Contains(t, textContent.Text, testData.Channel.DisplayName, "Response should contain channel display name")
				}
			}
		})

		t.Run("SmartLookupByDisplayName", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_display_name": testData.Channel.DisplayName,
				"team_id":              testData.Team.Id,
			}

			result := executeToolWithMCP(t, suite, "get_channel_info", args)
			assert.False(t, result.IsError, "get_channel_info by display name should succeed")
			assert.NotEmpty(t, result.Content, "get_channel_info should return content")
		})

		t.Run("SmartLookupByChannelName", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_name": testData.Channel.Name,
				"team_id":      testData.Team.Id,
			}

			result := executeToolWithMCP(t, suite, "get_channel_info", args)
			assert.False(t, result.IsError, "get_channel_info by channel name should succeed")
		})

		t.Run("InvalidChannelID", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_id": "invalid-channel-id",
			}

			result := executeToolWithMCP(t, suite, "get_channel_info", args)
			assert.True(t, result.IsError, "get_channel_info with invalid ID should fail")
		})

		t.Run("CrossTeamLookupByChannelName", func(t *testing.T) {
			args := map[string]interface{}{
				"channel_name": testData.Channel.Name,
				// missing team_id - should fall back to cross-team search
			}

			result := executeToolWithMCP(t, suite, "get_channel_info", args)
			assert.False(t, result.IsError, "get_channel_info with channel name should succeed via cross-team search")
			assert.NotEmpty(t, result.Content, "get_channel_info should return content")
		})
	})

	t.Run("GetTeamInfoTool", func(t *testing.T) {
		t.Run("HappyPathWithTeamID", func(t *testing.T) {
			args := map[string]interface{}{
				"team_id": testData.Team.Id,
			}

			result := executeToolWithMCP(t, suite, "get_team_info", args)
			assert.False(t, result.IsError, "get_team_info should succeed")
			assert.NotEmpty(t, result.Content, "get_team_info should return content")

			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, testData.Team.Id, "Response should contain team ID")
					assert.Contains(t, textContent.Text, testData.Team.DisplayName, "Response should contain team display name")
				}
			}
		})

		t.Run("SmartLookupByDisplayName", func(t *testing.T) {
			args := map[string]interface{}{
				"team_display_name": testData.Team.DisplayName,
			}

			result := executeToolWithMCP(t, suite, "get_team_info", args)
			assert.False(t, result.IsError, "get_team_info by display name should succeed")
		})

		t.Run("InvalidTeamID", func(t *testing.T) {
			args := map[string]interface{}{
				"team_id": "invalid-team-id",
			}

			result := executeToolWithMCP(t, suite, "get_team_info", args)
			assert.True(t, result.IsError, "get_team_info with invalid ID should fail")
		})
	})

	t.Run("SearchUsersTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"term":  testData.User.Username,
				"limit": 10,
			}

			result := executeToolWithMCP(t, suite, "search_users", args)
			assert.False(t, result.IsError, "search_users should succeed")
			assert.NotEmpty(t, result.Content, "search_users should return content")

			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, testData.User.Username, "Response should contain the username")
				}
			}
		})

		t.Run("NoResultsFound", func(t *testing.T) {
			args := map[string]interface{}{
				"term":  "nonexistent-user-xyz123",
				"limit": 10,
			}

			result := executeToolWithMCP(t, suite, "search_users", args)
			assert.False(t, result.IsError, "search_users with no results should not error")
			// Should return empty results, not an error
		})

		t.Run("MissingSearchTerm", func(t *testing.T) {
			args := map[string]interface{}{
				"limit": 10,
				// missing term
			}

			result := executeToolWithMCP(t, suite, "search_users", args)
			assert.True(t, result.IsError, "search_users without term should fail")
		})
	})

	t.Run("ReadPostTool", func(t *testing.T) {
		// Create a test post for reading
		testPost := testhelpers.CreateTestPost(t, client, testData.Channel.Id, "Test post for reading")

		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"post_id":        testPost.Id,
				"include_thread": true,
			}

			result := executeToolWithMCP(t, suite, "read_post", args)
			assert.False(t, result.IsError, "read_post should succeed")
			assert.NotEmpty(t, result.Content, "read_post should return content")

			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, testPost.Id, "Response should contain post ID")
					assert.Contains(t, textContent.Text, "Test post for reading", "Response should contain post message")
				}
			}
		})

		t.Run("InvalidPostID", func(t *testing.T) {
			args := map[string]interface{}{
				"post_id": "invalid-post-id",
			}

			result := executeToolWithMCP(t, suite, "read_post", args)
			assert.True(t, result.IsError, "read_post with invalid ID should fail")
		})
	})

	t.Run("CreateChannelTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"name":         "test-created-channel",
				"display_name": "Test Created Channel",
				"type":         "O",
				"team_id":      testData.Team.Id,
			}

			result := executeToolWithMCP(t, suite, "create_channel", args)
			assert.False(t, result.IsError, "create_channel should succeed")
			assert.NotEmpty(t, result.Content, "create_channel should return content")
		})

		t.Run("InvalidTeamID", func(t *testing.T) {
			args := map[string]interface{}{
				"name":         "test-channel-fail",
				"display_name": "Test Channel Fail",
				"type":         "O",
				"team_id":      "invalid-team-id",
			}

			result := executeToolWithMCP(t, suite, "create_channel", args)
			assert.True(t, result.IsError, "create_channel with invalid team_id should fail")
		})
	})

	t.Run("SearchPostsTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			// Create a test post with unique content for searching
			testMessage := "unique-search-test-message-12345"
			createdPost := testhelpers.CreateTestPost(t, client, testData.Channel.Id, testMessage)

			// Simple search test - just verify the API call works
			args := map[string]interface{}{
				"query":   testMessage,
				"team_id": testData.Team.Id,
				"limit":   10,
			}

			result := executeToolWithMCP(t, suite, "search_posts", args)
			assert.False(t, result.IsError, "search_posts should not error")
			assert.NotEmpty(t, result.Content, "search_posts should return content")

			// Check that we get a valid response (either posts found or none found message)
			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.NotEmpty(t, textContent.Text, "Response should have content")
				}
			}

			// Clean up
			_, err := client.DeletePost(context.Background(), createdPost.Id)
			require.NoError(t, err, "Should be able to clean up test post")
		})

		t.Run("NoResultsFound", func(t *testing.T) {
			args := map[string]interface{}{
				"query": "nonexistent-search-term-xyz123",
				"limit": 10,
			}

			result := executeToolWithMCP(t, suite, "search_posts", args)
			assert.False(t, result.IsError, "search_posts with no results should not error")
		})
	})

	t.Run("DMSelfTool", func(t *testing.T) {
		t.Run("HappyPath", func(t *testing.T) {
			args := map[string]interface{}{
				"message": "Test DM to myself from integration test!",
			}

			result := executeToolWithMCP(t, suite, "dm_self", args)
			assert.False(t, result.IsError, "dm_self should succeed")
			assert.NotEmpty(t, result.Content, "dm_self should return content")

			// Verify the response mentions success
			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					assert.Contains(t, textContent.Text, "Successfully sent DM to yourself", "Response should indicate success")
					assert.Contains(t, textContent.Text, "with ID:", "Response should include post ID")
				}
			}

			// Get user to find DM channel
			user, _, err := client.GetMe(context.Background(), "")
			require.NoError(t, err)

			// Get DM channel with self (userID__userID format)
			dmChannel, _, err := client.CreateDirectChannel(context.Background(), user.Id, user.Id)
			require.NoError(t, err)

			// Verify the post was actually created in the DM channel
			posts, _, err := client.GetPostsForChannel(context.Background(), dmChannel.Id, 0, 10, "", false, false)
			require.NoError(t, err)
			found := false
			for _, post := range posts.Posts {
				if post.Message == "Test DM to myself from integration test!" {
					found = true
					// Verify props were set
					assert.Equal(t, "true", post.GetProp("from_webhook"), "Post should have from_webhook prop set")
					break
				}
			}
			assert.True(t, found, "Test DM post should be found in self DM channel")
		})

		t.Run("EmptyMessage", func(t *testing.T) {
			args := map[string]interface{}{
				"message": "",
			}

			result := executeToolWithMCP(t, suite, "dm_self", args)
			assert.True(t, result.IsError, "dm_self with empty message should fail")
		})

		t.Run("MissingMessage", func(t *testing.T) {
			args := map[string]interface{}{
				// missing message field
			}

			result := executeToolWithMCP(t, suite, "dm_self", args)
			assert.True(t, result.IsError, "dm_self without message should fail")
		})
	})
}

// executeToolWithMCP calls the MCP tool through the unified helper
func executeToolWithMCP(t *testing.T, suite *TestSuite, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	require.NotNil(t, suite.mcpServer, "MCP server must be created before calling tools")
	return testhelpers.ExecuteMCPTool(t, suite.mcpServer.GetMCPServer(), toolName, args)
}
