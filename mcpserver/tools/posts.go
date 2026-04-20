// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ReadPostArgs represents arguments for the read_post tool
type ReadPostArgs struct {
	PostID        string `json:"post_id" jsonschema:"The ID of the post to read,minLength=26,maxLength=26"`
	IncludeThread bool   `json:"include_thread,omitempty" jsonschema:"Whether to include the entire thread (default: true)"`
}

// CreatePostArgs represents arguments for the create_post tool
type CreatePostArgs struct {
	ChannelID          string   `json:"channel_id" jsonschema:"The ID of the channel to post in,minLength=26,maxLength=26"`
	ChannelDisplayName string   `json:"channel_display_name" jsonschema:"The display name of the channel (for context verification),minLength=1"`
	TeamDisplayName    string   `json:"team_display_name" jsonschema:"The display name of the team (for context verification),minLength=1"`
	Message            string   `json:"message" jsonschema:"The message content,minLength=1"`
	RootID             string   `json:"root_id,omitempty" jsonschema:"Optional root post ID for replies,minLength=26,maxLength=26"`
	Attachments        []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the post"`
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

// DMArgs represents arguments for the dm tool
type DMArgs struct {
	Username    string   `json:"username,omitempty" jsonschema:"Target username. If omitted the message is sent to yourself."`
	Message     string   `json:"message" jsonschema:"The message content to send,minLength=1"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach"`
}

// GroupMessageArgs represents arguments for the group_message tool
type GroupMessageArgs struct {
	Usernames   []string `json:"usernames" jsonschema:"Target usernames (must be at least 2)."`
	Message     string   `json:"message" jsonschema:"The message content to send,minLength=1"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach"`
}

// providePostTools registers all post-related MCP tools.
func (p *MattermostToolProvider) providePostTools(s *mcp.Server) {
	attachmentsParam := ""
	if p.accessMode == AccessModeLocal {
		attachmentsParam = ", attachments (optional file paths/URLs)"
	}

	createPostDesc := fmt.Sprintf("Create a new post in Mattermost. IMPORTANT WORKFLOW: You MUST first call get_channel_info to obtain the channel_id, channel_display_name, and team_display_name. Present this context to the user before posting. Then call this tool with all required parameters. This ensures full transparency about where the message will be posted. Parameters: channel_id (required), message (required), root_id (optional - for replies)%s. Returns created post details including ID and timestamp. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"message\": \"Hello team!\"}", attachmentsParam)

	dmDesc := fmt.Sprintf("Send a direct message to a user. Provide username to specify the recipient. If username is omitted, the message is sent to yourself. This is the DEFAULT way to message people — call it multiple times to message multiple people individually. Only use the group_message tool when the user explicitly asks for a group chat. Parameters: message (required), username (optional)%s. Returns confirmation with message ID. Example: {\"message\": \"Hello!\", \"username\": \"john\"}", attachmentsParam)

	groupMessageDesc := fmt.Sprintf("Send a message to a shared group conversation with 2 or more other users. All participants can see each other's messages. ONLY use this when the user explicitly asks for a group message, group chat, or group conversation. If the user just asks to 'message' or 'send to' multiple people, use the dm tool once per person instead. Parameters: message (required), usernames (at least 2 required)%s. Returns confirmation with message ID. Example: {\"message\": \"Hey team!\", \"usernames\": [\"alice\", \"bob\"]}", attachmentsParam)

	registerTool(s, p, "read_post",
		"Read a specific post and its thread from Mattermost. Parameters: post_id (required), include_thread (boolean, default true). Returns post content, author info, and optionally all replies in the thread. Example: {\"post_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\", \"include_thread\": true}",
		NewJSONSchemaForAccessMode[ReadPostArgs](string(p.accessMode)),
		p.toolReadPost,
		format.ReadPostOutput,
	)
	registerTool(s, p, "create_post", createPostDesc,
		NewJSONSchemaForAccessMode[CreatePostArgs](string(p.accessMode)),
		p.toolCreatePost,
		format.CreatePostOutput,
	)
	registerTool(s, p, "dm", dmDesc,
		NewJSONSchemaForAccessMode[DMArgs](string(p.accessMode)),
		p.toolDM,
		format.DMOutput,
	)
	registerTool(s, p, "group_message", groupMessageDesc,
		NewJSONSchemaForAccessMode[GroupMessageArgs](string(p.accessMode)),
		p.toolGroupMessage,
		format.GroupMessageOutput,
	)
}

// provideDevPostTools registers development post-related MCP tools.
func (p *MattermostToolProvider) provideDevPostTools(s *mcp.Server) {
	registerTool(s, p, "create_post_as_user",
		"Create a post as a specific user using username/password login. Use this tool in dev mode for creating realistic multi-user scenarios. Simply provide the username and password of created users.",
		NewJSONSchemaForAccessMode[CreatePostAsUserArgs](string(p.accessMode)),
		p.toolCreatePostAsUser,
		format.CreatePostAsUserOutput,
	)
}

// toolReadPost implements the read_post tool
func (p *MattermostToolProvider) toolReadPost(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.ReadPostOutput, error) {
	var args ReadPostArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.ReadPostOutput{}, fmt.Errorf("failed to get arguments for tool read_post: %w", err)
	}

	// Validate post ID
	if !model.IsValidId(args.PostID) {
		return mcptool.ReadPostOutput{}, fmt.Errorf("post_id must be a valid ID")
	}

	// Set default for include_thread
	if !args.IncludeThread {
		// Since bool defaults to false, we need to check if it was explicitly set
		// For now, default to true
		args.IncludeThread = true
	}

	// Get client from context
	if mcpContext.Client == nil {
		return mcptool.ReadPostOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var posts []*model.Post

	if args.IncludeThread {
		// Get the thread
		postList, _, err := client.GetPostThread(ctx, args.PostID, "", false)
		if err != nil {
			return mcptool.ReadPostOutput{}, fmt.Errorf("error fetching post thread: %w", err)
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
			return mcptool.ReadPostOutput{}, fmt.Errorf("error fetching post: %w", err)
		}
		posts = []*model.Post{post}
	}

	if len(posts) == 0 {
		return mcptool.ReadPostOutput{}, nil
	}

	var channelName, teamName string
	if len(posts) > 0 {
		channel, _, chErr := client.GetChannel(ctx, posts[0].ChannelId)
		if chErr == nil {
			channelName = channel.DisplayName
			team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
			if teamErr == nil {
				teamName = team.DisplayName
			}
		}
	}

	userIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, post := range posts {
		if !seen[post.UserId] {
			seen[post.UserId] = true
			userIDs = append(userIDs, post.UserId)
		}
	}
	userMap := make(map[string]*model.User)
	if len(userIDs) > 0 {
		users, _, usersErr := client.GetUsersByIds(ctx, userIDs)
		if usersErr != nil {
			p.logger.Warn("failed to fetch users for read_post", "error", usersErr)
		} else {
			for _, u := range users {
				userMap[u.Id] = u
			}
		}
	}

	channelID := ""
	if len(posts) > 0 {
		channelID = posts[0].ChannelId
	}

	out := mcptool.ReadPostOutput{
		Posts:         posts,
		Users:         userMap,
		ChannelID:     channelID,
		ChannelName:   channelName,
		TeamName:      teamName,
		IncludeThread: args.IncludeThread,
	}
	return out, nil
}

// toolCreatePost implements the create_post tool
func (p *MattermostToolProvider) toolCreatePost(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.CreatePostOutput, error) {
	var args CreatePostArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.CreatePostOutput{}, fmt.Errorf("failed to get arguments for tool create_post: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.ChannelID) {
		return mcptool.CreatePostOutput{}, fmt.Errorf("channel_id must be a valid ID")
	}
	if args.Message == "" {
		return mcptool.CreatePostOutput{}, fmt.Errorf("message cannot be empty")
	}
	if args.ChannelDisplayName == "" {
		return mcptool.CreatePostOutput{}, fmt.Errorf("channel_display_name cannot be empty - you must call get_channel_info first")
	}
	if args.TeamDisplayName == "" {
		return mcptool.CreatePostOutput{}, fmt.Errorf("team_display_name cannot be empty - you must call get_channel_info first")
	}
	// Validate root ID if provided (for replies)
	if args.RootID != "" && !model.IsValidId(args.RootID) {
		return mcptool.CreatePostOutput{}, fmt.Errorf("root_id must be a valid ID")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return mcptool.CreatePostOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Validate that the provided display names match the actual channel and team
	channel, _, err := client.GetChannel(ctx, args.ChannelID)
	if err != nil {
		return mcptool.CreatePostOutput{}, fmt.Errorf("error fetching channel for validation: %w", err)
	}

	// Check if channel display name matches
	if channel.DisplayName != args.ChannelDisplayName {
		return mcptool.CreatePostOutput{}, fmt.Errorf("channel_display_name mismatch: provided '%s' but channel ID '%s' has display name '%s'",
			args.ChannelDisplayName, args.ChannelID, channel.DisplayName)
	}

	// Get team info to validate team display name
	team, _, err := client.GetTeam(ctx, channel.TeamId, "")
	if err != nil {
		return mcptool.CreatePostOutput{}, fmt.Errorf("error fetching team for validation: %w", err)
	}

	// Check if team display name matches
	if team.DisplayName != args.TeamDisplayName {
		return mcptool.CreatePostOutput{}, fmt.Errorf("team_display_name mismatch: provided '%s' but team ID '%s' has display name '%s'",
			args.TeamDisplayName, channel.TeamId, team.DisplayName)
	}

	// Upload files if specified
	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, client, args.ChannelID, args.Attachments, mcpContext.AccessMode)

	// Create the post
	post := &model.Post{
		ChannelId: args.ChannelID,
		Message:   args.Message,
		RootId:    args.RootID,
		FileIds:   fileIDs,
	}

	// Add AI-generated prop if tracking is enabled
	if p.trackAIGenerated {
		var userID string

		// First check if bot user ID was provided via context metadata (from embedded server)
		if mcpContext.BotUserID != "" && model.IsValidId(mcpContext.BotUserID) {
			userID = mcpContext.BotUserID
		} else {
			// For external servers, fetch the authenticated user's ID
			if user, _, getMeErr := client.GetMe(ctx, ""); getMeErr == nil && user != nil {
				userID = user.Id
			}
		}

		// Add the prop if we have a valid user ID
		if userID != "" {
			if post.Props == nil {
				post.Props = make(model.StringInterface)
			}
			post.Props["ai_generated_by"] = userID
		}
	}

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return mcptool.CreatePostOutput{}, fmt.Errorf("error creating post: %w", err)
	}

	out := mcptool.CreatePostOutput{
		Post:              createdPost,
		Channel:           channel,
		Team:              team,
		AttachmentMessage: attachmentMessage,
	}
	return out, nil
}

