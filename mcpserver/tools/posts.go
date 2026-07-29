// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// ReadPostArgs represents arguments for the read_post tool
type ReadPostArgs struct {
	PostID        string `json:"post_id" jsonschema:"The ID of the post to read,minLength=26,maxLength=26"`
	IncludeThread *bool  `json:"include_thread,omitempty" jsonschema:"Whether to include the entire thread (default: true). Set false to fetch only the single post."`
}

// CreatePostArgs represents arguments for the create_post tool
type CreatePostArgs struct {
	ChannelID          string       `json:"channel_id" jsonschema:"The ID of the channel to post in,minLength=26,maxLength=26"`
	ChannelDisplayName string       `json:"channel_display_name" jsonschema:"The display name of the channel (for context verification),minLength=1"`
	TeamDisplayName    string       `json:"team_display_name" jsonschema:"The display name of the team (for context verification),minLength=1"`
	Message            string       `json:"message" jsonschema:"The message content,minLength=1"`
	RootID             string       `json:"root_id,omitempty" jsonschema:"Optional root post ID for replies,minLength=26,maxLength=26"`
	Attachments        []string     `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the post"`
	Files              []InlineFile `json:"files,omitempty" jsonschema:"Optional text files to create from inline content and attach to the post (max 10). The file name extension determines the file type."`
}

// CreatePostAsUserArgs represents arguments for the create_post_as_user tool (dev mode only)
type CreatePostAsUserArgs struct {
	Username    string   `json:"username" jsonschema:"Username to login as"`
	Password    string   `json:"password" jsonschema:"Password to login with"`
	ChannelID   string   `json:"channel_id" jsonschema:"The ID of the channel to post in,minLength=26,maxLength=26"`
	Message     string   `json:"message" jsonschema:"The message content"`
	RootID      string   `json:"root_id,omitempty" jsonschema:"Optional root post ID for replies,maxLength=26"`
	Props       string   `json:"props" jsonschema:"Optional post properties (JSON string)"`
	Attachments []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach to the post"`
}

// DMArgs represents arguments for the dm tool
type DMArgs struct {
	Username    string       `json:"username,omitempty" jsonschema:"Target username. If omitted the message is sent to yourself."`
	Message     string       `json:"message" jsonschema:"The message content to send,minLength=1"`
	Attachments []string     `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach"`
	Files       []InlineFile `json:"files,omitempty" jsonschema:"Optional text files to create from inline content and attach to the post (max 10). The file name extension determines the file type."`
}

// GroupMessageArgs represents arguments for the group_message tool
type GroupMessageArgs struct {
	Usernames   []string     `json:"usernames" jsonschema:"Target usernames (must be at least 2)."`
	Message     string       `json:"message" jsonschema:"The message content to send,minLength=1"`
	Attachments []string     `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file paths or URLs to attach"`
	Files       []InlineFile `json:"files,omitempty" jsonschema:"Optional text files to create from inline content and attach to the post (max 10). The file name extension determines the file type."`
}

// Tool description constants for post-related tools. The create_post/dm/group_message
// descriptions are format strings: getPostTools fills the %s with an access-mode-dependent
// attachments clause.
const (
	readPostDescription = "Read a specific post and its thread from Mattermost. Parameters: post_id (required), include_thread (boolean, default true). Returns post content, author info, and optionally all replies in the thread. Example: {\"post_id\": \"8xqzn3pfmtbyfkr9hqbw4hheoa\", \"include_thread\": true}"

	createPostDescriptionFmt = "Create a new post in Mattermost. IMPORTANT WORKFLOW: You MUST first call get_channel_info to obtain the channel_id, channel_display_name, and team_display_name. Present this context to the user before posting. Then call this tool with all required parameters. This ensures full transparency about where the message will be posted. Parameters: channel_id (required), message (required), root_id (optional - for replies), files (optional: create and attach text files inline by providing name + content; the extension sets the file type; max 10)%s. Returns created post details including ID and timestamp. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"message\": \"Hello team!\"}"

	dmDescriptionFmt = "Send a direct message to a user. Provide username to specify the recipient. If username is omitted, the message is sent to yourself. This is the DEFAULT way to message people — call it multiple times to message multiple people individually. Only use the group_message tool when the user explicitly asks for a group chat. Parameters: message (required), username (optional), files (optional: create and attach text files inline by providing name + content; the extension sets the file type; max 10)%s. Returns confirmation with message ID. Example: {\"message\": \"Hello!\", \"username\": \"john\"}"

	groupMessageDescriptionFmt = "Send a message to a shared group conversation with 2 or more other users. All participants can see each other's messages. ONLY use this when the user explicitly asks for a group message, group chat, or group conversation. If the user just asks to 'message' or 'send to' multiple people, use the dm tool once per person instead. Parameters: message (required), usernames (at least 2 required), files (optional: create and attach text files inline by providing name + content; the extension sets the file type; max 10)%s. Returns confirmation with message ID. Example: {\"message\": \"Hey team!\", \"usernames\": [\"alice\", \"bob\"]}"
)

