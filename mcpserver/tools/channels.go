// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
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
	ChannelID   string `json:"channel_id,omitempty" jsonschema:"The exact channel ID (fastest, most reliable method),maxLength=26"`
	ChannelName string `json:"channel_name,omitempty" jsonschema:"Channel name to search for — matches against both display name and URL name (case-insensitive, supports partial matches),maxLength=64"`
	TeamID      string `json:"team_id,omitempty" jsonschema:"Team ID (optional - if provided, searches within specific team; if omitted, searches across all teams),maxLength=26"`
}

// GetChannelMembersArgs represents arguments for the get_channel_members tool
type GetChannelMembersArgs struct {
	ChannelID   string `json:"channel_id" jsonschema:"ID of the channel to get members for,minLength=26,maxLength=26"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Number of members to return (default: 50, max: 200),minimum=1,maximum=200"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	ExcludeBots *bool  `json:"exclude_bots,omitempty" jsonschema:"Exclude bot accounts from results (default: true)"`
}

// AddUserToChannelArgs represents arguments for the add_user_to_channel tool
type AddUserToChannelArgs struct {
	UserID    string `json:"user_id" jsonschema:"ID of the user to add"`
	ChannelID string `json:"channel_id" jsonschema:"ID of the channel to add user to"`
}

// GetUserChannelsArgs represents arguments for the get_user_channels tool
type GetUserChannelsArgs struct {
	TeamID  string `json:"team_id,omitempty" jsonschema:"Optional team ID to filter channels by team,maxLength=26"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Number of channels per page (default: 60, max: 200),minimum=1,maximum=200"`
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
			Description: "Get information about channel(s). Provide channel_id (fastest) or channel_name (matches against both display name and URL name, case-insensitive, supports partial matches). Optional: team_id to limit search scope. If multiple channels match (e.g., 'General' exists in multiple teams), returns ALL matches with team context for disambiguation. Returns channel metadata including ID, names, type, team, purpose, and member count. Example: {\"channel_name\": \"General\"} or {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\"}",
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
		{
			Name:        "get_user_channels",
			Description: "Get channels the current user is a member of, including DMs and GMs. Parameters: team_id (optional, filter by team), page (default 0), per_page (1-200, default 60). Returns channel details with team info and pagination. Example: {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\", \"per_page\": 60}",
			Schema:      llm.NewJSONSchemaFromStruct[GetUserChannelsArgs](),
			Resolver:    p.toolGetUserChannels,
		},
	}
}

// toolReadChannel implements the read_channel tool.
// It reads recent posts from a channel and formats them with author usernames.
// Uses GetUsersByIds to fetch all authors in a single API call.
// Makes a single GetTeam call for the channel's team context (acceptable for one channel).
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

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Parse since timestamp if provided
	var since int64
	if args.Since != "" {
		parsedTime, parseErr := time.Parse(time.RFC3339, args.Since)
		if parseErr != nil {
			return "invalid since timestamp format", fmt.Errorf("invalid timestamp format: %w", parseErr)
		}
		since = parsedTime.Unix() * 1000 // Convert to milliseconds
	}

	// Get channel info for context
	channel, _, err := client.GetChannel(ctx, args.ChannelID)
	if err != nil {
		return "failed to fetch channel info", fmt.Errorf("error fetching channel: %w", err)
	}

	// Determine team display name; DMs/Groups have no team
	channelDisplayName := channel.DisplayName
	if channelDisplayName == "" {
		switch channel.Type {
		case model.ChannelTypeDirect:
			channelDisplayName = "Direct Message"
		case model.ChannelTypeGroup:
			channelDisplayName = "Group Message"
		default:
			channelDisplayName = channel.Name
		}
	}

	teamDisplayName := ""
	if channel.TeamId == "" {
		switch channel.Type {
		case model.ChannelTypeDirect:
			teamDisplayName = "Direct Message"
		case model.ChannelTypeGroup:
			teamDisplayName = "Group Message"
		default:
			teamDisplayName = "No Team"
		}
	} else {
		team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
		if teamErr != nil {
			return "failed to fetch team info", fmt.Errorf("error fetching team: %w", teamErr)
		}
		teamDisplayName = team.DisplayName
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

	// Sort chronologically (oldest first) for natural reading order
	sort.Slice(filteredPosts, func(i, j int) bool {
		return filteredPosts[i].CreateAt < filteredPosts[j].CreateAt
	})

	// Collect unique user IDs and fetch all at once
	userIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, post := range filteredPosts {
		if !seen[post.UserId] {
			seen[post.UserId] = true
			userIDs = append(userIDs, post.UserId)
		}
	}

	userCache := make(map[string]string)
	users, _, err := client.GetUsersByIds(ctx, userIDs)
	if err != nil {
		p.logger.Warn("failed to fetch users by IDs", "error", err)
		for _, id := range userIDs {
			userCache[id] = "Unknown User"
		}
	} else {
		for _, user := range users {
			userCache[user.Id] = user.Username
		}
		// Mark any IDs not returned as unknown
		for _, id := range userIDs {
			if _, exists := userCache[id]; !exists {
				userCache[id] = "Unknown User"
			}
		}
	}

	// Format the response
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Channel: %s (Team: %s)\n", channelDisplayName, teamDisplayName))
	result.WriteString(fmt.Sprintf("Found %d posts:\n\n", len(filteredPosts)))

	postIndex := format.BuildPostIndex(filteredPosts)
	for i, post := range filteredPosts {
		var replyAnnotation string
		if post.RootId != "" {
			if parentNum, ok := postIndex[post.RootId]; ok {
				replyAnnotation = fmt.Sprintf("(reply to Post %d)", parentNum)
			}
		}
		format.WritePost(&result, format.PostEntry{
			HeaderLabel:     fmt.Sprintf("Post %d", i+1),
			Username:        userCache[post.UserId],
			ReplyAnnotation: replyAnnotation,
			Post:            post,
		})
	}

	return result.String(), nil
}

