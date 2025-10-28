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

// ReadPostArgs represents arguments for the read_post tool
type ReadPostArgs struct {
	PostID        string `json:"post_id" jsonschema:"The ID of the post to read,minLength=26,maxLength=26"`
	IncludeThread bool   `json:"include_thread,omitempty" jsonschema:"Whether to include the entire thread (default: true)"`
}

// CreatePostArgs represents arguments for the create_post tool
type CreatePostArgs struct {
	ChannelID   string   `json:"channel_id" jsonschema:"The ID of the channel to post in,minLength=26,maxLength=26"`
	Message     string   `json:"message" jsonschema:"The message content,minLength=1"`
	RootID      string   `json:"root_id,omitempty" jsonschema:"Optional root post ID for replies,minLength=26,maxLength=26"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the post"`
}

// CreatePostAsUserArgs represents arguments for the create_post_as_user tool (dev mode only)
type CreatePostAsUserArgs struct {
	Username    string   `json:"username" jsonschema:"Username to login as"`
	Password    string   `json:"password" jsonschema:"Password to login with"`
	ChannelID   string   `json:"channel_id" jsonschema:"The ID of the channel to post in"`
	Message     string   `json:"message" jsonschema:"The message content"`
	RootID      string   `json:"root_id" jsonschema:"Optional root post ID for replies"`
	Props       string   `json:"props" jsonschema:"Optional post properties (JSON string)"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the post"`
}

// DMSelfArgs represents arguments for the dm_self tool
type DMSelfArgs struct {
	Message     string   `json:"message" jsonschema:"The message content to send to yourself,minLength=1"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the message"`
}

// getPostTools returns all post-related tools
func (p *MattermostToolProvider) getPostTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "read_post",
			Description: "Read a specific post and its thread from Mattermost. Parameters: post_id (required), include_thread (boolean, default true). Returns post content, author info, and optionally all replies in the thread. Example: {\"post_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\", \"include_thread\": true}",
			Schema:      NewJSONSchemaForAccessMode[ReadPostArgs](string(p.accessMode)),
			Resolver:    p.toolReadPost,
		},
		{
			Name:        "create_post",
			Description: "Create a new post in Mattermost. Parameters: channel_id (required), message (required), root_id (optional - for replies), attachments (optional file paths/URLs). Returns created post details including ID and timestamp. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"message\": \"Hello team!\"}",
			Schema:      NewJSONSchemaForAccessMode[CreatePostArgs](string(p.accessMode)),
			Resolver:    p.toolCreatePost,
		},
		{
			Name:        "dm_self",
			Description: "Send a direct message to yourself. Use when user requests to send something to themselves (e.g., 'send me this', 'DM me that'). Parameters: message (required), attachments (optional file paths/URLs). Returns confirmation with message ID. Example: {\"message\": \"Reminder: Follow up on project\"}",
			Schema:      NewJSONSchemaForAccessMode[DMSelfArgs](string(p.accessMode)),
			Resolver:    p.toolDMSelf,
		},
	}
}

// getDevPostTools returns development post-related tools for MCP
func (p *MattermostToolProvider) getDevPostTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "create_post_as_user",
			Description: "Create a post as a specific user using username/password login. Use this tool in dev mode for creating realistic multi-user scenarios. Simply provide the username and password of created users.",
			Schema:      NewJSONSchemaForAccessMode[CreatePostAsUserArgs](string(p.accessMode)),
			Resolver:    p.toolCreatePostAsUser,
		},
	}
}

// toolReadPost implements the read_post tool
func (p *MattermostToolProvider) toolReadPost(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args ReadPostArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool read_post: %w", err)
	}

	// Validate post ID
	if !model.IsValidId(args.PostID) {
		return "invalid post_id format", fmt.Errorf("post_id must be a valid ID")
	}

	// Set default for include_thread
	if !args.IncludeThread {
		// Since bool defaults to false, we need to check if it was explicitly set
		// For now, default to true
		args.IncludeThread = true
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	var posts []*model.Post

	if args.IncludeThread {
		// Get the thread
		postList, _, err := client.GetPostThread(ctx, args.PostID, "", false)
		if err != nil {
			return "failed to fetch post thread", fmt.Errorf("error fetching post thread: %w", err)
		}

		// Convert to slice and sort by creation time
		posts = make([]*model.Post, 0, len(postList.Posts))
		for _, post := range postList.Posts {
			posts = append(posts, post)
		}

		// Sort posts by CreateAt
		for i := 0; i < len(posts)-1; i++ {
			for j := i + 1; j < len(posts); j++ {
				if posts[i].CreateAt > posts[j].CreateAt {
					posts[i], posts[j] = posts[j], posts[i]
				}
			}
		}
	} else {
		// Get just the single post
		post, _, err := client.GetPost(ctx, args.PostID, "")
		if err != nil {
			return "failed to fetch post", fmt.Errorf("error fetching post: %w", err)
		}
		posts = []*model.Post{post}
	}

	if len(posts) == 0 {
		return "no posts found", nil
	}

	// Format the response
	var result strings.Builder
	if args.IncludeThread && len(posts) > 1 {
		result.WriteString(fmt.Sprintf("Thread with %d posts:\n\n", len(posts)))
	}

	for i, post := range posts {
		// Get user info for the post
		user, _, err := client.GetUser(ctx, post.UserId, "")
		if err != nil {
			p.logger.Warn("failed to get user for post", "user_id", post.UserId, "error", err)
			result.WriteString(fmt.Sprintf("**Post %d** by Unknown User:\n", i+1))
		} else {
			result.WriteString(fmt.Sprintf("**Post %d** by %s:\n", i+1, user.Username))
		}

		result.WriteString(fmt.Sprintf("Post ID: %s\n", post.Id))
		result.WriteString(fmt.Sprintf("%s\n\n", post.Message))
	}

	return result.String(), nil
}