// getPostTools returns all post-related tools
func (p *MattermostToolProvider) getPostTools() []MCPTool {
	// Build descriptions conditionally based on access mode
	attachmentsParam := ""
	if p.accessMode == AccessModeLocal {
		attachmentsParam = ", attachments (optional file paths/URLs)"
	}

	createPostDesc := fmt.Sprintf(createPostDescriptionFmt, attachmentsParam)

	dmDesc := fmt.Sprintf(dmDescriptionFmt, attachmentsParam)

	groupMessageDesc := fmt.Sprintf(groupMessageDescriptionFmt, attachmentsParam)

	return []MCPTool{
		{
			Name:        "read_post",
			Description: readPostDescription,
			Schema:      NewJSONSchemaForAccessMode[ReadPostArgs](string(p.accessMode)),
			Resolver:    typed("read_post", p.toolReadPost),
		},
		{
			Name:        "create_post",
			Description: createPostDesc,
			Schema:      NewJSONSchemaForAccessMode[CreatePostArgs](string(p.accessMode)),
			Resolver:    typed("create_post", p.toolCreatePost),
		},
		{
			Name:        "dm",
			Description: dmDesc,
			Schema:      NewJSONSchemaForAccessMode[DMArgs](string(p.accessMode)),
			Resolver:    typed("dm", p.toolDM),
		},
		{
			Name:        "group_message",
			Description: groupMessageDesc,
			Schema:      NewJSONSchemaForAccessMode[GroupMessageArgs](string(p.accessMode)),
			Resolver:    typed("group_message", p.toolGroupMessage),
		},
		{
			Name:        "get_post_info",
			Description: getPostInfoDescription,
			Schema:      NewJSONSchemaForAccessMode[GetPostInfoArgs](string(p.accessMode)),
			Resolver:    typed("get_post_info", p.toolGetPostInfo),
		},
		{
			Name:        "list_pinned_posts",
			Description: listPinnedPostsDescription,
			Schema:      NewJSONSchemaForAccessMode[ListPinnedPostsArgs](string(p.accessMode)),
			Resolver:    typed("list_pinned_posts", p.toolListPinnedPosts),
		},
		{
			Name:        "list_saved_posts",
			Description: listSavedPostsDescription,
			Schema:      NewJSONSchemaForAccessMode[ListSavedPostsArgs](string(p.accessMode)),
			Resolver:    typed("list_saved_posts", p.toolListSavedPosts),
		},
		{
			Name:        "update_post",
			Description: updatePostDescription,
			Schema:      NewJSONSchemaForAccessMode[UpdatePostArgs](string(p.accessMode)),
			Resolver:    typed("update_post", p.toolUpdatePost),
		},
		{
			Name:        "delete_post",
			Description: deletePostDescription,
			Schema:      NewJSONSchemaForAccessMode[DeletePostArgs](string(p.accessMode)),
			Resolver:    typed("delete_post", p.toolDeletePost),
		},
		{
			Name:        "pin_post",
			Description: pinPostDescription,
			Schema:      NewJSONSchemaForAccessMode[PinPostArgs](string(p.accessMode)),
			Resolver:    typed("pin_post", p.toolPinPost),
		},
		{
			Name:        "unpin_post",
			Description: unpinPostDescription,
			Schema:      NewJSONSchemaForAccessMode[UnpinPostArgs](string(p.accessMode)),
			Resolver:    typed("unpin_post", p.toolUnpinPost),
		},
		{
			Name:        "save_post",
			Description: savePostDescription,
			Schema:      NewJSONSchemaForAccessMode[SavePostArgs](string(p.accessMode)),
			Resolver:    typed("save_post", p.toolSavePost),
		},
		{
			Name:        "acknowledge_post",
			Description: acknowledgePostDescription,
			Schema:      NewJSONSchemaForAccessMode[AcknowledgePostArgs](string(p.accessMode)),
			Resolver:    typed("acknowledge_post", p.toolAcknowledgePost),
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
			Resolver:    typed("create_post_as_user", p.toolCreatePostAsUser),
		},
	}
}

