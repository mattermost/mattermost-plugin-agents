// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetGroupInfoArgs represents arguments for the get_group_info tool.
type GetGroupInfoArgs struct {
	GroupID string `json:"group_id" jsonschema:"The group ID,minLength=26,maxLength=26"`
}

// ListGroupsArgs represents arguments for the list_groups tool.
type ListGroupsArgs struct {
	Query   string `json:"query,omitempty" jsonschema:"Optional search term to filter groups by name"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Groups per page (default: 60, max: 200),minimum=1,maximum=200"`
}

// GetUserGroupsArgs represents arguments for the get_user_groups tool.
type GetUserGroupsArgs struct {
	UserID string `json:"user_id,omitempty" jsonschema:"The user; omit for yourself,maxLength=26"`
}

// GetChannelGroupsArgs represents arguments for the get_channel_groups tool.
type GetChannelGroupsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Groups per page (default: 60, max: 200),minimum=1,maximum=200"`
}

// GetTeamGroupsArgs represents arguments for the get_team_groups tool.
type GetTeamGroupsArgs struct {
	TeamID  string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Groups per page (default: 60, max: 200),minimum=1,maximum=200"`
}

// GetUsersInGroupChannelsArgs represents arguments for the get_users_in_group_channels tool.
type GetUsersInGroupChannelsArgs struct {
	ChannelIDs []string `json:"channel_ids" jsonschema:"The group-message (GM) channel IDs"`
}

// CreateGroupArgs represents arguments for the create_group tool.
type CreateGroupArgs struct {
	DisplayName    string `json:"display_name" jsonschema:"The group's display name,minLength=1,maxLength=128"`
	Name           string `json:"name,omitempty" jsonschema:"Mention handle (lowercase letters, numbers, periods, dashes, underscores). Required when allow_reference is true,maxLength=64"`
	Description    string `json:"description,omitempty" jsonschema:"Optional group description,maxLength=1024"`
	AllowReference bool   `json:"allow_reference,omitempty" jsonschema:"Allow the group to be mentioned with @name. Requires name"`
}

// UpdateGroupArgs represents arguments for the update_group tool.
type UpdateGroupArgs struct {
	GroupID        string  `json:"group_id" jsonschema:"The group to update,minLength=26,maxLength=26"`
	DisplayName    *string `json:"display_name,omitempty" jsonschema:"New display name"`
	Name           *string `json:"name,omitempty" jsonschema:"New mention handle (lowercase letters, numbers, periods, dashes, underscores),maxLength=64"`
	Description    *string `json:"description,omitempty" jsonschema:"New description. Pass an empty string to clear it,maxLength=1024"`
	AllowReference *bool   `json:"allow_reference,omitempty" jsonschema:"Whether the group can be mentioned with @name. Turning this on requires the group to have a name"`
}

const (
	getGroupInfoDescription            = "Get a group's metadata. Parameters: group_id (required)."
	listGroupsDescription              = "List or search custom and LDAP-synced groups. Parameters: query (optional search term), page, per_page."
	getUserGroupsDescription           = "Get the groups a user belongs to. Parameters: user_id (optional, defaults to you)."
	getChannelGroupsDescription        = "Get the groups synced to a channel. Parameters: channel_id (required), page, per_page."
	getTeamGroupsDescription           = "Get the groups synced to a team. Parameters: team_id (required), page, per_page."
	getUsersInGroupChannelsDescription = "Get the members of given group-message (GM) channels. Parameters: channel_ids (required list)."
	createGroupDescription             = "Create a custom user group. Parameters: display_name (required), name (mention handle; required when allow_reference is true), description (optional), allow_reference (optional). LDAP and plugin-synced groups cannot be created this way."
	updateGroupDescription             = "Update a user group's display name, mention handle, description, or whether it can be @mentioned. Parameters: group_id (required), plus any of display_name, name, description, allow_reference. Does not add or remove members."
)

// getGroupTools returns the group tools.
func (p *MattermostToolProvider) getGroupTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, "get_group_info", getGroupInfoDescription, p.toolGetGroupInfo),
		mcpReadTool(p, "list_groups", listGroupsDescription, p.toolListGroups),
		mcpReadTool(p, "get_user_groups", getUserGroupsDescription, p.toolGetUserGroups),
		mcpReadTool(p, "get_channel_groups", getChannelGroupsDescription, p.toolGetChannelGroups),
		mcpReadTool(p, "get_team_groups", getTeamGroupsDescription, p.toolGetTeamGroups),
		mcpReadTool(p, "get_users_in_group_channels", getUsersInGroupChannelsDescription, p.toolGetUsersInGroupChannels),
		mcpTool(p, "create_group", createGroupDescription, p.toolCreateGroup),
		mcpTool(p, "update_group", updateGroupDescription, p.toolUpdateGroup),
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
	page, perPage := normalizePage(args.Page, args.PerPage, 60, 200)
	opts := model.GroupSearchOpts{Q: args.Query, PageOpts: &model.PageOpts{Page: page, PerPage: perPage}}
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
	userID, err := p.resolveUserIDOrDefault(mcpContext, args.UserID)
	if err != nil {
		return "", err
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
	page, perPage := normalizePage(args.Page, args.PerPage, 60, 200)
	groups, _, _, err := mcpContext.Client.GetGroupsByChannel(mcpContext.Ctx, args.ChannelID, model.GroupSearchOpts{PageOpts: &model.PageOpts{Page: page, PerPage: perPage}})
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
	page, perPage := normalizePage(args.Page, args.PerPage, 60, 200)
	groups, _, _, err := mcpContext.Client.GetGroupsByTeam(mcpContext.Ctx, args.TeamID, model.GroupSearchOpts{PageOpts: &model.PageOpts{Page: page, PerPage: perPage}})
	if err != nil {
		return "", fmt.Errorf("error fetching team groups: %w", err)
	}
	return formatGroupListWithSchemeAdmin(groups), nil
}