// toolCreateChannel implements the create_channel tool.
// Creates a new public or private channel in a specified team.
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

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

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

// toolGetChannelInfo implements the get_channel_info tool.
func (p *MattermostToolProvider) toolGetChannelInfo(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetChannelInfoArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_channel_info: %w", err)
	}

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Validate team ID if provided
	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}

	var channels []*model.Channel

	var lastError error

	// Try different lookup methods based on provided parameters
	switch {
	case args.ChannelID != "":
		// Validate channel ID format
		if !model.IsValidId(args.ChannelID) {
			return "invalid channel_id format", fmt.Errorf("channel_id must be a valid ID")
		}
		// Direct ID lookup - fastest method, always returns single result
		var channel *model.Channel
		var resp *model.Response
		channel, resp, err = client.GetChannel(ctx, args.ChannelID)
		if err != nil {
			// Check if it's a 404 (not found) - return success with message
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return fmt.Sprintf("No channel found with ID '%s'. The channel may have been deleted or you may not have access to it.", args.ChannelID), nil
			}
			// Real error (network, auth, etc.)
			return "failed to fetch channel", fmt.Errorf("error fetching channel by ID: %w", err)
		}
		channels = []*model.Channel{channel}
	case args.ChannelName != "":
		// Unified lookup: try display name match, then URL name match, then substring
		channels, lastError = p.tryFindChannelByDisplayName(ctx, client, args.ChannelName, args.TeamID)
		if lastError != nil {
			return "failed to search for channels", lastError
		}

		// If no display name match, try URL name
		if len(channels) == 0 {
			channels, err = p.tryFindChannelByName(ctx, client, args.ChannelName, args.TeamID)
			if err != nil {
				return "failed to search for channels", err
			}
		}

		// If still nothing and we have a team scope, try substring match on display name
		if len(channels) == 0 && args.TeamID != "" {
			channels, err = p.tryFindChannelBySubstring(ctx, client, args.ChannelName, args.TeamID)
			if err != nil {
				return "failed to search for channels", err
			}
		}

		if len(channels) == 0 {
			var notFoundMsg strings.Builder
			notFoundMsg.WriteString(fmt.Sprintf("No channels found matching '%s'.", args.ChannelName))

			if args.TeamID != "" {
				team, _, teamErr := client.GetTeam(ctx, args.TeamID, "")
				if teamErr == nil {
					notFoundMsg.WriteString(fmt.Sprintf(" (searched within team '%s', ID: %s)", team.DisplayName, args.TeamID))
				} else {
					notFoundMsg.WriteString(fmt.Sprintf(" (searched within team ID: %s)", args.TeamID))
				}
			} else {
				notFoundMsg.WriteString(" (searched across all teams)")
			}

			notFoundMsg.WriteString("\n\nACTION REQUIRED - Try these alternatives before asking the user:\n")
			stepNum := 1
			if args.TeamID == "" {
				notFoundMsg.WriteString(fmt.Sprintf("%d. If you know the team, call get_channel_info with team_id parameter to narrow the search\n", stepNum))
				stepNum++
			}
			notFoundMsg.WriteString(fmt.Sprintf("%d. Call get_user_channels to list all channels you have access to\n", stepNum))
			notFoundMsg.WriteString("\nOnly ask the user for help after trying all alternatives above.")

			return notFoundMsg.String(), nil
		}
	default:
		return "either channel_id or channel_name must be provided", fmt.Errorf("insufficient parameters for channel lookup")
	}

	// If multiple channels found, return all with disambiguation guidance
	if len(channels) > 1 {
		return p.formatMultipleChannels(ctx, client, channels)
	}

	// Single channel found - format as before (backward compatible)
	channel := channels[0]

	// Get team info
	var teamName string
	team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
	if teamErr == nil {
		teamName = team.DisplayName
	}

	// Get member count
	var memberCount int64 = -1
	stats, _, err := client.GetChannelStats(ctx, channel.Id, "", false)
	if err == nil {
		memberCount = stats.MemberCount
	}

	var result strings.Builder
	format.WriteChannel(&result, format.ChannelEntry{
		HeaderLabel: "Channel Information:",
		Channel:     channel,
		TeamName:    teamName,
		TeamID:      channel.TeamId,
		MemberCount: memberCount,
	})

	return result.String(), nil
}

