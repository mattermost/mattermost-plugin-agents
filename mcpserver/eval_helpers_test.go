// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/testhelpers"
	"github.com/mattermost/mattermost/server/public/model"
)

// evalChannelData holds the seeded test data for eval tests.
type evalChannelData struct {
	team    *model.Team
	channel *model.Channel
	alice   *model.User
	bob     *model.User
	charlie *model.User
	// bobTimelinePost is Bob's "What's the timeline?" post, used as a thread root.
	bobTimelinePost *model.Post
}

// seedChannelConversation creates a realistic multi-user conversation in a channel
// with known content that rubrics can check against.
func seedChannelConversation(t *testing.T, serverURL, adminToken string) *evalChannelData {
	t.Helper()

	ctx := context.Background()
	adminClient := model.NewAPIv4Client(serverURL)
	adminClient.SetToken(adminToken)

	// Create team and channel
	team := testhelpers.CreateTestTeam(t, adminClient, "eval-team", "Eval Team")
	channel := testhelpers.CreateTestChannel(t, adminClient, team.Id, "migration-planning", "Migration Planning")

	// Create users with known passwords so we can log in as them
	password := "EvalTest123!"
	alice := testhelpers.CreateTestUser(t, adminClient, "alice.eval", "alice.eval@example.com", password)
	bob := testhelpers.CreateTestUser(t, adminClient, "bob.eval", "bob.eval@example.com", password)
	charlie := testhelpers.CreateTestUser(t, adminClient, "charlie.eval", "charlie.eval@example.com", password)

	// Add users to team and channel
	for _, u := range []*model.User{alice, bob, charlie} {
		testhelpers.AddUserToTeam(t, adminClient, team.Id, u.Id)
		testhelpers.AddUserToChannel(t, adminClient, channel.Id, u.Id)
	}

	// Create per-user clients
	aliceClient := model.NewAPIv4Client(serverURL)
	_, _, err := aliceClient.Login(ctx, alice.Username, password)
	require.NoError(t, err, "Failed to login as alice")

	bobClient := model.NewAPIv4Client(serverURL)
	_, _, err = bobClient.Login(ctx, bob.Username, password)
	require.NoError(t, err, "Failed to login as bob")

	charlieClient := model.NewAPIv4Client(serverURL)
	_, _, err = charlieClient.Login(ctx, charlie.Username, password)
	require.NoError(t, err, "Failed to login as charlie")

	// Post messages in order to create a realistic conversation.
	// Alice proposes PostgreSQL migration
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "I've been evaluating our database options and I think we should migrate from MySQL to PostgreSQL. The JSON support and extension ecosystem are much better for our use case.",
	})
	require.NoError(t, err)

	// Bob asks about timeline
	bobTimelinePost, _, err := bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "What's the timeline for this? We have the Q3 feature freeze coming up.",
	})
	require.NoError(t, err)

	// Alice replies in-thread to Bob's question
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		RootId:    bobTimelinePost.Id,
		Message:   "I'm targeting next sprint for the schema migration, then two weeks of testing before we cut over.",
	})
	require.NoError(t, err)

	// Charlie raises concern about data volume
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "One concern — we have about 500GB of data in the current MySQL instance. Have we estimated the downtime for the migration?",
	})
	require.NoError(t, err)

	// Bob @mentions alice asking about rollback plan
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "@alice.eval what's the rollback plan if something goes wrong during the migration?",
	})
	require.NoError(t, err)

	// Alice describes rollback approach
	_, _, err = aliceClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "We'll keep the MySQL instance running in read-only mode during the cutover. If anything fails, we can switch back within minutes. I've also set up continuous replication as a safety net.",
	})
	require.NoError(t, err)

	// Charlie shares monitoring docs link
	_, _, err = charlieClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Good plan. I've put together monitoring dashboards for tracking the migration progress: https://wiki.example.com/pg-migration-monitoring",
	})
	require.NoError(t, err)

	// Bob confirms he'll review
	_, _, err = bobClient.CreatePost(ctx, &model.Post{
		ChannelId: channel.Id,
		Message:   "Sounds solid. I'll review the migration script this week and flag any issues before we start.",
	})
	require.NoError(t, err)

	return &evalChannelData{
		team:            team,
		channel:         channel,
		alice:           alice,
		bob:             bob,
		charlie:         charlie,
		bobTimelinePost: bobTimelinePost,
	}
}

// mcpToolsToLLMTools converts MCP server tools into llm.Tool instances with resolvers
// that call through the MCP protocol. This mirrors the production pattern in
// mcp/user_clients.go:201 (createToolResolver).
func mcpToolsToLLMTools(t *testing.T, mcpServer *gomcp.Server) []llm.Tool {
	t.Helper()

	session := testhelpers.CreateTestMCPSession(t, mcpServer)

	ctx := context.Background()
	toolsList, err := session.ListTools(ctx, nil)
	require.NoError(t, err, "Failed to list MCP tools")

	var tools []llm.Tool
	for _, tool := range toolsList.Tools {
		toolName := tool.Name // capture for closure
		tools = append(tools, llm.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
			Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
				// Same pattern as production createToolResolver (mcp/user_clients.go:201)
				var args map[string]any
				if err := argsGetter(&args); err != nil {
					return "", err
				}
				result, err := session.CallTool(ctx, &gomcp.CallToolParams{
					Name:      toolName,
					Arguments: args,
				})
				if err != nil {
					return "", err
				}
				// Extract text content
				var text string
				for _, c := range result.Content {
					if tc, ok := c.(*gomcp.TextContent); ok {
						text += tc.Text
					}
				}
				return text, nil
			},
		})
	}
	return tools
}
