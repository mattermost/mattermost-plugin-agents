// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// SearchPostsArgs represents arguments for the search_posts tool
type SearchPostsArgs struct {
	Query     string `json:"query" jsonschema:"The search query,minLength=1,maxLength=4000"`
	TeamID    string `json:"team_id,omitempty" jsonschema:"Optional team ID to limit search scope,minLength=26,maxLength=26"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"Optional channel ID to limit search to a specific channel,minLength=26,maxLength=26"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Number of results to return (default: 20, max: 100),minimum=1,maximum=100"`
}

// SearchUsersArgs represents arguments for the search_users tool
type SearchUsersArgs struct {
	Term  string `json:"term" jsonschema:"Search term (username, email, first name, or last name),minLength=1,maxLength=64"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default: 20, max: 100),minimum=1,maximum=100"`
}

// getSearchTools returns all search-related tools
func (p *MattermostToolProvider) getSearchTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "search_posts",
			Description: "Search for posts in Mattermost. Parameters: query (required search terms), team_id (optional scope), channel_id (optional scope), limit (1-100, default 20). Returns matching posts with content, author, channel, and timestamp. Example: {\"query\": \"bug fix\", \"limit\": 10}",
			Schema:      llm.NewJSONSchemaFromStruct[SearchPostsArgs](),
			Resolver:    p.toolSearchPosts,
		},
		{
			Name:        "search_users",
			Description: "Search for existing users by username, email, or name. Parameters: term (required search text), limit (1-100, default 20). Returns user details including username, email, display name, and position for matching users. Example: {\"term\": \"john\", \"limit\": 5}",
			Schema:      llm.NewJSONSchemaFromStruct[SearchUsersArgs](),
			Resolver:    p.toolSearchUsers,
		},
	}
}

// toolSearchPosts implements the search_posts tool
func (p *MattermostToolProvider) toolSearchPosts(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args SearchPostsArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool search_posts: %w", err)
	}

	// Validate required fields
	if args.Query == "" {
		return "query is required", fmt.Errorf("query cannot be empty")
	}

	// Validate optional ID fields
	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}
	if args.ChannelID != "" && !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}

	// Set defaults
	if args.Limit == 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Build search parameters - use the simpler search method
	searchTerm := args.Query

	// For team-specific search, include team context. This can be an empty string if not specified.
	teamID := args.TeamID

	// Perform the search using basic search
	searchResults, _, err := client.SearchPosts(ctx, teamID, searchTerm, false)
	if err != nil {
		return "search failed", fmt.Errorf("error searching posts: %w", err)
	}

	if len(searchResults.Posts) == 0 {
		return "no posts found matching the search criteria", nil
	}

	// Convert posts map to slice
	posts := make([]*model.Post, 0, len(searchResults.Posts))
	for _, post := range searchResults.Posts {
		posts = append(posts, post)
	}

	// Limit results
	if len(posts) > args.Limit {
		posts = posts[:args.Limit]
	}

	// Format the response
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d posts matching '%s':\n\n", len(posts), args.Query))

	for i, post := range posts {
		// Get user info for the post
		user, _, err := client.GetUser(ctx, post.UserId, "")
		if err != nil {
			p.logger.Warn("failed to get user for post", "user_id", post.UserId, "error", err)
			result.WriteString(fmt.Sprintf("**Result %d** by Unknown User:\n", i+1))
		} else {
			result.WriteString(fmt.Sprintf("**Result %d** by %s:\n", i+1, user.Username))
		}

		// Get channel info
		channel, _, err := client.GetChannel(ctx, post.ChannelId, "")
		if err == nil {
			result.WriteString(fmt.Sprintf("Channel: %s\n", channel.DisplayName))
		}

		result.WriteString(fmt.Sprintf("Post ID: %s\n", post.Id))
		result.WriteString(fmt.Sprintf("Message: %s\n\n", post.Message))
	}

	return result.String(), nil
}

// toolSearchUsers implements the search_users tool
func (p *MattermostToolProvider) toolSearchUsers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args SearchUsersArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool search_users: %w", err)
	}

	// Validate required fields
	if args.Term == "" {
		return "term is required", fmt.Errorf("search term cannot be empty")
	}

	// Set defaults
	if args.Limit == 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Build search options
	searchOptions := &model.UserSearch{
		Term:          args.Term,
		Limit:         args.Limit,
		AllowInactive: false,
		WithoutTeam:   false,
	}

	// Perform the search
	users, _, err := client.SearchUsers(ctx, searchOptions)
	if err != nil {
		return "user search failed", fmt.Errorf("error searching users: %w", err)
	}

	if len(users) == 0 {
		return "no users found matching the search criteria", nil
	}

	// Format the response
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d users matching '%s':\n\n", len(users), args.Term))

	for i, user := range users {
		result.WriteString(fmt.Sprintf("**User %d**:\n", i+1))
		result.WriteString(fmt.Sprintf("Username: %s\n", user.Username))
		result.WriteString(fmt.Sprintf("ID: %s\n", user.Id))

		if user.FirstName != "" || user.LastName != "" {
			result.WriteString(fmt.Sprintf("Name: %s %s\n", user.FirstName, user.LastName))
		}
		if user.Email != "" {
			result.WriteString(fmt.Sprintf("Email: %s\n", user.Email))
		}
		if user.Nickname != "" {
			result.WriteString(fmt.Sprintf("Nickname: %s\n", user.Nickname))
		}
		if user.Position != "" {
			result.WriteString(fmt.Sprintf("Position: %s\n", user.Position))
		}

		result.WriteString("\n")
	}

	return result.String(), nil
}