// toolReadPost implements the read_post tool
func (p *MattermostToolProvider) toolReadPost(mcpContext *MCPToolContext, args ReadPostArgs) (string, error) {
	// Validate post ID
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	// Default include_thread to true when omitted; an explicit false fetches only
	// the single post.
	includeThread := args.IncludeThread == nil || *args.IncludeThread

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var posts []*model.Post

	if includeThread {
		// Get the thread
		postList, _, err := client.GetPostThread(ctx, args.PostID, "", false)
		if err != nil {
			return "", fmt.Errorf("error fetching post thread: %w", err)
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
			return "", fmt.Errorf("error fetching post: %w", err)
		}
		posts = []*model.Post{post}
	}

	if len(posts) == 0 {
		return "no posts found", nil
	}

	// Get channel and team info for context (using the first post's channel)
	var channelName, teamName string
	if len(posts) > 0 {
		channel, _, err := client.GetChannel(ctx, posts[0].ChannelId)
		if err == nil {
			channelName = channel.DisplayName
			team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
			if teamErr == nil {
				teamName = team.DisplayName
			}
		}
	}

	// Format the response
	var result strings.Builder
	if channelName != "" && teamName != "" {
		result.WriteString(fmt.Sprintf("Channel: %s (Team: %s)\n", channelName, teamName))
	}

	// Add Channel ID and Root ID to header
	if len(posts) > 0 {
		result.WriteString(fmt.Sprintf("Channel ID: %s\n", posts[0].ChannelId))

		// Find any post with a non-empty RootId - all replies share the same RootId
		var rootID string
		for _, post := range posts {
			if post.RootId != "" {
				rootID = post.RootId
				break
			}
		}

		if rootID != "" {
			result.WriteString(fmt.Sprintf("Root ID: %s\n", rootID))
		}
	}
	result.WriteString("\n")

	if includeThread && len(posts) > 1 {
		result.WriteString(fmt.Sprintf("Thread with %d posts:\n\n", len(posts)))
	}

	for i, post := range posts {
		user, _, err := client.GetUser(ctx, post.UserId, "")
		username := ""
		if err != nil {
			p.logger.Warn("failed to get user for post", "user_id", post.UserId, "error", err)
		} else {
			username = user.Username
		}
		format.WritePost(&result, format.PostEntry{
			HeaderLabel: fmt.Sprintf("Post %d", i+1),
			Username:    username,
			Post:        post,
		})
	}

	return result.String(), nil
}

