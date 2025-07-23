// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
)

// TestData holds common test data structures
type TestData struct {
	Team     *model.Team
	Channel  *model.Channel
	User     *model.User
	AdminPAT string
}

// CreateTestTeam creates a test team
func CreateTestTeam(t *testing.T, client *model.Client4, name, displayName string) *model.Team {
	team := &model.Team{
		Name:        name,
		DisplayName: displayName,
		Type:        model.TeamOpen,
	}

	createdTeam, _, err := client.CreateTeam(context.Background(), team)
	require.NoError(t, err, "Failed to create test team")
	require.NotNil(t, createdTeam, "Created team should not be nil")

	return createdTeam
}

// CreateTestChannel creates a test channel
func CreateTestChannel(t *testing.T, client *model.Client4, teamID, name, displayName string) *model.Channel {
	channel := &model.Channel{
		TeamId:      teamID,
		Name:        name,
		DisplayName: displayName,
		Type:        model.ChannelTypeOpen,
	}

	createdChannel, _, err := client.CreateChannel(context.Background(), channel)
	require.NoError(t, err, "Failed to create test channel")
	require.NotNil(t, createdChannel, "Created channel should not be nil")

	return createdChannel
}

// CreateTestUser creates a test user
func CreateTestUser(t *testing.T, client *model.Client4, username, email, password string) *model.User {
	user := &model.User{
		Username: username,
		Email:    email,
		Password: password,
	}

	createdUser, _, err := client.CreateUser(context.Background(), user)
	require.NoError(t, err, "Failed to create test user")
	require.NotNil(t, createdUser, "Created user should not be nil")

	return createdUser
}

// CreateTestPost creates a test post
func CreateTestPost(t *testing.T, client *model.Client4, channelID, message string) *model.Post {
	post := &model.Post{
		ChannelId: channelID,
		Message:   message,
	}

	createdPost, _, err := client.CreatePost(context.Background(), post)
	require.NoError(t, err, "Failed to create test post")
	require.NotNil(t, createdPost, "Created post should not be nil")

	return createdPost
}

// AddUserToTeam adds a user to a team
func AddUserToTeam(t *testing.T, client *model.Client4, teamID, userID string) {
	_, _, err := client.AddTeamMember(context.Background(), teamID, userID)
	require.NoError(t, err, "Failed to add user to team")
}

// AddUserToChannel adds a user to a channel
func AddUserToChannel(t *testing.T, client *model.Client4, channelID, userID string) {
	_, _, err := client.AddChannelMember(context.Background(), channelID, userID)
	require.NoError(t, err, "Failed to add user to channel")
}

// SetupBasicTestData creates basic test data (team, channel, user)
func SetupBasicTestData(t *testing.T, client *model.Client4, adminPAT string) *TestData {
	// Create test team
	team := CreateTestTeam(t, client, "test-team", "Test Team")

	// Create test channel
	channel := CreateTestChannel(t, client, team.Id, "test-channel", "Test Channel")

	// Create test user
	user := CreateTestUser(t, client, "testuser", "test@example.com", "testpassword")

	// Add user to team and channel
	AddUserToTeam(t, client, team.Id, user.Id)
	AddUserToChannel(t, client, channel.Id, user.Id)

	return &TestData{
		Team:     team,
		Channel:  channel,
		User:     user,
		AdminPAT: adminPAT,
	}
}

// ExecuteMCPTool calls an MCP tool through the MCP server's message handler
// This provides true integration testing by using the actual MCP protocol
func ExecuteMCPTool(t *testing.T, mcpServer *server.MCPServer, toolName string, args map[string]interface{}) *mcp.CallToolResult {
	require.NotNil(t, mcpServer, "MCP server must be provided")

	ctx := context.Background()

	// Create a proper MCP JSON-RPC request
	jsonrpcRequest := mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcp.NewRequestId("test-" + toolName),
		Request: mcp.Request{
			Method: "tools/call",
		},
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}

	// Marshal the request to JSON (as it would come over the wire)
	requestBytes, err := json.Marshal(jsonrpcRequest)
	require.NoError(t, err, "Failed to marshal MCP request")

	// Send through the MCP server's message handler (same as production)
	responseMessage := mcpServer.HandleMessage(ctx, json.RawMessage(requestBytes))

	// Parse the response
	responseBytes, err := json.Marshal(responseMessage)
	require.NoError(t, err, "Failed to marshal MCP response")

	// Check if it's an error response
	var errorResponse struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      interface{} `json:"id"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(responseBytes, &errorResponse) == nil && errorResponse.Error != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Error: %s", errorResponse.Error.Message),
				},
			},
			IsError: true,
		}
	}

	// Parse as successful response with custom structure to handle Content interface
	var successResponse struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      interface{} `json:"id"`
		Result  struct {
			Content []map[string]interface{} `json:"content"` // Handle as raw JSON first
			IsError bool                     `json:"isError,omitempty"`
		} `json:"result"`
	}
	err = json.Unmarshal(responseBytes, &successResponse)
	require.NoError(t, err, "Failed to unmarshal MCP tool response")

	// Convert to proper CallToolResult with TextContent
	result := &mcp.CallToolResult{
		IsError: successResponse.Result.IsError,
		Content: make([]mcp.Content, len(successResponse.Result.Content)),
	}

	// Convert each content item to TextContent (most common case for our tools)
	for i, content := range successResponse.Result.Content {
		if text, ok := content["text"].(string); ok {
			result.Content[i] = mcp.TextContent{
				Type: "text",
				Text: text,
			}
		} else {
			// Fallback for other content types - just convert to string
			result.Content[i] = mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("%v", content),
			}
		}
	}

	return result
}