// toolCreatePost implements the create_post tool
func (p *MattermostToolProvider) toolCreatePost(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreatePostArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_post: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}
	if args.Message == "" {
		return "message is required", fmt.Errorf("message cannot be empty")
	}
	// Validate root ID if provided (for replies)
	if args.RootID != "" && !model.IsValidId(args.RootID) {
		return "invalid root_id format", fmt.Errorf("root_id must be a valid ID")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Upload files if specified
	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, client, args.ChannelID, args.Attachments, mcpContext.AccessMode)

	// Create the post
	post := &model.Post{
		ChannelId: args.ChannelID,
		Message:   args.Message,
		RootId:    args.RootID,
		FileIds:   fileIDs,
	}

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return "failed to create post", fmt.Errorf("error creating post: %w", err)
	}

	return fmt.Sprintf("Successfully created post with ID: %s%s", createdPost.Id, attachmentMessage), nil
}

// toolCreatePostAsUser implements the create_post_as_user tool with custom authentication
func (p *MattermostToolProvider) toolCreatePostAsUser(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreatePostAsUserArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_post_as_user: %w", err)
	}

	// Validate required fields
	if args.Username == "" {
		return "username is required", fmt.Errorf("username cannot be empty")
	}
	if args.Password == "" {
		return "password is required", fmt.Errorf("password cannot be empty")
	}
	if !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}
	if args.Message == "" {
		return "message is required", fmt.Errorf("message cannot be empty")
	}
	// Validate root ID if provided (for replies)
	if args.RootID != "" && !model.IsValidId(args.RootID) {
		return "invalid root_id format", fmt.Errorf("root_id must be a valid ID")
	}

	// Create a new client and login as the specified user
	ctx := context.Background()
	userClient := model.NewAPIv4Client(p.mmInternalServerURL)

	// Login as the specified user
	user, _, err := userClient.Login(ctx, args.Username, args.Password)
	if err != nil {
		return "failed to login as user", fmt.Errorf("login failed for user %s: %w", args.Username, err)
	}

	// Upload files if specified
	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, userClient, args.ChannelID, args.Attachments, mcpContext.AccessMode)

	// Create the post
	post := &model.Post{
		ChannelId: args.ChannelID,
		Message:   args.Message,
		RootId:    args.RootID,
		FileIds:   fileIDs,
	}

	// Parse props if provided
	if args.Props != "" {
		// For simplicity, we'll just add it as a string. In a real implementation,
		// you might want to parse the JSON properly
		post.SetProps(map[string]interface{}{"custom_props": args.Props})
	}

	createdPost, _, err := userClient.CreatePost(ctx, post)
	if err != nil {
		return "failed to create post", fmt.Errorf("error creating post as user %s: %w", args.Username, err)
	}

	return fmt.Sprintf("Successfully created post with ID %s as user %s%s", createdPost.Id, user.Username, attachmentMessage), nil
}

// toolDMSelf implements the dm_self tool
func (p *MattermostToolProvider) toolDMSelf(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args DMSelfArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool dm_self: %w", err)
	}

	// Validate required fields
	if args.Message == "" {
		return "message is required", fmt.Errorf("message cannot be empty")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Get current user information
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		return "failed to get current user", fmt.Errorf("error getting current user: %w", err)
	}

	// Create or get direct channel with self
	dmChannel, _, err := client.CreateDirectChannel(ctx, user.Id, user.Id)
	if err != nil {
		return "failed to create DM channel", fmt.Errorf("error creating direct channel: %w", err)
	}

	// Upload files if specified
	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, client, dmChannel.Id, args.Attachments, mcpContext.AccessMode)

	// Create the post in the DM channel
	post := &model.Post{
		ChannelId: dmChannel.Id,
		Message:   args.Message,
		FileIds:   fileIDs,
	}

	// Set props to trigger notifications
	post.SetProps(map[string]interface{}{
		"from_webhook": "true",
	})

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return "failed to create DM post", fmt.Errorf("error creating DM post: %w", err)
	}

	return fmt.Sprintf("Successfully sent DM to yourself with ID: %s%s", createdPost.Id, attachmentMessage), nil
}