// formatMultipleChannels formats multiple channel results with team context for disambiguation.
// It uses a local team cache to avoid redundant GetTeam calls within the same result set.
func (p *MattermostToolProvider) formatMultipleChannels(ctx context.Context, client *model.Client4, channels []*model.Channel) (string, error) {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d channels with matching name:\n\n", len(channels)))

	// Cache teams to avoid duplicate fetches
	teamCache := make(map[string]*model.Team)

	for i, channel := range channels {
		// Get team info from cache or fetch
		var teamName string
		team, exists := teamCache[channel.TeamId]
		if !exists {
			fetchedTeam, _, err := client.GetTeam(ctx, channel.TeamId, "")
			if err == nil {
				team = fetchedTeam
				teamCache[channel.TeamId] = team
			}
		}
		if team != nil {
			teamName = team.DisplayName
		}

		// Get member count
		var memberCount int64 = -1
		stats, _, err := client.GetChannelStats(ctx, channel.Id, "", false)
		if err == nil {
			memberCount = stats.MemberCount
		}

		format.WriteChannel(&result, format.ChannelEntry{
			HeaderLabel: fmt.Sprintf("%d. %s", i+1, channel.DisplayName),
			Channel:     channel,
			TeamName:    teamName,
			TeamID:      channel.TeamId,
			MemberCount: memberCount,
		})
	}

	result.WriteString("Multiple channels found. To disambiguate, either:\n")
	result.WriteString("- Specify which team's channel you need\n")
	result.WriteString("- Call get_channel_info again with the team_id parameter\n")
	result.WriteString("- Use the specific channel_id from above in create_post\n")

	return result.String(), nil
}

// toolGetChannelMembers implements the get_channel_members tool.
// Returns paginated member details for a channel, including username, email, and roles.
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

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Default exclude_bots to true
	excludeBots := args.ExcludeBots == nil || *args.ExcludeBots

	// Get channel members
	members, _, err := client.GetChannelMembers(ctx, args.ChannelID, args.Page, args.Limit, "")
	if err != nil {
		return "failed to fetch channel members", fmt.Errorf("error fetching channel members: %w", err)
	}

	if len(members) == 0 {
		return "no members found in this channel", nil
	}

	// Get user details for each member, optionally filtering bots
	var result strings.Builder
	botsExcluded := 0
	var written int

	for _, member := range members {
		user, _, err := client.GetUser(ctx, member.UserId, "")
		if err != nil {
			p.logger.Warn("failed to get user details for member", "user_id", member.UserId, "error", err)
			format.WriteUser(&result, format.UserEntry{User: &model.User{Id: member.UserId, Username: "details unavailable"}})
			written++
			continue
		}

		if excludeBots && user.IsBot {
			botsExcluded++
			continue
		}

		format.WriteUser(&result, format.UserEntry{
			User: user,
			Role: format.MemberRole(member.SchemeAdmin, member.SchemeGuest, member.SchemeUser),
		})
		written++
	}

	// Build header and footer
	var header strings.Builder
	header.WriteString(fmt.Sprintf("Channel Members (page %d, showing %d members):\n", args.Page, written))

	var footer string
	if botsExcluded > 0 {
		footer = fmt.Sprintf("\n(%d bot account(s) excluded — set exclude_bots=false to include them)\n", botsExcluded)
	}

	return header.String() + result.String() + footer, nil
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

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Add user to channel
	_, _, err = client.AddChannelMember(ctx, args.ChannelID, args.UserID)
	if err != nil {
		return "failed to add user to channel", fmt.Errorf("error adding user to channel: %w", err)
	}

	// Get user and channel info for confirmation
	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	channel, _, channelErr := client.GetChannel(ctx, args.ChannelID)

	if userErr != nil || channelErr != nil {
		return fmt.Sprintf("Successfully added user %s to channel %s", args.UserID, args.ChannelID), nil
	}

	return fmt.Sprintf("Successfully added user '%s' to channel '%s'", user.Username, channel.DisplayName), nil
}

