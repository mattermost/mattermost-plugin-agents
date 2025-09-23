// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package testhelpers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// CreateTestMCPSession creates an in-memory MCP client session connected to the server
// This enables testing tools through the full MCP protocol stack without external transports
func CreateTestMCPSession(t *testing.T, mcpServer *mcp.Server) *mcp.ClientSession {
	require.NotNil(t, mcpServer, "MCP server must be provided")

	// Create in-memory transport pair
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	// Start server with in-memory transport in a goroutine
	ctx := context.Background()
	go func() {
		if err := mcpServer.Run(ctx, serverTransport); err != nil {
			// Log error but don't fail the test here since it might be normal shutdown
			t.Logf("Server stopped with error: %v", err)
		}
	}()

	// Create test client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	// Connect client to server
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err, "Failed to connect test client to MCP server")

	return session
}

// ExecuteMCPTool calls an MCP tool through a test client session
// This provides true integration testing by using the actual MCP protocol with in-memory transport
func ExecuteMCPTool(t *testing.T, mcpServer *mcp.Server, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Create test session
	session := CreateTestMCPSession(t, mcpServer)
	defer session.Close()

	// Call the tool
	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})

	if err != nil {
		return result, err
	}

	// Check if the result indicates an error from the tool resolver
	if result.IsError {
		// Extract error message from content
		var errorMsgs []string
		for _, content := range result.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				errorMsgs = append(errorMsgs, textContent.Text)
			}
		}

		errorMsg := "tool execution failed"
		if len(errorMsgs) > 0 {
			errorMsg = strings.Join(errorMsgs, "\n")
		}
		return result, fmt.Errorf("%s", errorMsg)
	}

	return result, nil
}