// toolGetUsersInGroupChannels implements the get_users_in_group_channels tool.
func (p *MattermostToolProvider) toolGetUsersInGroupChannels(mcpContext *MCPToolContext, args GetUsersInGroupChannelsArgs) (string, error) {
	if err := requireIDs("channel_ids", args.ChannelIDs); err != nil {
		return "", err
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
		fmt.Fprintf(&result, "Group message %s (%d members):\n", channelID, len(users))
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
	fmt.Fprintf(&result, "Found %d group(s):\n\n", len(groups))
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
	fmt.Fprintf(&result, "Found %d group(s):\n\n", len(groups))
	for i, group := range groups {
		format.WriteGroup(&result, format.GroupEntry{HeaderLabel: fmt.Sprintf("Group %d", i+1), Group: &group.Group})
	}
	return result.String()
}

// toolCreateGroup implements the create_group tool.
// Source is always custom: callers cannot create LDAP or plugin-synced groups.
func (p *MattermostToolProvider) toolCreateGroup(mcpContext *MCPToolContext, args CreateGroupArgs) (string, error) {
	if err := validateGroupDisplayName(args.DisplayName); err != nil {
		return "", err
	}
	if err := validateGroupDescription(args.Description); err != nil {
		return "", err
	}
	if args.Name != "" {
		if err := validateGroupName(args.Name); err != nil {
			return "", err
		}
	} else if args.AllowReference {
		return "", fmt.Errorf("name is required when allow_reference is true")
	}

	group := &model.Group{
		DisplayName:    args.DisplayName,
		Description:    args.Description,
		Source:         model.GroupSourceCustom,
		AllowReference: args.AllowReference,
	}
	if args.Name != "" {
		group.Name = &args.Name
	}

	created, _, err := mcpContext.Client.CreateGroup(mcpContext.Ctx, group)
	if err != nil {
		return "", fmt.Errorf("error creating group: %w", err)
	}
	return fmt.Sprintf("Successfully created custom group '%s' (ID: %s)", created.DisplayName, created.Id), nil
}

// toolUpdateGroup implements the update_group tool.
func (p *MattermostToolProvider) toolUpdateGroup(mcpContext *MCPToolContext, args UpdateGroupArgs) (string, error) {
	if err := requireID("group_id", args.GroupID); err != nil {
		return "", err
	}
	if args.DisplayName == nil && args.Name == nil && args.Description == nil && args.AllowReference == nil {
		return "", fmt.Errorf("provide at least one of display_name, name, description, allow_reference to update")
	}
	if args.DisplayName != nil {
		if err := validateGroupDisplayName(*args.DisplayName); err != nil {
			return "", err
		}
	}
	if args.Name != nil {
		if err := validateGroupName(*args.Name); err != nil {
			return "", err
		}
	}
	if args.Description != nil {
		if err := validateGroupDescription(*args.Description); err != nil {
			return "", err
		}
	}

	patch := &model.GroupPatch{
		Name:           args.Name,
		DisplayName:    args.DisplayName,
		Description:    args.Description,
		AllowReference: args.AllowReference,
	}
	updated, _, err := mcpContext.Client.PatchGroup(mcpContext.Ctx, args.GroupID, patch)
	if err != nil {
		return "", fmt.Errorf("error updating group: %w", err)
	}
	return fmt.Sprintf("Successfully updated group '%s' (ID: %s)", updated.DisplayName, updated.Id), nil
}

func validateGroupDisplayName(displayName string) error {
	if displayName == "" {
		return fmt.Errorf("display_name cannot be empty")
	}
	if len(displayName) > model.GroupDisplayNameMaxLength {
		return fmt.Errorf("display_name must be at most %d characters", model.GroupDisplayNameMaxLength)
	}
	return nil
}

func validateGroupDescription(description string) error {
	if len(description) > model.GroupDescriptionMaxLength {
		return fmt.Errorf("description must be at most %d characters", model.GroupDescriptionMaxLength)
	}
	return nil
}

// validateGroupName checks a mention handle with the same rules as model.Group.IsValidName.
func validateGroupName(name string) error {
	group := &model.Group{Name: &name}
	if appErr := group.IsValidName(); appErr != nil {
		return fmt.Errorf("name must be 1-%d characters of lowercase letters, numbers, periods, dashes, or underscores, and must not be all, here, or channel", model.GroupNameMaxLength)
	}
	return nil
}
