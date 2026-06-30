// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetGroupInfoArgs represents arguments for the get_group_info tool.
type GetGroupInfoArgs struct {
	GroupID string `json:"group_id" jsonschema:"The group ID,minLength=26,maxLength=26"`
}

// ListGroupsArgs represents arguments for the list_groups tool.
type ListGroupsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Optional search term to filter groups by name"`
}

// GetUserGroupsArgs represents arguments for the get_user_groups tool.
type GetUserGroupsArgs struct {
	UserID string `json:"user_id,omitempty" jsonschema:"The user; omit for yourself,maxLength=26"`
}

// GetChannelGroupsArgs represents arguments for the get_channel_groups tool.
type GetChannelGroupsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
}

// GetTeamGroupsArgs represents arguments for the get_team_groups tool.
type GetTeamGroupsArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
}

// GetUsersInGroupChannelsArgs represents arguments for the get_users_in_group_channels tool.
type GetUsersInGroupChannelsArgs struct {
	ChannelIDs []string `json:"channel_ids" jsonschema:"The group-message (GM) channel IDs"`
}

const (
	getGroupInfoDescription            = "Get a group's metadata. Parameters: group_id (required)."
	listGroupsDescription              = "List or search custom and LDAP-synced groups. Parameters: query (optional search term)."
	getUserGroupsDescription           = "Get the groups a user belongs to. Parameters: user_id (optional, defaults to you)."
	getChannelGroupsDescription        = "Get the groups synced to a channel. Parameters: channel_id (required)."
	getTeamGroupsDescription           = "Get the groups synced to a team. Parameters: team_id (required)."
	getUsersInGroupChannelsDescription = "Get the members of given group-message (GM) channels. Parameters: channel_ids (required list)."
)

// getGroupTools returns the group tools.
func (p *MattermostToolProvider) getGroupTools() []MCPTool {
	return []MCPTool{
		{Name: "get_group_info", Description: getGroupInfoDescription, Schema: NewJSONSchemaForAccessMode[GetGroupInfoArgs](string(p.accessMode)), Resolver: typed("get_group_info", p.toolGetGroupInfo)},
		{Name: "list_groups", Description: listGroupsDescription, Schema: NewJSONSchemaForAccessMode[ListGroupsArgs](string(p.accessMode)), Resolver: typed("list_groups", p.toolListGroups)},
		{Name: "get_user_groups", Description: getUserGroupsDescription, Schema: NewJSONSchemaForAccessMode[GetUserGroupsArgs](string(p.accessMode)), Resolver: typed("get_user_groups", p.toolGetUserGroups)},
		{Name: "get_channel_groups", Description: getChannelGroupsDescription, Schema: NewJSONSchemaForAccessMode[GetChannelGroupsArgs](string(p.accessMode)), Resolver: typed("get_channel_groups", p.toolGetChannelGroups)},
		{Name: "get_team_groups", Description: getTeamGroupsDescription, Schema: NewJSONSchemaForAccessMode[GetTeamGroupsArgs](string(p.accessMode)), Resolver: typed("get_team_groups", p.toolGetTeamGroups)},
		{Name: "get_users_in_group_channels", Description: getUsersInGroupChannelsDescription, Schema: NewJSONSchemaForAccessMode[GetUsersInGroupChannelsArgs](string(p.accessMode)), Resolver: typed("get_users_in_group_channels", p.toolGetUsersInGroupChannels)},
	}
}

// toolGetGroupInfo implements the get_group_info tool.
func (p *MattermostToolProvider) toolGetGroupInfo(mcpContext *MCPToolContext, args GetGroupInfoArgs) (string, error) {
	if err := requireID("group_id", args.GroupID); err != nil {
		return "", err
	}
	group, _, err := mcpContext.Client.GetGroup(mcpContext.Ctx, args.GroupID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching group: %w", err)
	}
	var result strings.Builder
	format.WriteGroup(&result, format.GroupEntry{Group: group})
	return result.String(), nil
}