// tryFindChannelByDisplayName attempts to find channels by display name
// Returns all exact matches when teamID is not provided, or single match when teamID is specified
func (p *MattermostToolProvider) tryFindChannelByDisplayName(ctx context.Context, client *model.Client4, displayName, teamID string) ([]*model.Channel, error) {
	if teamID != "" {
		// Search within specific team - should only return one result
		user, _, userErr := client.GetMe(ctx, "")
		if userErr != nil {
			return nil, fmt.Errorf("error getting current user: %w", userErr)
		}

		channels, _, channelErr := client.GetChannelsForTeamForUser(ctx, teamID, user.Id, false, "")
		if channelErr != nil {
			return nil, fmt.Errorf("error fetching team channels: %w", channelErr)
		}

		for _, ch := range channels {
			if strings.EqualFold(ch.DisplayName, displayName) {
				return []*model.Channel{ch}, nil
			}
		}

		// Not found in team - return empty slice with nil error (not a technical failure)
		return []*model.Channel{}, nil
	}

	// Search across all teams
	channels, _, searchErr := client.SearchAllChannelsForUser(ctx, displayName)
	if searchErr != nil {
		return nil, fmt.Errorf("error searching channels: %w", searchErr)
	}

	// Find ALL matches by display name (case-insensitive)
	var matches []*model.Channel
	for _, ch := range channels {
		if strings.EqualFold(ch.DisplayName, displayName) {
			// Convert ChannelWithTeamData to Channel
			matches = append(matches, &model.Channel{
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
			})
		}
	}

	// Return empty slice if no matches found (not a technical failure)
	return matches, nil
}

// tryFindChannelByName attempts to find channels by name
// Returns all exact matches when teamID is not provided, or single match when teamID is specified
func (p *MattermostToolProvider) tryFindChannelByName(ctx context.Context, client *model.Client4, name, teamID string) ([]*model.Channel, error) {
	if teamID != "" {
		// Search within specific team - should only return one result
		channel, resp, err := client.GetChannelByName(ctx, name, teamID, "")
		if err != nil {
			// Check if it's a 404 (not found) - this is not a technical error
			if resp != nil && resp.StatusCode == 404 {
				return []*model.Channel{}, nil
			}
			// Real error (network, auth, etc.)
			return nil, fmt.Errorf("error fetching channel by name in team: %w", err)
		}
		return []*model.Channel{channel}, nil
	}

	// Search across all teams
	channels, _, searchErr := client.SearchAllChannelsForUser(ctx, name)
	if searchErr != nil {
		return nil, fmt.Errorf("error searching channels: %w", searchErr)
	}

	// Find ALL exact matches by name
	var matches []*model.Channel
	for _, ch := range channels {
		if ch.Name == name {
			// Convert ChannelWithTeamData to Channel
			matches = append(matches, &model.Channel{
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
			})
		}
	}

	// Return empty slice if no matches found (not a technical failure)
	return matches, nil
}

// tryFindChannelBySubstring does a case-insensitive substring match on display names
// within a specific team. Used as a fallback when exact matches fail.
func (p *MattermostToolProvider) tryFindChannelBySubstring(ctx context.Context, client *model.Client4, term, teamID string) ([]*model.Channel, error) {
	user, _, userErr := client.GetMe(ctx, "")
	if userErr != nil {
		return nil, fmt.Errorf("error getting current user: %w", userErr)
	}

	channels, _, channelErr := client.GetChannelsForTeamForUser(ctx, teamID, user.Id, false, "")
	if channelErr != nil {
		return nil, fmt.Errorf("error fetching team channels: %w", channelErr)
	}

	termLower := strings.ToLower(term)
	var matches []*model.Channel
	for _, ch := range channels {
		if strings.Contains(strings.ToLower(ch.DisplayName), termLower) {
			matches = append(matches, ch)
		}
	}

	return matches, nil
}

