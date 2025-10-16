// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// ReadChannelArgs represents arguments for the read_channel tool
type ReadChannelArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The ID of the channel to read from,minLength=26,maxLength=26"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Number of posts to retrieve (default: 20, max: 100),minimum=1,maximum=100"`
	Since     string `json:"since,omitempty" jsonschema:"Only get posts since this timestamp (ISO 8601 format),format=date-time"`
}

// CreateChannelArgs represents arguments for the create_channel tool
type CreateChannelArgs struct {
	Name        string `json:"name" jsonschema:"The channel name (URL-friendly),minLength=1,maxLength=64"`
	DisplayName string `json:"display_name" jsonschema:"The channel display name,minLength=1,maxLength=64"`
	Type        string `json:"type" jsonschema:"Channel type,enum=O,enum=P"`
	TeamID      string `json:"team_id" jsonschema:"The team ID where the channel will be created,minLength=26,maxLength=26"`
	Purpose     string `json:"purpose" jsonschema:"Optional channel purpose,maxLength=250"`
	Header      string `json:"header" jsonschema:"Optional channel header,maxLength=1024"`
}

// GetChannelInfoArgs represents arguments for the get_channel_info tool
type GetChannelInfoArgs struct {
	ChannelID          string `json:"channel_id,omitempty" jsonschema:"The exact channel ID (fastest, most reliable method),maxLength=26"`
	ChannelDisplayName string `json:"channel_display_name,omitempty" jsonschema:"The human-readable display name users see (e.g. 'General Discussion'). Try this first for user-provided names.,maxLength=64"`
	ChannelName        string `json:"channel_name,omitempty" jsonschema:"The URL-friendly channel name (e.g. 'general-discussion'). Use this only if display_name doesn't work.,maxLength=64"`
	TeamID             string `json:"team_id,omitempty" jsonschema:"Team ID (optional - if provided, searches within specific team; if omitted, searches across all teams),maxLength=26"`
}

// GetChannelMembersArgs represents arguments for the get_channel_members tool
type GetChannelMembersArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"ID of the channel to get members for,minLength=26,maxLength=26"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Number of members to return (default: 50, max: 200),minimum=1,maximum=200"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
}

// AddUserToChannelArgs represents arguments for the add_user_to_channel tool
type AddUserToChannelArgs struct {
	UserID    string `json:"user_id" jsonschema:"ID of the user to add"`
	ChannelID string `json:"channel_id" jsonschema:"ID of the channel to add user to"`
}

// getChannelTools returns all channel-related tools
func (p *MattermostToolProvider) getChannelTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "read_channel",
			Description: "Read recent posts from a Mattermost channel. Parameters: channel_id (required), limit (1-100, default 20), since (ISO 8601 timestamp, optional). Returns post details including author, content, and timestamps. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"limit\": 10, \"since\": \"2024-01-01T00:00:00Z\"}",
			Schema:      llm.NewJSONSchemaFromStruct[ReadChannelArgs](),
			Resolver:    p.toolReadChannel,
		},
		{
			Name:        "create_channel",
			Description: "Create a new channel in Mattermost. Parameters: name (URL-friendly), display_name (user-visible), type ('O' for public, 'P' for private), team_id (required), purpose (optional), header (optional). Returns created channel details. Example: {\"name\": \"dev-chat\", \"display_name\": \"Development Chat\", \"type\": \"O\", \"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\"}",
			Schema:      llm.NewJSONSchemaFromStruct[CreateChannelArgs](),
			Resolver:    p.toolCreateChannel,
		},
		{
			Name:        "get_channel_info",
			Description: "Get information about a channel. Provide ONE parameter: channel_id (fastest), channel_display_name (user-visible), or channel_name (URL name). Optional: team_id to limit search scope. Returns channel metadata including ID, names, type, team, purpose, and member count. Example: {\"channel_display_name\": \"General\"} or {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\"}",
			Schema:      llm.NewJSONSchemaFromStruct[GetChannelInfoArgs](),
			Resolver:    p.toolGetChannelInfo,
		},
		{
			Name:        "get_channel_members",
			Description: "Get members of a channel with pagination support. Parameters: channel_id (required), limit (1-200, default 50), page (0+, default 0). Returns user details for each member including username, email, display name, and join date. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"limit\": 25, \"page\": 0}",
			Schema:      llm.NewJSONSchemaFromStruct[GetChannelMembersArgs](),
			Resolver:    p.toolGetChannelMembers,
		},
		{
			Name:        "add_user_to_channel",
			Description: "Add a user to a channel. Parameters: user_id (required), channel_id (required). Returns confirmation message.",
			Schema:      llm.NewJSONSchemaFromStruct[AddUserToChannelArgs](),
			Resolver:    p.toolAddUserToChannel,
		},
	}
}

// toolReadChannel implements the read_channel tool
func (p *MattermostToolProvider) toolReadChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args ReadChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool read_channel: %w", err)
	}

	// Validate channel ID
	if !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}

	// Set defaults and validate
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

	// Parse since timestamp if provided
	var since int64
	if args.Since != "" {
		parsedTime, parseErr := time.Parse(time.RFC3339, args.Since)
		if parseErr != nil {
			return "invalid since timestamp format", fmt.Errorf("invalid timestamp format: %w", parseErr)
		}
		since = parsedTime.Unix() * 1000 // Convert to milliseconds
	}

	// Get posts from the channel
	posts, _, err := client.GetPostsForChannel(ctx, args.ChannelID, 0, args.Limit, "", false, false)
	if err != nil {
		return "failed to fetch channel posts", fmt.Errorf("error fetching posts: %w", err)
	}

	// Filter by since timestamp if provided
	var filteredPosts []*model.Post
	for _, post := range posts.ToSlice() {
		if since == 0 || post.CreateAt >= since {
			filteredPosts = append(filteredPosts, post)
		}
	}

	if len(filteredPosts) == 0 {
		return "no posts found in the specified timeframe", nil
	}

	// Format the response
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d posts in channel:\n\n", len(filteredPosts)))

	for i, post := range filteredPosts {
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

// toolCreateChannel implements the create_channel tool
func (p *MattermostToolProvider) toolCreateChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_channel: %w", err)
	}

	// Validate required fields
	if args.Name == "" {
		return "name is required", fmt.Errorf("name cannot be empty")
	}
	if args.DisplayName == "" {
		return "display_name is required", fmt.Errorf("display_name cannot be empty")
	}
	if args.Type == "" {
		return "type is required", fmt.Errorf("type cannot be empty")
	}
	if !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}

	// Validate channel type
	if args.Type != "O" && args.Type != "P" {
		return "type must be 'O' for public or 'P' for private", fmt.Errorf("invalid channel type: %s", args.Type)
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Create the channel
	channel := &model.Channel{
		TeamId:      args.TeamID,
		Type:        model.ChannelType(args.Type),
		DisplayName: args.DisplayName,
		Name:        args.Name,
		Purpose:     args.Purpose,
		Header:      args.Header,
	}

	createdChannel, _, err := client.CreateChannel(ctx, channel)
	if err != nil {
		return "failed to create channel", fmt.Errorf("error creating channel: %w", err)
	}

	return fmt.Sprintf("Successfully created channel '%s' with ID: %s", createdChannel.DisplayName, createdChannel.Id), nil
}

// toolGetChannelInfo implements the get_channel_info tool
func (p *MattermostToolProvider) toolGetChannelInfo(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetChannelInfoArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_channel_info: %w", err)
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Validate team ID if provided
	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}

	var channel *model.Channel

	var lastError error

	// Try different lookup methods based on provided parameters
	switch {
	case args.ChannelID != "":
		// Validate channel ID format
		if !model.IsValidId(args.ChannelID) {
			return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
		}
		// Direct ID lookup - fastest method
		channel, _, err = client.GetChannel(ctx, args.ChannelID, "")
		if err != nil {
			return "channel not found by ID", fmt.Errorf("error fetching channel by ID: %w", err)
		}
	case args.ChannelDisplayName != "" || args.ChannelName != "":
		// Prioritize display name over name - try display name first if provided
		if args.ChannelDisplayName != "" {
			channel, lastError = p.tryFindChannelByDisplayName(ctx, client, args.ChannelDisplayName, args.TeamID)
			if channel != nil {
				break // Found it with display name
			}
		}

		// If display name didn't work (or wasn't provided), try channel name
		if args.ChannelName != "" {
			channel, err = p.tryFindChannelByName(ctx, client, args.ChannelName, args.TeamID)
			if err != nil {
				// If we also failed with display name, return combined error message
				if lastError != nil {
					return fmt.Sprintf("channel not found by display name '%s' or name '%s'", args.ChannelDisplayName, args.ChannelName),
						fmt.Errorf("display name error: %v; name error: %v", lastError, err)
				}
				return fmt.Sprintf("channel not found by name '%s'", args.ChannelName), err
			}
		} else if lastError != nil {
			// Only display name was provided and it failed
			return fmt.Sprintf("channel not found by display name '%s'", args.ChannelDisplayName), lastError
		}

		if channel == nil {
			return "no channel found with the provided parameters", fmt.Errorf("channel lookup failed")
		}
	default:
		return "either channel_id or channel_name/channel_display_name must be provided", fmt.Errorf("insufficient parameters for channel lookup")
	}

	// Format the response
	var result strings.Builder
	result.WriteString("Channel Information:\n")
	result.WriteString(fmt.Sprintf("ID: %s\n", channel.Id))
	result.WriteString(fmt.Sprintf("Name: %s\n", channel.Name))
	result.WriteString(fmt.Sprintf("Display Name: %s\n", channel.DisplayName))
	result.WriteString(fmt.Sprintf("Type: %s\n", channel.Type))
	result.WriteString(fmt.Sprintf("Team ID: %s\n", channel.TeamId))

	if channel.Purpose != "" {
		result.WriteString(fmt.Sprintf("Purpose: %s\n", channel.Purpose))
	}
	if channel.Header != "" {
		result.WriteString(fmt.Sprintf("Header: %s\n", channel.Header))
	}

	result.WriteString(fmt.Sprintf("Created: %s\n", time.Unix(channel.CreateAt/1000, 0).Format("2006-01-02 15:04:05")))

	// Get member count
	memberCount, _, err := client.GetChannelStats(ctx, channel.Id, "", false)
	if err == nil {
		result.WriteString(fmt.Sprintf("Member Count: %s\n", strconv.FormatInt(memberCount.MemberCount, 10)))
	}

	return result.String(), nil
}

// toolGetChannelMembers implements the get_channel_members tool
func (p *MattermostToolProvider) toolGetChannelMembers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetChannelMembersArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_channel_members: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}

	// Set defaults and validate
	if args.Limit == 0 {
		args.Limit = 50
	}
	if args.Limit > 200 {
		args.Limit = 200
	}
	if args.Page < 0 {
		args.Page = 0
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Get channel members
	members, _, err := client.GetChannelMembers(ctx, args.ChannelID, args.Page, args.Limit, "")
	if err != nil {
		return "failed to fetch channel members", fmt.Errorf("error fetching channel members: %w", err)
	}

	if len(members) == 0 {
		return "no members found in this channel", nil
	}

	// Get user details for each member
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Channel Members (page %d, showing %d members):\n\n", args.Page, len(members)))

	for i, member := range members {
		user, _, err := client.GetUser(ctx, member.UserId, "")
		if err != nil {
			p.logger.Warn("failed to get user details for member", "user_id", member.UserId, "error", err)
			result.WriteString(fmt.Sprintf("%d. User ID: %s (details unavailable)\n", i+1, member.UserId))
			continue
		}

		result.WriteString(fmt.Sprintf("%d. **%s**", i+1, user.Username))

		if user.FirstName != "" || user.LastName != "" {
			result.WriteString(fmt.Sprintf(" (%s %s)", user.FirstName, user.LastName))
		}

		result.WriteString(fmt.Sprintf("\n   ID: %s\n", user.Id))

		if user.Email != "" {
			result.WriteString(fmt.Sprintf("   Email: %s\n", user.Email))
		}

		// Add role information
		roles := strings.Split(member.Roles, " ")
		if len(roles) > 0 && roles[0] != "" {
			result.WriteString(fmt.Sprintf("   Roles: %s\n", strings.Join(roles, ", ")))
		}

		result.WriteString("\n")
	}

	return result.String(), nil
}

// toolAddUserToChannel implements the add_user_to_channel tool using the context client
func (p *MattermostToolProvider) toolAddUserToChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args AddUserToChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool add_user_to_channel: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.UserID) {
		return "invalid user_id format", fmt.Errorf("user_id must be a valid ID")
	}
	if !model.IsValidId(args.ChannelID) {
		return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Add user to channel
	_, _, err = client.AddChannelMember(ctx, args.ChannelID, args.UserID)
	if err != nil {
		return "failed to add user to channel", fmt.Errorf("error adding user to channel: %w", err)
	}

	// Get user and channel info for confirmation
	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	channel, _, channelErr := client.GetChannel(ctx, args.ChannelID, "")

	if userErr != nil || channelErr != nil {
		return fmt.Sprintf("Successfully added user %s to channel %s", args.UserID, args.ChannelID), nil
	}

	return fmt.Sprintf("Successfully added user '%s' to channel '%s'", user.Username, channel.DisplayName), nil
}

// tryFindChannelByDisplayName attempts to find a channel by display name
func (p *MattermostToolProvider) tryFindChannelByDisplayName(ctx context.Context, client *model.Client4, displayName, teamID string) (*model.Channel, error) {
	if teamID != "" {
		// Search within specific team
		user, _, userErr := client.GetMe(ctx, "")
		if userErr != nil {
			return nil, fmt.Errorf("error getting current user: %w", userErr)
		}

		channels, _, channelErr := client.GetChannelsForTeamForUser(ctx, teamID, user.Id, false, "")
		if channelErr != nil {
			return nil, fmt.Errorf("error fetching team channels: %w", channelErr)
		}

		for _, ch := range channels {
			if ch.DisplayName == displayName {
				return ch, nil
			}
		}

		return nil, fmt.Errorf("no channel found with display name: %s in team", displayName)
	}

	// Search across all teams
	channels, _, searchErr := client.SearchAllChannelsForUser(ctx, displayName)
	if searchErr != nil {
		return nil, fmt.Errorf("error searching channels: %w", searchErr)
	}

	// Find exact match by display name
	for _, ch := range channels {
		if ch.DisplayName == displayName {
			// Convert ChannelWithTeamData to Channel
			return &model.Channel{
				Id:               ch.Id,
				CreateAt:         ch.CreateAt,
				UpdateAt:         ch.UpdateAt,
				DeleteAt:         ch.DeleteAt,
				TeamId:           ch.TeamId,
				Type:             ch.Type,
				DisplayName:      ch.DisplayName,
				Name:             ch.Name,
				Header:           ch.Header,
				Purpose:          ch.Purpose,
				LastPostAt:       ch.LastPostAt,
				TotalMsgCount:    ch.TotalMsgCount,
				ExtraUpdateAt:    ch.ExtraUpdateAt,
				CreatorId:        ch.CreatorId,
				SchemeId:         ch.SchemeId,
				Props:            ch.Props,
				GroupConstrained: ch.GroupConstrained,
			}, nil
		}
	}

	return nil, fmt.Errorf("no channel found with display name: %s across all teams", displayName)
}

// tryFindChannelByName attempts to find a channel by name
func (p *MattermostToolProvider) tryFindChannelByName(ctx context.Context, client *model.Client4, name, teamID string) (*model.Channel, error) {
	if teamID != "" {
		// Search within specific team
		channel, _, err := client.GetChannelByName(ctx, name, teamID, "")
		if err != nil {
			return nil, fmt.Errorf("error fetching channel by name in team: %w", err)
		}
		return channel, nil
	}

	// Search across all teams
	channels, _, searchErr := client.SearchAllChannelsForUser(ctx, name)
	if searchErr != nil {
		return nil, fmt.Errorf("error searching channels: %w", searchErr)
	}

	// Find exact match by name
	for _, ch := range channels {
		if ch.Name == name {
			// Convert ChannelWithTeamData to Channel
			return &model.Channel{
				Id:               ch.Id,
				CreateAt:         ch.CreateAt,
				UpdateAt:         ch.UpdateAt,
				DeleteAt:         ch.DeleteAt,
				TeamId:           ch.TeamId,
				Type:             ch.Type,
				DisplayName:      ch.DisplayName,
				Name:             ch.Name,
				Header:           ch.Header,
				Purpose:          ch.Purpose,
				LastPostAt:       ch.LastPostAt,
				TotalMsgCount:    ch.TotalMsgCount,
				ExtraUpdateAt:    ch.ExtraUpdateAt,
				CreatorId:        ch.CreatorId,
				SchemeId:         ch.SchemeId,
				Props:            ch.Props,
				GroupConstrained: ch.GroupConstrained,
			}, nil
		}
	}

	return nil, fmt.Errorf("no channel found with name: %s across all teams", name)
}