// toolListGroups implements the list_groups tool.
func (p *MattermostToolProvider) toolListGroups(mcpContext *MCPToolContext, args ListGroupsArgs) (string, error) {
	opts := model.GroupSearchOpts{Q: args.Query}
	groups, _, err := mcpContext.Client.GetGroups(mcpContext.Ctx, opts)
	if err != nil {
		return "", fmt.Errorf("error fetching groups: %w", err)
	}
	return formatGroupList(groups), nil
}

// toolGetUserGroups implements the get_user_groups tool.
func (p *MattermostToolProvider) toolGetUserGroups(mcpContext *MCPToolContext, args GetUserGroupsArgs) (string, error) {
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
	groups, _, err := mcpContext.Client.GetGroupsByUserId(mcpContext.Ctx, userID)
	if err != nil {
		return "", fmt.Errorf("error fetching user groups: %w", err)
	}
	return formatGroupList(groups), nil
}

// toolGetChannelGroups implements the get_channel_groups tool.
func (p *MattermostToolProvider) toolGetChannelGroups(mcpContext *MCPToolContext, args GetChannelGroupsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	groups, _, _, err := mcpContext.Client.GetGroupsByChannel(mcpContext.Ctx, args.ChannelID, model.GroupSearchOpts{})
	if err != nil {
		return "", fmt.Errorf("error fetching channel groups: %w", err)
	}
	return formatGroupListWithSchemeAdmin(groups), nil
}

// toolGetTeamGroups implements the get_team_groups tool.
func (p *MattermostToolProvider) toolGetTeamGroups(mcpContext *MCPToolContext, args GetTeamGroupsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	groups, _, _, err := mcpContext.Client.GetGroupsByTeam(mcpContext.Ctx, args.TeamID, model.GroupSearchOpts{})
	if err != nil {
		return "", fmt.Errorf("error fetching team groups: %w", err)
	}
	return formatGroupListWithSchemeAdmin(groups), nil
}

// toolGetUsersInGroupChannels implements the get_users_in_group_channels tool.
func (p *MattermostToolProvider) toolGetUsersInGroupChannels(mcpContext *MCPToolContext, args GetUsersInGroupChannelsArgs) (string, error) {
	if len(args.ChannelIDs) == 0 {
		return "", fmt.Errorf("channel_ids cannot be empty")
	}
	for _, id := range args.ChannelIDs {
		if err := requireID("channel_ids", id); err != nil {
			return "", err
		}
	}

	byChannel, _, err := mcpContext.Client.GetUsersByGroupChannelIds(mcpContext.Ctx, args.ChannelIDs)
	if err != nil {
		return "", fmt.Errorf("error fetching users in group channels: %w", err)
	}
	if len(byChannel) == 0 {
		return "no members found in the given group-message channels", nil
	}

	var result strings.Builder
	for _, channelID := range args.ChannelIDs {
		users := byChannel[channelID]
		result.WriteString(fmt.Sprintf("Group message %s (%d members):\n", channelID, len(users)))
		for _, user := range users {
			format.WriteUser(&result, format.UserEntry{User: user})
		}
		result.WriteString("\n")
	}
	return result.String(), nil
}

// formatGroupList renders a slice of groups.
func formatGroupList(groups []*model.Group) string {
	if len(groups) == 0 {
		return "no groups found"
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d group(s):\n\n", len(groups)))
	for i, group := range groups {
		format.WriteGroup(&result, format.GroupEntry{HeaderLabel: fmt.Sprintf("Group %d", i+1), Group: group})
	}
	return result.String()
}

// formatGroupListWithSchemeAdmin renders groups returned with scheme-admin metadata.
func formatGroupListWithSchemeAdmin(groups []*model.GroupWithSchemeAdmin) string {
	if len(groups) == 0 {
		return "no groups found"
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d group(s):\n\n", len(groups)))
	for i, group := range groups {
		format.WriteGroup(&result, format.GroupEntry{HeaderLabel: fmt.Sprintf("Group %d", i+1), Group: &group.Group})
	}
	return result.String()
}