// toolCreatePostAsUser implements the create_post_as_user tool with custom authentication
func (p *MattermostToolProvider) toolCreatePostAsUser(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.CreatePostAsUserOutput, error) {
	var args CreatePostAsUserArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("failed to get arguments for tool create_post_as_user: %w", err)
	}

	// Validate required fields
	if args.Username == "" {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("username cannot be empty")
	}
	if args.Password == "" {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("password cannot be empty")
	}
	if !model.IsValidId(args.ChannelID) {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("channel_id must be a valid ID")
	}
	if args.Message == "" {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("message cannot be empty")
	}
	// Validate root ID if provided (for replies)
	if args.RootID != "" && !model.IsValidId(args.RootID) {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("root_id must be a valid ID")
	}

	// Create a new client and login as the specified user
	ctx := mcpContext.Ctx
	userClient := model.NewAPIv4Client(p.mmServerURL)

	// Login as the specified user
	user, _, err := userClient.Login(ctx, args.Username, args.Password)
	if err != nil {
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("login failed for user %s: %w", args.Username, err)
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
		return mcptool.CreatePostAsUserOutput{}, fmt.Errorf("error creating post as user %s: %w", args.Username, err)
	}

	out := mcptool.CreatePostAsUserOutput{
		Post:              createdPost,
		Username:          user.Username,
		AttachmentMessage: attachmentMessage,
	}
	return out, nil
}