// toolCreatePost implements the create_post tool
func (p *MattermostToolProvider) toolCreatePost(mcpContext *MCPToolContext, args CreatePostArgs) (string, error) {
	// Validate required fields
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}
	if args.ChannelDisplayName == "" {
		return "", fmt.Errorf("channel_display_name cannot be empty - you must call get_channel_info first")
	}
	if args.TeamDisplayName == "" {
		return "", fmt.Errorf("team_display_name cannot be empty - you must call get_channel_info first")
	}
	// Validate root ID if provided (for replies)
	if err := optionalID("root_id", args.RootID); err != nil {
		return "", err
	}

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Validate that the provided display names match the actual channel and team
	channel, _, err := client.GetChannel(ctx, args.ChannelID)
	if err != nil {
		return "", fmt.Errorf("error fetching channel for validation: %w", err)
	}

	// Check if channel display name matches
	if channel.DisplayName != args.ChannelDisplayName {
		return "", fmt.Errorf("channel_display_name mismatch: provided '%s' but channel ID '%s' has display name '%s'",
			args.ChannelDisplayName, args.ChannelID, channel.DisplayName)
	}

	// Get team info to validate team display name
	team, _, err := client.GetTeam(ctx, channel.TeamId, "")
	if err != nil {
		return "", fmt.Errorf("error fetching team for validation: %w", err)
	}

	// Check if team display name matches
	if team.DisplayName != args.TeamDisplayName {
		return "", fmt.Errorf("team_display_name mismatch: provided '%s' but team ID '%s' has display name '%s'",
			args.TeamDisplayName, channel.TeamId, team.DisplayName)
	}

	fileIDs, filesMessage, err := resolvePostFiles(ctx, client, args.ChannelID, args.Attachments, args.Files, mcpContext.AccessMode)
	if err != nil {
		return "", err
	}

	// Create the post
	post := &model.Post{
		ChannelId: args.ChannelID,
		Message:   args.Message,
		RootId:    args.RootID,
		FileIds:   fileIDs,
	}

	p.stampAIGenerated(post, mcpContext, "")

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return "", fmt.Errorf("error creating post: %w", err)
	}

	return fmt.Sprintf("Successfully created post in channel '%s' (Team: %s) with ID: %s%s",
		channel.DisplayName, team.DisplayName, createdPost.Id, filesMessage), nil
}

// toolCreatePostAsUser implements the create_post_as_user tool with custom authentication
func (p *MattermostToolProvider) toolCreatePostAsUser(mcpContext *MCPToolContext, args CreatePostAsUserArgs) (string, error) {
	// Validate required fields
	if args.Username == "" {
		return "", fmt.Errorf("username cannot be empty")
	}
	if args.Password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}
	// Validate root ID if provided (for replies)
	if err := optionalID("root_id", args.RootID); err != nil {
		return "", err
	}

	// Create a new client and login as the specified user
	ctx := mcpContext.Ctx
	userClient := model.NewAPIv4Client(p.mmServerURL)

	// Login as the specified user
	user, _, err := userClient.Login(ctx, args.Username, args.Password)
	if err != nil {
		return "", fmt.Errorf("login failed for user %s: %w", args.Username, err)
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
		return "", fmt.Errorf("error creating post as user %s: %w", args.Username, err)
	}

	return fmt.Sprintf("Successfully created post with ID %s as user %s%s", createdPost.Id, user.Username, attachmentMessage), nil
}

// toolDM implements the dm tool
func (p *MattermostToolProvider) toolDM(mcpContext *MCPToolContext, args DMArgs) (string, error) {
	// Validate required fields
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Get current user information
	currentUser, _, err := client.GetMe(ctx, "")
	if err != nil {
		return "", fmt.Errorf("error getting current user: %w", err)
	}

	// Resolve target user
	var targetUser *model.User
	dmSelf := false
	username := strings.TrimPrefix(args.Username, "@")
	if username != "" {
		targetUser, _, err = client.GetUserByUsername(ctx, username, "")
		if err != nil {
			return "", fmt.Errorf("error getting user by username %q: %w", username, err)
		}
		dmSelf = targetUser.Id == currentUser.Id
	} else {
		targetUser = currentUser
		dmSelf = true
	}

	// Create or get direct channel
	dmChannel, _, err := client.CreateDirectChannel(ctx, currentUser.Id, targetUser.Id)
	if err != nil {
		return "", fmt.Errorf("error creating direct channel: %w", err)
	}

	fileIDs, filesMessage, err := resolvePostFiles(ctx, client, dmChannel.Id, args.Attachments, args.Files, mcpContext.AccessMode)
	if err != nil {
		return "", err
	}

	// Create the post in the DM channel
	post := &model.Post{
		ChannelId: dmChannel.Id,
		Message:   args.Message,
		FileIds:   fileIDs,
	}

	// Set from_webhook only when DM'ing yourself (prevents AI auto-response loop)
	if dmSelf {
		post.AddProp("from_webhook", "true")
	}

	p.stampAIGenerated(post, mcpContext, currentUser.Id)

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return "", fmt.Errorf("error creating DM post: %w", err)
	}

	if dmSelf {
		return fmt.Sprintf("Successfully sent DM to yourself with ID: %s%s", createdPost.Id, filesMessage), nil
	}
	return fmt.Sprintf("Successfully sent DM to @%s with ID: %s%s", targetUser.Username, createdPost.Id, filesMessage), nil
}

