// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetRoleArgs represents arguments for the get_role tool.
type GetRoleArgs struct {
	RoleID   string `json:"role_id,omitempty" jsonschema:"The role ID (provide role_id or role_name),maxLength=26"`
	RoleName string `json:"role_name,omitempty" jsonschema:"The role name (e.g. channel_admin); provide role_id or role_name"`
}

// GetChannelModerationsArgs represents arguments for the get_channel_moderations tool.
type GetChannelModerationsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
}

// UpdateChannelMemberRolesArgs represents arguments for the update_channel_member_roles tool.
type UpdateChannelMemberRolesArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The channel,minLength=26,maxLength=26"`
	UserID    string `json:"user_id" jsonschema:"The member to promote/demote,minLength=26,maxLength=26"`
	Admin     bool   `json:"admin" jsonschema:"true to make the member a channel admin, false for a regular member"`
}

// UpdateTeamMemberRolesArgs represents arguments for the update_team_member_roles tool.
type UpdateTeamMemberRolesArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	UserID string `json:"user_id" jsonschema:"The member to promote/demote,minLength=26,maxLength=26"`
	Admin  bool   `json:"admin" jsonschema:"true to make the member a team admin, false for a regular member"`
}

const (
	getRoleDescription                  = "Get a role definition and its permission set. Parameters: role_id OR role_name (e.g. channel_admin, team_admin, system_user)."
	getChannelModerationsDescription    = "Get a channel's moderation settings (which actions members and guests may take). Parameters: channel_id (required)."
	updateChannelMemberRolesDescription = "Promote or demote a channel member (e.g. channel admin). Parameters: channel_id (required), user_id (required), admin (true/false)."
	updateTeamMemberRolesDescription    = "Promote or demote a team member (e.g. team admin). Parameters: team_id (required), user_id (required), admin (true/false)."
)

// getRoleTools returns the role and permission tools.
func (p *MattermostToolProvider) getRoleTools() []MCPTool {
	return []MCPTool{
		{Name: "get_role", Description: getRoleDescription, Schema: NewJSONSchemaForAccessMode[GetRoleArgs](string(p.accessMode)), Resolver: typed("get_role", p.toolGetRole)},
		{Name: "get_channel_moderations", Description: getChannelModerationsDescription, Schema: NewJSONSchemaForAccessMode[GetChannelModerationsArgs](string(p.accessMode)), Resolver: typed("get_channel_moderations", p.toolGetChannelModerations)},
		{Name: "update_channel_member_roles", Description: updateChannelMemberRolesDescription, Schema: NewJSONSchemaForAccessMode[UpdateChannelMemberRolesArgs](string(p.accessMode)), Resolver: typed("update_channel_member_roles", p.toolUpdateChannelMemberRoles)},
		{Name: "update_team_member_roles", Description: updateTeamMemberRolesDescription, Schema: NewJSONSchemaForAccessMode[UpdateTeamMemberRolesArgs](string(p.accessMode)), Resolver: typed("update_team_member_roles", p.toolUpdateTeamMemberRoles)},
	}
}

// toolGetRole implements the get_role tool.
func (p *MattermostToolProvider) toolGetRole(mcpContext *MCPToolContext, args GetRoleArgs) (string, error) {
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var role *model.Role
	var err error
	switch {
	case args.RoleID != "":
		if vErr := requireID("role_id", args.RoleID); vErr != nil {
			return "", vErr
		}
		role, _, err = client.GetRole(ctx, args.RoleID)
	case args.RoleName != "":
		role, _, err = client.GetRoleByName(ctx, args.RoleName)
	default:
		return "", fmt.Errorf("provide role_id or role_name")
	}
	if err != nil {
		return "", fmt.Errorf("error fetching role: %w", err)
	}

	var result strings.Builder
	format.WriteRole(&result, format.RoleEntry{Role: role})
	return result.String(), nil
}

// toolGetChannelModerations implements the get_channel_moderations tool.
func (p *MattermostToolProvider) toolGetChannelModerations(mcpContext *MCPToolContext, args GetChannelModerationsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	moderations, _, err := mcpContext.Client.GetChannelModerations(mcpContext.Ctx, args.ChannelID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching channel moderations: %w", err)
	}
	if len(moderations) == 0 {
		return "no moderation settings found for this channel", nil
	}
	var result strings.Builder
	format.WriteChannelModerations(&result, moderations)
	return result.String(), nil
}

// toolUpdateChannelMemberRoles implements the update_channel_member_roles tool.
func (p *MattermostToolProvider) toolUpdateChannelMemberRoles(mcpContext *MCPToolContext, args UpdateChannelMemberRolesArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}

	schemeRoles := &model.SchemeRoles{SchemeUser: true, SchemeAdmin: args.Admin}
	if _, err := mcpContext.Client.UpdateChannelMemberSchemeRoles(mcpContext.Ctx, args.ChannelID, args.UserID, schemeRoles); err != nil {
		return "", fmt.Errorf("error updating channel member roles: %w", err)
	}

	role := "member"
	if args.Admin {
		role = "channel admin"
	}
	return fmt.Sprintf("Successfully set user %s as %s in channel %s", args.UserID, role, args.ChannelID), nil
}

// toolUpdateTeamMemberRoles implements the update_team_member_roles tool.
func (p *MattermostToolProvider) toolUpdateTeamMemberRoles(mcpContext *MCPToolContext, args UpdateTeamMemberRolesArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}

	schemeRoles := &model.SchemeRoles{SchemeUser: true, SchemeAdmin: args.Admin}
	if _, err := mcpContext.Client.UpdateTeamMemberSchemeRoles(mcpContext.Ctx, args.TeamID, args.UserID, schemeRoles); err != nil {
		return "", fmt.Errorf("error updating team member roles: %w", err)
	}

	role := "member"
	if args.Admin {
		role = "team admin"
	}
	return fmt.Sprintf("Successfully set user %s as %s in team %s", args.UserID, role, args.TeamID), nil
}