// toolDM implements the dm tool
func (p *MattermostToolProvider) toolDM(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.DMOutput, error) {
	var args DMArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.DMOutput{}, fmt.Errorf("failed to get arguments for tool dm: %w", err)
	}

	// Validate required fields
	if args.Message == "" {
		return mcptool.DMOutput{}, fmt.Errorf("message cannot be empty")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return mcptool.DMOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Get current user information
	currentUser, _, err := client.GetMe(ctx, "")
	if err != nil {
		return mcptool.DMOutput{}, fmt.Errorf("error getting current user: %w", err)
	}

	// Resolve target user
	var targetUser *model.User
	dmSelf := false
	username := strings.TrimPrefix(args.Username, "@")
	if username != "" {
		targetUser, _, err = client.GetUserByUsername(ctx, username, "")
		if err != nil {
			return mcptool.DMOutput{}, fmt.Errorf("error getting user by username %q: %w", username, err)
		}
		dmSelf = targetUser.Id == currentUser.Id
	} else {
		targetUser = currentUser
		dmSelf = true
	}

	// Create or get direct channel
	dmChannel, _, err := client.CreateDirectChannel(ctx, currentUser.Id, targetUser.Id)
	if err != nil {
		return mcptool.DMOutput{}, fmt.Errorf("error creating direct channel: %w", err)
	}

	// Upload files if specified
	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, client, dmChannel.Id, args.Attachments, mcpContext.AccessMode)

	// Create the post in the DM channel
	post := &model.Post{
		ChannelId: dmChannel.Id,
		Message:   args.Message,
		FileIds:   fileIDs,
	}

	props := make(map[string]interface{})

	// Set from_webhook only when DM'ing yourself (prevents AI auto-response loop)
	if dmSelf {
		props["from_webhook"] = "true"
	}

	// Add AI-generated prop if tracking is enabled
	if p.trackAIGenerated {
		var userID string

		// First check if bot user ID was provided via context metadata (from embedded server)
		if mcpContext.BotUserID != "" && model.IsValidId(mcpContext.BotUserID) {
			userID = mcpContext.BotUserID
		} else {
			userID = currentUser.Id
		}

		if userID != "" {
			props["ai_generated_by"] = userID
		}
	}

	if len(props) > 0 {
		post.SetProps(props)
	}

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return mcptool.DMOutput{}, fmt.Errorf("error creating DM post: %w", err)
	}

	out := mcptool.DMOutput{
		Post:              createdPost,
		TargetUser:        targetUser,
		DMSelf:            dmSelf,
		AttachmentMessage: attachmentMessage,
	}
	return out, nil
}