// toolGroupMessage implements the group_message tool
func (p *MattermostToolProvider) toolGroupMessage(mcpContext *MCPToolContext, args GroupMessageArgs) (string, error) {
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	currentUser, _, err := client.GetMe(ctx, "")
	if err != nil {
		return "", fmt.Errorf("error getting current user: %w", err)
	}

	// Resolve all targets into a deduplicated map of userID -> username
	targets := make(map[string]string)

	for _, uname := range args.Usernames {
		uname = strings.TrimPrefix(uname, "@")
		resolvedUser, _, resolveErr := client.GetUserByUsername(ctx, uname, "")
		if resolveErr != nil {
			return "", fmt.Errorf("error getting user by username %q: %w", uname, resolveErr)
		}
		if resolvedUser.Id != currentUser.Id {
			targets[resolvedUser.Id] = resolvedUser.Username
		}
	}

	if len(targets) < 2 {
		return "", fmt.Errorf("group messages require at least 2 other users — for 1:1 DMs use the dm tool instead (got %d)", len(targets))
	}

	// Build member list: targets + current user
	memberIDs := make([]string, 0, len(targets)+1)
	for uid := range targets {
		memberIDs = append(memberIDs, uid)
	}
	memberIDs = append(memberIDs, currentUser.Id)

	gmChannel, _, err := client.CreateGroupChannel(ctx, memberIDs)
	if err != nil {
		return "", fmt.Errorf("error creating group channel: %w", err)
	}

	fileIDs, filesMessage, err := resolvePostFiles(ctx, client, gmChannel.Id, args.Attachments, args.Files, mcpContext.AccessMode)
	if err != nil {
		return "", err
	}

	post := &model.Post{
		ChannelId: gmChannel.Id,
		Message:   args.Message,
		FileIds:   fileIDs,
	}

	p.stampAIGenerated(post, mcpContext, currentUser.Id)

	createdPost, _, err := client.CreatePost(ctx, post)
	if err != nil {
		return "", fmt.Errorf("error creating group message post: %w", err)
	}

	// Build username list for success message
	usernames := make([]string, 0, len(targets))
	for _, uname := range targets {
		usernames = append(usernames, "@"+uname)
	}

	return fmt.Sprintf("Successfully sent group message to %s with ID: %s%s",
		strings.Join(usernames, ", "), createdPost.Id, filesMessage), nil
}

// --- Additional post tools (info, pins, saved, edit/delete, acknowledge) ---

// GetPostInfoArgs represents arguments for the get_post_info tool.
type GetPostInfoArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post,minLength=26,maxLength=26"`
}

// ListPinnedPostsArgs represents arguments for the list_pinned_posts tool.
type ListPinnedPostsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The ID of the channel,minLength=26,maxLength=26"`
}

// ListSavedPostsArgs represents arguments for the list_saved_posts tool.
type ListSavedPostsArgs struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"Optional channel ID to scope saved posts to one channel,maxLength=26"`
	TeamID    string `json:"team_id,omitempty" jsonschema:"Optional team ID to scope saved posts to one team,maxLength=26"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Number of posts per page (default: 30, max: 100),minimum=1,maximum=100"`
}

// UpdatePostArgs represents arguments for the update_post tool.
type UpdatePostArgs struct {
	PostID  string `json:"post_id" jsonschema:"The ID of the post to edit,minLength=26,maxLength=26"`
	Message string `json:"message" jsonschema:"The new message content,minLength=1"`
}

// DeletePostArgs represents arguments for the delete_post tool.
type DeletePostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to delete,minLength=26,maxLength=26"`
}

// PinPostArgs represents arguments for the pin_post tool.
type PinPostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to pin,minLength=26,maxLength=26"`
}

// UnpinPostArgs represents arguments for the unpin_post tool.
type UnpinPostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to unpin,minLength=26,maxLength=26"`
}

// SavePostArgs represents arguments for the save_post tool.
type SavePostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to save (flag) for yourself,minLength=26,maxLength=26"`
}

// AcknowledgePostArgs represents arguments for the acknowledge_post tool.
type AcknowledgePostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to acknowledge,minLength=26,maxLength=26"`
}

