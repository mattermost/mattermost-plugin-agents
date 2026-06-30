// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetChannelMemberArgs represents arguments for the get_channel_member tool.
type GetChannelMemberArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	UserID    string `json:"user_id,omitempty" jsonschema:"The user; omit for your own membership,maxLength=26"`
}

// GetChannelMembersByIdsArgs represents arguments for the get_channel_members_by_ids tool.
type GetChannelMembersByIdsArgs struct {
	ChannelID string   `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	UserIDs   []string `json:"user_ids" jsonschema:"The user IDs whose membership records to fetch"`
}

// GetChannelMembersByStatusArgs represents arguments for the get_channel_members_by_status tool.
type GetChannelMembersByStatusArgs struct {
	ChannelID   string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage     int    `json:"per_page,omitempty" jsonschema:"Members per page (default: 50, max: 200),minimum=1,maximum=200"`
	ExcludeBots *bool  `json:"exclude_bots,omitempty" jsonschema:"Exclude bot accounts (default: true)"`
}

// GetUserChannelMembershipsArgs represents arguments for the get_user_channel_memberships tool.
type GetUserChannelMembershipsArgs struct {
	UserID string `json:"user_id,omitempty" jsonschema:"The user; omit for yourself,maxLength=26"`
	TeamID string `json:"team_id" jsonschema:"The team to scope channel memberships to,minLength=26,maxLength=26"`
}

// GetUsersNotInChannelArgs represents arguments for the get_users_not_in_channel tool.
type GetUsersNotInChannelArgs struct {
	TeamID    string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Users per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// SearchUsersInChannelArgs represents arguments for the search_users_in_channel tool.
type SearchUsersInChannelArgs struct {
	Term      string `json:"term" jsonschema:"Search term,minLength=1,maxLength=64"`
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	NotIn     bool   `json:"not_in,omitempty" jsonschema:"If true, search users NOT in the channel instead of in it"`
	TeamID    string `json:"team_id,omitempty" jsonschema:"Team ID (required when not_in is true to scope candidates),maxLength=26"`
}

// ListSidebarCategoriesArgs represents arguments for the list_sidebar_categories tool.
type ListSidebarCategoriesArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team whose sidebar categories to list,minLength=26,maxLength=26"`
}

// AddChannelMembersArgs represents arguments for the add_channel_members tool.
type AddChannelMembersArgs struct {
	ChannelID string   `json:"channel_id" jsonschema:"The channel to add users to,minLength=26,maxLength=26"`
	UserIDs   []string `json:"user_ids" jsonschema:"The user IDs to add"`
}

// RemoveChannelMemberArgs represents arguments for the remove_channel_member tool.
type RemoveChannelMemberArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	UserID    string `json:"user_id" jsonschema:"The user to remove,minLength=26,maxLength=26"`
}

// SetChannelMuteArgs represents arguments for the set_channel_mute tool.
type SetChannelMuteArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Muted     bool   `json:"muted" jsonschema:"true to mute the channel for yourself, false to unmute"`
}

// SetChannelFavoriteArgs represents arguments for the set_channel_favorite tool.
type SetChannelFavoriteArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Favorite  bool   `json:"favorite" jsonschema:"true to favorite the channel, false to unfavorite"`
}

// UpdateChannelNotifyPropsArgs represents arguments for the update_channel_notify_props tool.
type UpdateChannelNotifyPropsArgs struct {
	ChannelID  string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Desktop    string `json:"desktop,omitempty" jsonschema:"Desktop notification level,enum=default,enum=all,enum=mention,enum=none"`
	Push       string `json:"push,omitempty" jsonschema:"Mobile push notification level,enum=default,enum=all,enum=mention,enum=none"`
	MarkUnread string `json:"mark_unread,omitempty" jsonschema:"Unread level,enum=all,enum=mention"`
}

