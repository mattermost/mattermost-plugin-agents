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
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	Limit       int    `json:"limit,omitempty" jsonschema:"Number of members to return (default: 50, max: 50),minimum=1,maximum=50"`
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

// provideChannelTools registers all channel-related MCP tools.
func (p *MattermostToolProvider) provideChannelTools(s *mcp.Server) {
	registerTool[ReadChannelArgs](s, p, "read_channel",
		"Read recent posts from a Mattermost channel. Parameters: channel_id (required), limit (1-100, default 20), since (ISO 8601 timestamp, optional). Returns post details including author, content, and timestamps. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"limit\": 10, \"since\": \"2024-01-01T00:00:00Z\"}",
		llm.NewJSONSchemaFromStruct[ReadChannelArgs](),
		p.toolReadChannel,
		format.ReadChannelOutput,
	)
	registerTool[CreateChannelArgs](s, p, "create_channel",
		"Create a new channel in Mattermost. Parameters: name (URL-friendly), display_name (user-visible), type ('O' for public, 'P' for private), team_id (required), purpose (optional), header (optional). Returns created channel details. Example: {\"name\": \"dev-chat\", \"display_name\": \"Development Chat\", \"type\": \"O\", \"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\"}",
		llm.NewJSONSchemaFromStruct[CreateChannelArgs](),
		p.toolCreateChannel,
		format.CreateChannelOutput,
	)
	registerTool[GetChannelInfoArgs](s, p, "get_channel_info",
		"Get information about channel(s). Provide channel_id (fastest) or channel_name (matches against both display name and URL name, case-insensitive, supports partial matches). Optional: team_id to limit search scope. If multiple channels match (e.g., 'General' exists in multiple teams), returns ALL matches with team context for disambiguation. Returns channel metadata including ID, names, type, team, purpose, member count, and the requesting user's role in the channel (admin, member, guest, or not_member). Example: {\"channel_name\": \"General\"} or {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\"}",
		llm.NewJSONSchemaFromStruct[GetChannelInfoArgs](),
		p.toolGetChannelInfo,
		format.ChannelInfoOutput,
	)
	registerTool[GetChannelMembersArgs](s, p, "get_channel_members",
		"Get members of a channel with pagination support. Parameters: channel_id (required), limit (1-50, default 50), page (0+, default 0). Returns user details for each member including username, email, display name, and join date. Example: {\"channel_id\": \"h5wqm8kxptbztfgzpaxbsqozah\", \"limit\": 25, \"page\": 0}",
		llm.NewJSONSchemaFromStruct[GetChannelMembersArgs](),
		p.toolGetChannelMembers,
		format.ChannelMembersOutput,
	)
	registerTool[AddUserToChannelArgs](s, p, "add_user_to_channel",
		"Add a user to a channel. Parameters: user_id (required), channel_id (required). Returns confirmation message.",
		llm.NewJSONSchemaFromStruct[AddUserToChannelArgs](),
		p.toolAddUserToChannel,
		format.AddUserToChannelOutput,
	)
	registerTool[GetUserChannelsArgs](s, p, "get_user_channels",
		"Get channels the current user is a member of, including DMs and GMs. Parameters: team_id (optional, filter by team), page (default 0), per_page (1-200, default 60). Returns channel details with team info and pagination. Example: {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\", \"per_page\": 60}",
		llm.NewJSONSchemaFromStruct[GetUserChannelsArgs](),
		p.toolGetUserChannels,
		format.UserChannelsOutput,
	)
}