const (
	getPostInfoDescription     = "Get metadata and channel/team context for a single post (without the thread). Parameters: post_id (required). Use read_post to get the full thread content. Returns channel, team, type, and your membership."
	listPinnedPostsDescription = "List the pinned posts in a channel. Parameters: channel_id (required). Returns each pinned post's author, content, and timestamp."
	listSavedPostsDescription  = "List your saved (flagged) posts, optionally scoped to a channel or team. Parameters: channel_id (optional), team_id (optional), page, per_page. Returns matching saved posts."
	updatePostDescription      = "Edit the message text of an existing post. Parameters: post_id (required), message (required). Returns the updated post."
	deletePostDescription      = "Delete (soft-remove) a post by ID. Parameters: post_id (required). This cannot be undone by the agent."
	pinPostDescription         = "Pin a post to its channel. Parameters: post_id (required)."
	unpinPostDescription       = "Unpin a post from its channel. Parameters: post_id (required)."
	savePostDescription        = "Save (flag) a post for yourself so you can find it later. Parameters: post_id (required)."
	acknowledgePostDescription = "Add your acknowledgement to a post that requests one. Parameters: post_id (required)."
)

// toolGetPostInfo implements the get_post_info tool.
func (p *MattermostToolProvider) toolGetPostInfo(mcpContext *MCPToolContext, args GetPostInfoArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	info, _, err := mcpContext.Client.GetPostInfo(mcpContext.Ctx, args.PostID)
	if err != nil {
		return "", fmt.Errorf("error fetching post info: %w", err)
	}

	var result strings.Builder
	format.WritePostInfo(&result, format.PostInfoEntry{PostID: args.PostID, PostInfo: info})
	return result.String(), nil
}

// toolListPinnedPosts implements the list_pinned_posts tool.
func (p *MattermostToolProvider) toolListPinnedPosts(mcpContext *MCPToolContext, args ListPinnedPostsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	postList, _, err := mcpContext.Client.GetPinnedPosts(mcpContext.Ctx, args.ChannelID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching pinned posts: %w", err)
	}

	return p.formatPostListChrono(mcpContext, postList, "pinned posts"), nil
}