const (
	getChannelMemberDescription          = "Get your (or a given user's) membership in a channel: roles, mute, last-viewed. Parameters: channel_id (required), user_id (optional, defaults to you)."
	getChannelMembersByIdsDescription    = "Get membership records for a specific set of users in a channel. Parameters: channel_id (required), user_ids (required list)."
	getChannelMembersByStatusDescription = "Get a channel's members ordered by presence status (online first). Parameters: channel_id (required), page, per_page, exclude_bots."
	getUserChannelMembershipsDescription = "Get a user's channel membership records within a team (roles per channel). Parameters: user_id (optional, defaults to you), team_id (required)."
	getUsersNotInChannelDescription      = "Get team members who are not in a channel (candidates to add). Parameters: team_id (required), channel_id (required), page, per_page."
	searchUsersInChannelDescription      = "Search for users within (or excluding) a channel. Parameters: term (required), channel_id (required), not_in (optional), team_id (required when not_in)."
	listSidebarCategoriesDescription     = "List your sidebar categories and their channels for a team. Parameters: team_id (required)."
	addChannelMembersDescription         = "Add several users to a channel (bulk; pairs with add_channel_member). Parameters: channel_id (required), user_ids (required list)."
	removeChannelMemberDescription       = "Remove a user from a channel. Parameters: channel_id (required), user_id (required)."
	setChannelMuteDescription            = "Mute or unmute a channel for yourself. Parameters: channel_id (required), muted (true/false)."
	setChannelFavoriteDescription        = "Add or remove a channel from your favorites. Parameters: channel_id (required), favorite (true/false)."
	updateChannelNotifyPropsDescription  = "Set your per-channel notifications (desktop/push level, and unread/mute level). Parameters: channel_id (required), desktop, push, mark_unread."
)

// getChannelMemberTools returns the channel membership and per-channel settings tools.
func (p *MattermostToolProvider) getChannelMemberTools() []MCPTool {
	return []MCPTool{
		{Name: "get_channel_member", Description: getChannelMemberDescription, Schema: NewJSONSchemaForAccessMode[GetChannelMemberArgs](string(p.accessMode)), Resolver: typed("get_channel_member", p.toolGetChannelMember)},
		{Name: "get_channel_members_by_ids", Description: getChannelMembersByIdsDescription, Schema: NewJSONSchemaForAccessMode[GetChannelMembersByIdsArgs](string(p.accessMode)), Resolver: typed("get_channel_members_by_ids", p.toolGetChannelMembersByIds)},
		{Name: "get_channel_members_by_status", Description: getChannelMembersByStatusDescription, Schema: NewJSONSchemaForAccessMode[GetChannelMembersByStatusArgs](string(p.accessMode)), Resolver: typed("get_channel_members_by_status", p.toolGetChannelMembersByStatus)},
		{Name: "get_user_channel_memberships", Description: getUserChannelMembershipsDescription, Schema: NewJSONSchemaForAccessMode[GetUserChannelMembershipsArgs](string(p.accessMode)), Resolver: typed("get_user_channel_memberships", p.toolGetUserChannelMemberships)},
		{Name: "get_users_not_in_channel", Description: getUsersNotInChannelDescription, Schema: NewJSONSchemaForAccessMode[GetUsersNotInChannelArgs](string(p.accessMode)), Resolver: typed("get_users_not_in_channel", p.toolGetUsersNotInChannel)},
		{Name: "search_users_in_channel", Description: searchUsersInChannelDescription, Schema: NewJSONSchemaForAccessMode[SearchUsersInChannelArgs](string(p.accessMode)), Resolver: typed("search_users_in_channel", p.toolSearchUsersInChannel)},
		{Name: "list_sidebar_categories", Description: listSidebarCategoriesDescription, Schema: NewJSONSchemaForAccessMode[ListSidebarCategoriesArgs](string(p.accessMode)), Resolver: typed("list_sidebar_categories", p.toolListSidebarCategories)},
		{Name: "add_channel_members", Description: addChannelMembersDescription, Schema: NewJSONSchemaForAccessMode[AddChannelMembersArgs](string(p.accessMode)), Resolver: typed("add_channel_members", p.toolAddChannelMembers)},
		{Name: "remove_channel_member", Description: removeChannelMemberDescription, Schema: NewJSONSchemaForAccessMode[RemoveChannelMemberArgs](string(p.accessMode)), Resolver: typed("remove_channel_member", p.toolRemoveChannelMember)},
		{Name: "set_channel_mute", Description: setChannelMuteDescription, Schema: NewJSONSchemaForAccessMode[SetChannelMuteArgs](string(p.accessMode)), Resolver: typed("set_channel_mute", p.toolSetChannelMute)},
		{Name: "set_channel_favorite", Description: setChannelFavoriteDescription, Schema: NewJSONSchemaForAccessMode[SetChannelFavoriteArgs](string(p.accessMode)), Resolver: typed("set_channel_favorite", p.toolSetChannelFavorite)},
		{Name: "update_channel_notify_props", Description: updateChannelNotifyPropsDescription, Schema: NewJSONSchemaForAccessMode[UpdateChannelNotifyPropsArgs](string(p.accessMode)), Resolver: typed("update_channel_notify_props", p.toolUpdateChannelNotifyProps)},
	}
}