// toolGroupMessage implements the group_message tool
func (p *MattermostToolProvider) toolGroupMessage(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.GroupMessageOutput, error) {
	var args GroupMessageArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("failed to get arguments for tool group_message: %w", err)
	}

	if args.Message == "" {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("message cannot be empty")
	}

	if mcpContext.Client == nil {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	currentUser, _, err := client.GetMe(ctx, "")
	if err != nil {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("error getting current user: %w", err)
	}

	// Resolve all targets into a deduplicated map of userID -> username
	targets := make(map[string]string)

	for _, uname := range args.Usernames {
		uname = strings.TrimPrefix(uname, "@")
		resolvedUser, _, resolveErr := client.GetUserByUsername(ctx, uname, "")
		if resolveErr != nil {
			return mcptool.GroupMessageOutput{}, fmt.Errorf("error getting user by username %q: %w", uname, resolveErr)
		}
		if resolvedUser.Id != currentUser.Id {
			targets[resolvedUser.Id] = resolvedUser.Username
		}
	}

	if len(targets) < 2 {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("need at least 2 target users, got %d", len(targets))
	}

	// Build member list: targets + current user
	memberIDs := make([]string, 0, len(targets)+1)
	for uid := range targets {
		memberIDs = append(memberIDs, uid)
	}
	memberIDs = append(memberIDs, currentUser.Id)

	gmChannel, _, err := client.CreateGroupChannel(ctx, memberIDs)
	if err != nil {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("error creating group channel: %w", err)
	}

	fileIDs, attachmentMessage := uploadFilesAndUrlsForLocal(ctx, client, gmChannel.Id, args.Attachments, mcpContext.AccessMode)

	post := &model.Post{
		ChannelId: gmChannel.Id,
		Message:   args.Message,
		FileIds:   fileIDs,
	}

	if p.trackAIGenerated {
		var userID string
		if mcpContext.BotUserID != "" && model.IsValidId(mcpContext.BotUserID) {
			userID = mcpContext.BotUserID
		} else {
			userID = currentUser.Id
		}
		if userID != "" {
			if post.Props == nil {
				post.Props = make(model.StringInterface)
			}
			post.Props["ai_generated_by"] = userID
		}
	}

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return mcptool.GroupMessageOutput{}, fmt.Errorf("error creating group message post: %w", err)
	}

	usernames := make([]string, 0, len(targets))
	for _, uname := range targets {
		usernames = append(usernames, uname)
	}

	out := mcptool.GroupMessageOutput{
		Post:              createdPost,
		Usernames:         usernames,
		AttachmentMessage: attachmentMessage,
	}
	return out, nil
}