// toolListSavedPosts implements the list_saved_posts tool.
func (p *MattermostToolProvider) toolListSavedPosts(mcpContext *MCPToolContext, args ListSavedPostsArgs) (string, error) {
	if err := optionalID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if args.PerPage <= 0 {
		args.PerPage = 30
	}
	if args.PerPage > 100 {
		args.PerPage = 100
	}
	if args.Page < 0 {
		args.Page = 0
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var postList *model.PostList
	switch {
	case args.ChannelID != "":
		postList, _, err = client.GetFlaggedPostsForUserInChannel(ctx, userID, args.ChannelID, args.Page, args.PerPage)
	case args.TeamID != "":
		postList, _, err = client.GetFlaggedPostsForUserInTeam(ctx, userID, args.TeamID, args.Page, args.PerPage)
	default:
		postList, _, err = client.GetFlaggedPostsForUser(ctx, userID, args.Page, args.PerPage)
	}
	if err != nil {
		return "", fmt.Errorf("error fetching saved posts: %w", err)
	}

	return p.formatPostListChrono(mcpContext, postList, "saved posts"), nil
}

// toolUpdatePost implements the update_post tool.
func (p *MattermostToolProvider) toolUpdatePost(mcpContext *MCPToolContext, args UpdatePostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	post, _, err := client.GetPost(ctx, args.PostID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching post: %w", err)
	}

	post.Message = args.Message
	p.stampAIGenerated(post, mcpContext, "")

	updated, _, err := client.UpdatePost(ctx, args.PostID, post)
	if err != nil {
		return "", fmt.Errorf("error updating post: %w", err)
	}

	return fmt.Sprintf("Successfully updated post %s", updated.Id), nil
}

// toolDeletePost implements the delete_post tool.
func (p *MattermostToolProvider) toolDeletePost(mcpContext *MCPToolContext, args DeletePostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.DeletePost(mcpContext.Ctx, args.PostID); err != nil {
		return "", fmt.Errorf("error deleting post: %w", err)
	}

	return fmt.Sprintf("Successfully deleted post %s", args.PostID), nil
}

// toolPinPost implements the pin_post tool.
func (p *MattermostToolProvider) toolPinPost(mcpContext *MCPToolContext, args PinPostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.PinPost(mcpContext.Ctx, args.PostID); err != nil {
		return "", fmt.Errorf("error pinning post: %w", err)
	}

	return fmt.Sprintf("Successfully pinned post %s", args.PostID), nil
}

// toolUnpinPost implements the unpin_post tool.
func (p *MattermostToolProvider) toolUnpinPost(mcpContext *MCPToolContext, args UnpinPostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.UnpinPost(mcpContext.Ctx, args.PostID); err != nil {
		return "", fmt.Errorf("error unpinning post: %w", err)
	}

	return fmt.Sprintf("Successfully unpinned post %s", args.PostID), nil
}

// toolSavePost implements the save_post tool. There is no dedicated endpoint; a
// saved (flagged) post is stored as a user preference.
func (p *MattermostToolProvider) toolSavePost(mcpContext *MCPToolContext, args SavePostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	preferences := model.Preferences{{
		UserId:   userID,
		Category: model.PreferenceCategoryFlaggedPost,
		Name:     args.PostID,
		Value:    "true",
	}}

	if _, err := mcpContext.Client.UpdatePreferences(mcpContext.Ctx, userID, preferences); err != nil {
		return "", fmt.Errorf("error saving post: %w", err)
	}

	return fmt.Sprintf("Successfully saved post %s", args.PostID), nil
}

// toolAcknowledgePost implements the acknowledge_post tool.
func (p *MattermostToolProvider) toolAcknowledgePost(mcpContext *MCPToolContext, args AcknowledgePostArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, _, err := mcpContext.Client.AcknowledgePost(mcpContext.Ctx, args.PostID, userID); err != nil {
		return "", fmt.Errorf("error acknowledging post: %w", err)
	}

	return fmt.Sprintf("Successfully acknowledged post %s", args.PostID), nil
}

// formatPostListChrono renders a PostList in chronological order with author
// usernames, reusing the format package. noun labels the listing in the header.
func (p *MattermostToolProvider) formatPostListChrono(mcpContext *MCPToolContext, postList *model.PostList, noun string) string {
	if postList == nil || len(postList.Posts) == 0 {
		return fmt.Sprintf("no %s found", noun)
	}

	posts := make([]*model.Post, 0, len(postList.Posts))
	for _, post := range postList.Posts {
		posts = append(posts, post)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreateAt < posts[j].CreateAt
	})

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	userIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, post := range posts {
		if !seen[post.UserId] {
			seen[post.UserId] = true
			userIDs = append(userIDs, post.UserId)
		}
	}
	userCache := make(map[string]string)
	if users, _, err := client.GetUsersByIds(ctx, userIDs); err != nil {
		p.logger.Warn("failed to fetch users by IDs", "error", err)
	} else {
		for _, user := range users {
			userCache[user.Id] = user.Username
		}
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d %s:\n\n", len(posts), noun))
	for i, post := range posts {
		username := userCache[post.UserId]
		if username == "" {
			username = "Unknown User"
		}
		format.WritePost(&result, format.PostEntry{
			HeaderLabel: fmt.Sprintf("Post %d", i+1),
			Username:    username,
			Post:        post,
			ShowChannel: true,
		})
	}
	return result.String()
}

// stampAIGenerated marks a post as AI-generated (the ai_generated_by prop) when
// AI-content tracking is enabled. It prefers the bot user id supplied via request
// metadata (embedded servers); otherwise it uses fallbackUserID (the authenticated
// user the caller already resolved), and as a last resort fetches the current user.
// It is a no-op when tracking is disabled or no user id can be determined.
func (p *MattermostToolProvider) stampAIGenerated(post *model.Post, mcpContext *MCPToolContext, fallbackUserID string) {
	if !p.trackAIGenerated {
		return
	}

	var userID string
	switch {
	case mcpContext.BotUserID != "" && model.IsValidId(mcpContext.BotUserID):
		userID = mcpContext.BotUserID
	case fallbackUserID != "":
		userID = fallbackUserID
	case mcpContext.Client != nil:
		if user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, ""); err == nil && user != nil {
			userID = user.Id
		}
	}

	if userID != "" {
		post.AddProp("ai_generated_by", userID)
	}
}