// toolGetChannelMember implements the get_channel_member tool.
func (p *MattermostToolProvider) toolGetChannelMember(mcpContext *MCPToolContext, args GetChannelMemberArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := optionalID("user_id", args.UserID); err != nil {
		return "", err
	}

	userID := args.UserID
	if userID == "" {
		resolved, err := p.resolveUserID(mcpContext)
		if err != nil {
			return "", err
		}
		userID = resolved
	}

	member, _, err := mcpContext.Client.GetChannelMember(mcpContext.Ctx, args.ChannelID, userID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching channel member: %w", err)
	}

	var result strings.Builder
	format.WriteChannelMember(&result, format.ChannelMemberEntry{
		Member:   member,
		Username: p.usernameFor(mcpContext.Ctx, mcpContext.Client, member.UserId),
	})
	return result.String(), nil
}

// toolGetChannelMembersByIds implements the get_channel_members_by_ids tool.
func (p *MattermostToolProvider) toolGetChannelMembersByIds(mcpContext *MCPToolContext, args GetChannelMembersByIdsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if len(args.UserIDs) == 0 {
		return "", fmt.Errorf("user_ids cannot be empty")
	}
	for _, id := range args.UserIDs {
		if err := requireID("user_ids", id); err != nil {
			return "", err
		}
	}

	members, _, err := mcpContext.Client.GetChannelMembersByIds(mcpContext.Ctx, args.ChannelID, args.UserIDs)
	if err != nil {
		return "", fmt.Errorf("error fetching channel members: %w", err)
	}
	if len(members) == 0 {
		return "no matching channel members found", nil
	}

	rendered := make([]renderMember, len(members))
	for i, member := range members {
		rendered[i] = renderMember{
			userID: member.UserId,
			role:   format.MemberRole(member.SchemeAdmin, member.SchemeGuest, member.SchemeUser),
		}
	}
	return p.renderMembers(mcpContext.Ctx, mcpContext.Client, "Channel Members", 0, rendered, false), nil
}

// toolGetChannelMembersByStatus implements the get_channel_members_by_status tool.
func (p *MattermostToolProvider) toolGetChannelMembersByStatus(mcpContext *MCPToolContext, args GetChannelMembersByStatusArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	excludeBots := args.ExcludeBots == nil || *args.ExcludeBots

	users, _, err := mcpContext.Client.GetUsersInChannelByStatus(mcpContext.Ctx, args.ChannelID, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching channel members by status: %w", err)
	}

	return p.formatUserList(users, fmt.Sprintf("channel members (page %d, ordered by status)", page), excludeBots), nil
}