// toolGetUserChannels implements the get_user_channels tool.
// It returns all channels the current user is a member of, including DMs, GMs, and team channels.
// Team information is resolved in a single batch call via GetTeamsForUser to avoid N+1 queries.
// The response is paginated and returned as plain text with team metadata for each channel.
func (p *MattermostToolProvider) toolGetUserChannels(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetUserChannelsArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_user_channels: %w", err)
	}

	// Validate team ID if provided
	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}

	// Set defaults and cap to match schema (consistent with get_channel_members and get_team_members).
	// Guard against negative values to prevent slice panics from user input.
	if args.PerPage <= 0 {
		args.PerPage = 60
	}
	if args.PerPage > 200 {
		args.PerPage = 200
	}
	if args.Page < 0 {
		args.Page = 0
	}

	maxInt := int(^uint(0) >> 1)
	if args.Page > maxInt/args.PerPage {
		return "page value too large", fmt.Errorf("page * per_page overflows int")
	}

	// Get client and context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Get current user
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		return "failed to get current user", fmt.Errorf("failed to get current user: %w", err)
	}
	// Fetch all channels for the user (including DMs, GMs, and team channels).
	// NOTE: GetChannelsForUserWithLastDeleteAt does not support server-side pagination,
	// so we fetch all channels and paginate in memory. This is a Mattermost API limitation.
	// Pass 0 for lastDeleteAt to get all channels without filtering.
	allChannels, _, err := client.GetChannelsForUserWithLastDeleteAt(ctx, user.Id, 0)
	if err != nil {
		return "failed to get channels for user", fmt.Errorf("failed to get channels for user: %w", err)
	}

	// Filter by team if specified
	var channels []*model.Channel
	if args.TeamID != "" {
		for _, channel := range allChannels {
			if channel.TeamId == args.TeamID {
				channels = append(channels, channel)
			}
		}
	} else {
		channels = allChannels
	}

	// Store total count before pagination
	totalCount := len(channels)

	// Apply pagination
	start := args.Page * args.PerPage
	end := start + args.PerPage
	if start >= len(channels) {
		return fmt.Sprintf("No channels found (page %d, %d total channels).", args.Page, totalCount), nil
	}
	if end > len(channels) {
		end = len(channels)
	}
	hasMore := end < totalCount
	channels = channels[start:end]

	// Build a map of team IDs to team info for display.
	type TeamInfo struct {
		ID          string
		Name        string
		DisplayName string
	}
	teamInfoMap := make(map[string]*TeamInfo)
	userTeams, _, teamsErr := client.GetTeamsForUser(ctx, user.Id, "")
	if teamsErr != nil {
		p.logger.Warn("failed to fetch user teams for team info lookup, team details will be omitted", "error", teamsErr)
	} else {
		for _, team := range userTeams {
			teamInfoMap[team.Id] = &TeamInfo{
				ID:          team.Id,
				Name:        team.Name,
				DisplayName: team.DisplayName,
			}
		}
	}

	// Build human-readable response (consistent with get_channel_members, read_channel, etc.)
	var result strings.Builder
	result.WriteString(fmt.Sprintf("User Channels (page %d, showing %d of %d channels):\n\n", args.Page, len(channels), totalCount))

	for i, channel := range channels {
		displayName := channel.DisplayName
		if displayName == "" {
			switch channel.Type {
			case model.ChannelTypeDirect:
				displayName = "Direct Message"
			case model.ChannelTypeGroup:
				displayName = "Group Message"
			default:
				displayName = channel.Name
			}
		}

		var teamName string
		var teamID string
		if channel.TeamId != "" {
			if teamInfo, ok := teamInfoMap[channel.TeamId]; ok && teamInfo.DisplayName != "" {
				teamName = teamInfo.DisplayName
			}
			teamID = channel.TeamId
		}

		format.WriteChannel(&result, format.ChannelEntry{
			HeaderLabel: fmt.Sprintf("%d. **%s**", i+1+start, displayName),
			Channel:     channel,
			TeamName:    teamName,
			TeamID:      teamID,
			MemberCount: -1,
		})
	}

	if hasMore {
		result.WriteString(fmt.Sprintf("Page %d of results shown. More channels available — use page=%d to see the next page.\n", args.Page, args.Page+1))
	}

	return result.String(), nil
}