// toolReadChannel implements the read_channel tool.
// It reads recent posts from a channel and formats them with author usernames.
// Uses GetUsersByIds to fetch all authors in a single API call.
// Makes a single GetTeam call for the channel's team context (acceptable for one channel).
func (p *MattermostToolProvider) toolReadChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.ReadChannelOutput, error) {
	var args ReadChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.ReadChannelOutput{}, fmt.Errorf("failed to get arguments for tool read_channel: %w", err)
	}

	// Validate channel ID
	if !model.IsValidId(args.ChannelID) {
		return mcptool.ReadChannelOutput{}, fmt.Errorf("channel_id must be a valid ID")
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
		return mcptool.ReadChannelOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Parse since timestamp if provided
	var since int64
	if args.Since != "" {
		parsedTime, parseErr := time.Parse(time.RFC3339, args.Since)
		if parseErr != nil {
			return mcptool.ReadChannelOutput{}, fmt.Errorf("invalid timestamp format: %w", parseErr)
		}
		since = parsedTime.Unix() * 1000 // Convert to milliseconds
	}

	// Get channel info for context
	channel, _, err := client.GetChannel(ctx, args.ChannelID)
	if err != nil {
		return mcptool.ReadChannelOutput{}, fmt.Errorf("error fetching channel: %w", err)
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
			return mcptool.ReadChannelOutput{}, fmt.Errorf("error fetching team: %w", teamErr)
		}
		teamDisplayName = team.DisplayName
	}

	// Get posts from the channel
	posts, _, err := client.GetPostsForChannel(ctx, args.ChannelID, 0, args.Limit, "", false, false)
	if err != nil {
		return mcptool.ReadChannelOutput{}, fmt.Errorf("error fetching posts: %w", err)
	}

	// Filter by since timestamp if provided
	var filteredPosts []*model.Post
	for _, post := range posts.ToSlice() {
		if since == 0 || post.CreateAt >= since {
			filteredPosts = append(filteredPosts, post)
		}
	}

	// Sort chronologically (oldest first) for natural reading order
	sort.Slice(filteredPosts, func(i, j int) bool {
		return filteredPosts[i].CreateAt < filteredPosts[j].CreateAt
	})

	userMap := make(map[string]*model.User)
	if len(filteredPosts) > 0 {
		// Collect unique user IDs and fetch all at once
		userIDs := make([]string, 0)
		seen := make(map[string]bool)
		for _, post := range filteredPosts {
			if !seen[post.UserId] {
				seen[post.UserId] = true
				userIDs = append(userIDs, post.UserId)
			}
		}

		users, _, usersErr := client.GetUsersByIds(ctx, userIDs)
		if usersErr != nil {
			p.logger.Warn("failed to fetch users by IDs", "error", usersErr)
		} else {
			for _, u := range users {
				userMap[u.Id] = u
			}
		}
	}

	out := mcptool.ReadChannelOutput{
		Channel:  channel,
		Posts:    filteredPosts,
		Users:    userMap,
		TeamName: teamDisplayName,
	}
	return out, nil
}

// toolCreateChannel implements the create_channel tool.
// Creates a new public or private channel in a specified team.
func (p *MattermostToolProvider) toolCreateChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.CreateChannelOutput, error) {
	var args CreateChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("failed to get arguments for tool create_channel: %w", err)
	}

	// Validate required fields
	if args.Name == "" {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("name cannot be empty")
	}
	if args.DisplayName == "" {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("display_name cannot be empty")
	}
	if args.Type == "" {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("type cannot be empty")
	}
	if !model.IsValidId(args.TeamID) {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("team_id must be a valid ID")
	}

	// Validate channel type
	if args.Type != "O" && args.Type != "P" {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("invalid channel type: %s", args.Type)
	}

	// Get client and context
	if mcpContext.Client == nil {
		return mcptool.CreateChannelOutput{}, fmt.Errorf("client not available in context")
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
		return mcptool.CreateChannelOutput{}, fmt.Errorf("error creating channel: %w", err)
	}

	return mcptool.CreateChannelOutput{Channel: createdChannel}, nil
}

// toolGetChannelInfo implements the get_channel_info tool.
func (p *MattermostToolProvider) toolGetChannelInfo(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.ChannelInfoOutput, error) {
	var args GetChannelInfoArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.ChannelInfoOutput{}, fmt.Errorf("failed to get arguments for tool get_channel_info: %w", err)
	}

	if mcpContext.Client == nil {
		return mcptool.ChannelInfoOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return mcptool.ChannelInfoOutput{}, fmt.Errorf("invalid team_id format")
	}

	var channels []*model.Channel

	switch {
	case args.ChannelID != "":
		if !model.IsValidId(args.ChannelID) {
			return mcptool.ChannelInfoOutput{}, fmt.Errorf("invalid channel_id format")
		}
		channel, resp, getErr := client.GetChannel(ctx, args.ChannelID)
		if getErr != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return mcptool.ChannelInfoOutput{}, fmt.Errorf("no channel found with ID '%s'. The channel may have been deleted or you may not have access to it", args.ChannelID)
			}
			return mcptool.ChannelInfoOutput{}, fmt.Errorf("error fetching channel by ID: %w", getErr)
		}
		channels = []*model.Channel{channel}

	case args.ChannelName != "":
		channels, err = p.tryFindChannelByDisplayName(ctx, client, args.ChannelName, args.TeamID)
		if err != nil {
			return mcptool.ChannelInfoOutput{}, err
		}
		if len(channels) == 0 {
			channels, err = p.tryFindChannelByName(ctx, client, args.ChannelName, args.TeamID)
			if err != nil {
				return mcptool.ChannelInfoOutput{}, err
			}
		}
		if len(channels) == 0 && args.TeamID != "" {
			channels, err = p.tryFindChannelBySubstring(ctx, client, args.ChannelName, args.TeamID)
			if err != nil {
				return mcptool.ChannelInfoOutput{}, err
			}
		}

		if len(channels) == 0 {
			return mcptool.ChannelInfoOutput{}, channelNotFoundByNameError(ctx, client, args.ChannelName, args.TeamID)
		}

	default:
		return mcptool.ChannelInfoOutput{}, fmt.Errorf("either channel_id or channel_name must be provided")
	}

	teamByID := make(map[string]*model.Team)
	memberCountByChannelID := make(map[string]int64)
	channelRoleByID := make(map[string]string)
	userID := mcpContext.UserID

	for _, channel := range channels {
		if channel.TeamId != "" {
			if _, exists := teamByID[channel.TeamId]; !exists {
				team, _, teamErr := client.GetTeam(ctx, channel.TeamId, "")
				if teamErr == nil {
					teamByID[channel.TeamId] = team
				}
			}
		}
		if stats, _, statsErr := client.GetChannelStats(ctx, channel.Id, "", false); statsErr == nil {
			memberCountByChannelID[channel.Id] = stats.MemberCount
		}

		if userID != "" {
			member, resp, memberErr := client.GetChannelMember(ctx, channel.Id, userID, "")
			switch {
			case memberErr == nil:
				switch {
				case member.SchemeAdmin:
					channelRoleByID[channel.Id] = "admin"
				case member.SchemeGuest:
					channelRoleByID[channel.Id] = "guest"
				default:
					channelRoleByID[channel.Id] = "member"
				}
			case resp != nil && resp.StatusCode == http.StatusNotFound:
				channelRoleByID[channel.Id] = "not_member"
			default:
				p.logger.Warn("failed to get channel member for role lookup", "channel_id", channel.Id, "error", memberErr)
			}
		}
	}

	return mcptool.ChannelInfoOutput{
		Channels:               channels,
		TeamByID:               teamByID,
		MemberCountByChannelID: memberCountByChannelID,
		ChannelRoleByID:        channelRoleByID,
	}, nil
}