// toolGetUserChannelMemberships implements the get_user_channel_memberships tool.
func (p *MattermostToolProvider) toolGetUserChannelMemberships(mcpContext *MCPToolContext, args GetUserChannelMembershipsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := optionalID("user_id", args.UserID); err != nil {
		return "", err
	}

	userID := args.UserID
	if userID == "" {
		resolved, err := p.resolveUserID(mcpContext)
		if err != nil {
			return "", err
		}
		userID = resolved
	}

	members, _, err := mcpContext.Client.GetChannelMembersForUser(mcpContext.Ctx, userID, args.TeamID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching channel memberships: %w", err)
	}
	if len(members) == 0 {
		return "no channel memberships found", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Channel memberships (%d):\n\n", len(members)))
	for i := range members {
		format.WriteChannelMember(&result, format.ChannelMemberEntry{
			HeaderLabel: fmt.Sprintf("Membership %d", i+1),
			Member:      &members[i],
		})
	}
	return result.String(), nil
}

// toolGetUsersNotInChannel implements the get_users_not_in_channel tool.
func (p *MattermostToolProvider) toolGetUsersNotInChannel(mcpContext *MCPToolContext, args GetUsersNotInChannelArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)

	users, _, err := mcpContext.Client.GetUsersNotInChannel(mcpContext.Ctx, args.TeamID, args.ChannelID, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching users not in channel: %w", err)
	}

	return p.formatUserList(users, fmt.Sprintf("users not in channel (page %d)", page), false), nil
}

// toolSearchUsersInChannel implements the search_users_in_channel tool.
func (p *MattermostToolProvider) toolSearchUsersInChannel(mcpContext *MCPToolContext, args SearchUsersInChannelArgs) (string, error) {
	if args.Term == "" {
		return "", fmt.Errorf("term cannot be empty")
	}
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := optionalID("team_id", args.TeamID); err != nil {
		return "", err
	}

	search := &model.UserSearch{Term: args.Term, Limit: 100}
	if args.NotIn {
		search.NotInChannelId = args.ChannelID
		search.TeamId = args.TeamID
	} else {
		search.InChannelId = args.ChannelID
	}

	users, _, err := mcpContext.Client.SearchUsers(mcpContext.Ctx, search)
	if err != nil {
		return "", fmt.Errorf("error searching users in channel: %w", err)
	}

	return p.formatUserList(users, fmt.Sprintf("users matching '%s'", args.Term), false), nil
}