// channelNotFoundByNameError builds the descriptive not-found error for a
// channel-name lookup, including the team scope (if any) and prescriptive
// next steps for the LLM to try before falling back to the user.
func channelNotFoundByNameError(ctx context.Context, client *model.Client4, channelName, teamID string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no channels found matching '%s'", channelName)
	switch {
	case teamID != "":
		teamName := ""
		if team, _, teamErr := client.GetTeam(ctx, teamID, ""); teamErr == nil {
			teamName = team.DisplayName
		}
		if teamName != "" {
			fmt.Fprintf(&b, " (searched within team '%s', ID: %s)", teamName, teamID)
		} else {
			fmt.Fprintf(&b, " (searched within team ID: %s)", teamID)
		}
	default:
		b.WriteString(" (searched across all teams)")
	}
	b.WriteString(". ACTION REQUIRED - try these alternatives before asking the user:")
	stepNum := 1
	if teamID == "" {
		fmt.Fprintf(&b, " %d. if you know the team, call get_channel_info with team_id parameter to narrow the search;", stepNum)
		stepNum++
	}
	fmt.Fprintf(&b, " %d. call get_user_channels to list all channels you have access to.", stepNum)
	return fmt.Errorf("%s", b.String())
}

// toolGetChannelMembers implements the get_channel_members tool.
// Returns paginated member details for a channel, including username, email, and roles.
func (p *MattermostToolProvider) toolGetChannelMembers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.ChannelMembersOutput, error) {
	var args GetChannelMembersArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.ChannelMembersOutput{}, fmt.Errorf("failed to get arguments for tool get_channel_members: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.ChannelID) {
		return mcptool.ChannelMembersOutput{}, fmt.Errorf("channel_id must be a valid ID")
	}

	// Set defaults and validate
	if args.Limit == 0 {
		args.Limit = 50
	}
	if args.Limit > 50 {
		args.Limit = 50
	}
	if args.Page < 0 {
		args.Page = 0
	}

	// Get client and context
	if mcpContext.Client == nil {
		return mcptool.ChannelMembersOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Default exclude_bots to true
	excludeBots := args.ExcludeBots == nil || *args.ExcludeBots

	channel, _, chErr := client.GetChannel(ctx, args.ChannelID)
	if chErr != nil {
		return mcptool.ChannelMembersOutput{}, fmt.Errorf("error fetching channel: %w", chErr)
	}

	members, _, err := client.GetChannelMembers(ctx, args.ChannelID, args.Page, args.Limit, "")
	if err != nil {
		return mcptool.ChannelMembersOutput{}, fmt.Errorf("error fetching channel members: %w", err)
	}

	if len(members) == 0 {
		return mcptool.ChannelMembersOutput{
			Channel:     channel,
			Rows:        nil,
			Page:        args.Page,
			ExcludeBots: excludeBots,
		}, nil
	}

	rows := make([]mcptool.ChannelMemberRow, 0, len(members))
	for _, member := range members {
		user, _, userErr := client.GetUser(ctx, member.UserId, "")
		if userErr != nil {
			p.logger.Warn("failed to get user details for member", "user_id", member.UserId, "error", userErr)
			rows = append(rows, mcptool.ChannelMemberRow{
				User:        &model.User{Id: member.UserId, Username: "details unavailable"},
				SchemeAdmin: member.SchemeAdmin,
				SchemeGuest: member.SchemeGuest,
				SchemeUser:  member.SchemeUser,
			})
			continue
		}
		rows = append(rows, mcptool.ChannelMemberRow{
			User:        user,
			SchemeAdmin: member.SchemeAdmin,
			SchemeGuest: member.SchemeGuest,
			SchemeUser:  member.SchemeUser,
		})
	}

	out := mcptool.ChannelMembersOutput{
		Channel:     channel,
		Rows:        rows,
		Page:        args.Page,
		ExcludeBots: excludeBots,
	}
	return out, nil
}

// toolAddUserToChannel implements the add_user_to_channel tool using the context client
func (p *MattermostToolProvider) toolAddUserToChannel(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.AddUserToChannelOutput, error) {
	var args AddUserToChannelArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.AddUserToChannelOutput{}, fmt.Errorf("failed to get arguments for tool add_user_to_channel: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.UserID) {
		return mcptool.AddUserToChannelOutput{}, fmt.Errorf("user_id must be a valid ID")
	}
	if !model.IsValidId(args.ChannelID) {
		return mcptool.AddUserToChannelOutput{}, fmt.Errorf("channel_id must be a valid ID")
	}

	// Get client and context
	if mcpContext.Client == nil {
		return mcptool.AddUserToChannelOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	_, _, err = client.AddChannelMember(ctx, args.ChannelID, args.UserID)
	if err != nil {
		return mcptool.AddUserToChannelOutput{}, fmt.Errorf("error adding user to channel: %w", err)
	}

	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	channel, _, channelErr := client.GetChannel(ctx, args.ChannelID)

	out := mcptool.AddUserToChannelOutput{
		UserID:    args.UserID,
		ChannelID: args.ChannelID,
	}
	if userErr == nil {
		out.User = user
	}
	if channelErr == nil {
		out.Channel = channel
	}
	return out, nil
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
func (p *MattermostToolProvider) toolGetUserChannels(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.UserChannelsOutput, error) {
	var args GetUserChannelsArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.UserChannelsOutput{}, fmt.Errorf("failed to get arguments for tool get_user_channels: %w", err)
	}

	// Validate team ID if provided
	if args.TeamID != "" && !model.IsValidId(args.TeamID) {
		return mcptool.UserChannelsOutput{}, fmt.Errorf("team_id must be a valid ID")
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
		return mcptool.UserChannelsOutput{}, fmt.Errorf("page * per_page overflows int")
	}

	// Get client and context
	if mcpContext.Client == nil {
		return mcptool.UserChannelsOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Get current user
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		return mcptool.UserChannelsOutput{}, fmt.Errorf("failed to get current user: %w", err)
	}
	// Fetch all channels for the user (including DMs, GMs, and team channels).
	// NOTE: GetChannelsForUserWithLastDeleteAt does not support server-side pagination,
	// so we fetch all channels and paginate in memory. This is a Mattermost API limitation.
	// Pass 0 for lastDeleteAt to get all channels without filtering.
	allChannels, _, err := client.GetChannelsForUserWithLastDeleteAt(ctx, user.Id, 0)
	if err != nil {
		return mcptool.UserChannelsOutput{}, fmt.Errorf("failed to get channels for user: %w", err)
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
		return mcptool.UserChannelsOutput{
			Channels: nil,
			PageInfo: mcptool.UserChannelsPageInfo{
				Page:       args.Page,
				PerPage:    args.PerPage,
				TotalCount: totalCount,
				HasMore:    false,
			},
			TeamInfoByID: nil,
		}, nil
	}
	if end > len(channels) {
		end = len(channels)
	}
	hasMore := end < totalCount
	channels = channels[start:end]

	teamByID := make(map[string]*model.Team)
	userTeams, _, teamsErr := client.GetTeamsForUser(ctx, user.Id, "")
	if teamsErr != nil {
		p.logger.Warn("failed to fetch user teams for team info lookup, team details will be omitted", "error", teamsErr)
	} else {
		for _, team := range userTeams {
			teamByID[team.Id] = team
		}
	}

	out := mcptool.UserChannelsOutput{
		Channels: channels,
		PageInfo: mcptool.UserChannelsPageInfo{
			Page:       args.Page,
			PerPage:    args.PerPage,
			TotalCount: totalCount,
			HasMore:    hasMore,
		},
		TeamInfoByID: teamByID,
	}
	return out, nil
}