// toolListSidebarCategories implements the list_sidebar_categories tool.
func (p *MattermostToolProvider) toolListSidebarCategories(mcpContext *MCPToolContext, args ListSidebarCategoriesArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	categories, _, err := mcpContext.Client.GetSidebarCategoriesForTeamForUser(mcpContext.Ctx, userID, args.TeamID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching sidebar categories: %w", err)
	}
	if categories == nil || len(categories.Categories) == 0 {
		return "no sidebar categories found", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Sidebar categories (%d):\n\n", len(categories.Categories)))
	for i := range categories.Categories {
		format.WriteSidebarCategory(&result, format.SidebarCategoryEntry{
			HeaderLabel: fmt.Sprintf("Category %d", i+1),
			Category:    categories.Categories[i],
		})
	}
	return result.String(), nil
}

// toolAddChannelMembers implements the add_channel_members tool.
func (p *MattermostToolProvider) toolAddChannelMembers(mcpContext *MCPToolContext, args AddChannelMembersArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if len(args.UserIDs) == 0 {
		return "", fmt.Errorf("user_ids cannot be empty")
	}
	for _, id := range args.UserIDs {
		if err := requireID("user_ids", id); err != nil {
			return "", err
		}
	}

	if _, _, err := mcpContext.Client.AddChannelMembers(mcpContext.Ctx, args.ChannelID, "", args.UserIDs); err != nil {
		return "", fmt.Errorf("error adding channel members: %w", err)
	}

	return fmt.Sprintf("Successfully added %d user(s) to channel %s", len(args.UserIDs), args.ChannelID), nil
}

// toolRemoveChannelMember implements the remove_channel_member tool.
func (p *MattermostToolProvider) toolRemoveChannelMember(mcpContext *MCPToolContext, args RemoveChannelMemberArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.RemoveUserFromChannel(mcpContext.Ctx, args.ChannelID, args.UserID); err != nil {
		return "", fmt.Errorf("error removing channel member: %w", err)
	}

	return fmt.Sprintf("Successfully removed user %s from channel %s", args.UserID, args.ChannelID), nil
}

// toolSetChannelMute implements the set_channel_mute tool. There is no mute endpoint;
// muting toggles the member's mark_unread notify prop.
func (p *MattermostToolProvider) toolSetChannelMute(mcpContext *MCPToolContext, args SetChannelMuteArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	markUnread := model.ChannelMarkUnreadAll
	if args.Muted {
		markUnread = model.ChannelMarkUnreadMention
	}
	props := map[string]string{"mark_unread": markUnread}
	if _, err := mcpContext.Client.UpdateChannelNotifyProps(mcpContext.Ctx, args.ChannelID, userID, props); err != nil {
		return "", fmt.Errorf("error updating channel mute state: %w", err)
	}

	state := "unmuted"
	if args.Muted {
		state = "muted"
	}
	return fmt.Sprintf("Successfully %s channel %s", state, args.ChannelID), nil
}

// toolSetChannelFavorite implements the set_channel_favorite tool via the
// favorite_channel preference (no dedicated endpoint).
func (p *MattermostToolProvider) toolSetChannelFavorite(mcpContext *MCPToolContext, args SetChannelFavoriteArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	if args.Favorite {
		preferences := model.Preferences{{
			UserId:   userID,
			Category: model.PreferenceCategoryFavoriteChannel,
			Name:     args.ChannelID,
			Value:    "true",
		}}
		if _, err := client.UpdatePreferences(ctx, userID, preferences); err != nil {
			return "", fmt.Errorf("error favoriting channel: %w", err)
		}
		return fmt.Sprintf("Successfully favorited channel %s", args.ChannelID), nil
	}

	preferences := model.Preferences{{
		UserId:   userID,
		Category: model.PreferenceCategoryFavoriteChannel,
		Name:     args.ChannelID,
		Value:    "false",
	}}
	if _, err := client.DeletePreferences(ctx, userID, preferences); err != nil {
		return "", fmt.Errorf("error unfavoriting channel: %w", err)
	}
	return fmt.Sprintf("Successfully unfavorited channel %s", args.ChannelID), nil
}

// toolUpdateChannelNotifyProps implements the update_channel_notify_props tool.
func (p *MattermostToolProvider) toolUpdateChannelNotifyProps(mcpContext *MCPToolContext, args UpdateChannelNotifyPropsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	props := map[string]string{}
	if args.Desktop != "" {
		props["desktop"] = args.Desktop
	}
	if args.Push != "" {
		props["push"] = args.Push
	}
	if args.MarkUnread != "" {
		props["mark_unread"] = args.MarkUnread
	}
	if len(props) == 0 {
		return "", fmt.Errorf("provide at least one of desktop, push, mark_unread")
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, err := mcpContext.Client.UpdateChannelNotifyProps(mcpContext.Ctx, args.ChannelID, userID, props); err != nil {
		return "", fmt.Errorf("error updating channel notification settings: %w", err)
	}

	return fmt.Sprintf("Successfully updated notification settings for channel %s", args.ChannelID), nil
}

// formatUserList renders a slice of users, optionally excluding bots.
func (p *MattermostToolProvider) formatUserList(users []*model.User, noun string, excludeBots bool) string {
	if len(users) == 0 {
		return fmt.Sprintf("no %s found", noun)
	}

	var result strings.Builder
	botsExcluded := 0
	written := 0
	var body strings.Builder
	for _, user := range users {
		if excludeBots && user.IsBot {
			botsExcluded++
			continue
		}
		written++
		format.WriteUser(&body, format.UserEntry{HeaderLabel: fmt.Sprintf("User %d", written), User: user})
	}

	result.WriteString(fmt.Sprintf("Found %d %s:\n\n", written, noun))
	result.WriteString(body.String())
	if botsExcluded > 0 {
		result.WriteString(fmt.Sprintf("\n(%d bot account(s) excluded — set exclude_bots=false to include them)\n", botsExcluded))
	}
	return result.String()
}
