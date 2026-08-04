// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetUserStatusArgs represents arguments for the get_user_status tool.
type GetUserStatusArgs struct {
	UserID string `json:"user_id" jsonschema:"The user whose presence to fetch,minLength=26,maxLength=26"`
}

// GetUsersStatusesArgs represents arguments for the get_users_statuses tool.
type GetUsersStatusesArgs struct {
	UserIDs []string `json:"user_ids" jsonschema:"The user IDs whose presence to fetch"`
}

// GetUserCustomStatusArgs represents arguments for the get_user_custom_status tool.
type GetUserCustomStatusArgs struct {
	UserID string `json:"user_id" jsonschema:"The user whose custom status to fetch,minLength=26,maxLength=26"`
}

// SetStatusArgs represents arguments for the set_status tool.
type SetStatusArgs struct {
	Status string `json:"status" jsonschema:"Your presence status,enum=online,enum=away,enum=offline,enum=dnd"`
}

// SetDndArgs represents arguments for the set_dnd tool.
type SetDndArgs struct {
	EndTime string `json:"end_time,omitempty" jsonschema:"Optional ISO 8601 / RFC3339 time when Do Not Disturb should end; omit for indefinite,format=date-time"`
}

const (
	getUserStatusDescription       = "Get a user's presence: online, away, do not disturb (dnd), or offline. Parameters: user_id (required)."
	getUsersStatusesDescription    = "Batch-fetch presence for a list of user IDs. Parameters: user_ids (required list)."
	getUserCustomStatusDescription = "Get a user's custom status (emoji, text, expiry). Parameters: user_id (required)."
	setStatusDescription           = "Set your own presence: online, away, offline, or do not disturb (dnd). Parameters: status (required)."
	setDndDescription              = "Set your own Do Not Disturb (dnd) status, optionally until a given time. Parameters: end_time (optional ISO 8601 timestamp)."
)

// getStatusTools returns the presence and custom-status tools.
func (p *MattermostToolProvider) getStatusTools() []MCPTool {
	return []MCPTool{
		{Name: "get_user_status", Description: getUserStatusDescription, Schema: NewJSONSchemaForAccessMode[GetUserStatusArgs](string(p.accessMode)), Resolver: typed("get_user_status", p.toolGetUserStatus)},
		{Name: "get_users_statuses", Description: getUsersStatusesDescription, Schema: NewJSONSchemaForAccessMode[GetUsersStatusesArgs](string(p.accessMode)), Resolver: typed("get_users_statuses", p.toolGetUsersStatuses)},
		{Name: "get_user_custom_status", Description: getUserCustomStatusDescription, Schema: NewJSONSchemaForAccessMode[GetUserCustomStatusArgs](string(p.accessMode)), Resolver: typed("get_user_custom_status", p.toolGetUserCustomStatus)},
		{Name: "set_status", Description: setStatusDescription, Schema: NewJSONSchemaForAccessMode[SetStatusArgs](string(p.accessMode)), Resolver: typed("set_status", p.toolSetStatus)},
		{Name: "set_dnd", Description: setDndDescription, Schema: NewJSONSchemaForAccessMode[SetDndArgs](string(p.accessMode)), Resolver: typed("set_dnd", p.toolSetDnd)},
	}
}

// toolGetUserStatus implements the get_user_status tool.
func (p *MattermostToolProvider) toolGetUserStatus(mcpContext *MCPToolContext, args GetUserStatusArgs) (string, error) {
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	status, _, err := mcpContext.Client.GetUserStatus(mcpContext.Ctx, args.UserID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user status: %w", err)
	}
	var result strings.Builder
	format.WriteStatus(&result, format.StatusEntry{
		Status:   status,
		Username: p.usernameFor(mcpContext.Ctx, mcpContext.Client, args.UserID),
	})
	return result.String(), nil
}

// toolGetUsersStatuses implements the get_users_statuses tool.
func (p *MattermostToolProvider) toolGetUsersStatuses(mcpContext *MCPToolContext, args GetUsersStatusesArgs) (string, error) {
	if len(args.UserIDs) == 0 {
		return "", fmt.Errorf("user_ids cannot be empty")
	}
	for _, id := range args.UserIDs {
		if err := requireID("user_ids", id); err != nil {
			return "", err
		}
	}
	statuses, _, err := mcpContext.Client.GetUsersStatusesByIds(mcpContext.Ctx, args.UserIDs)
	if err != nil {
		return "", fmt.Errorf("error fetching user statuses: %w", err)
	}
	if len(statuses) == 0 {
		return "no statuses found", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Statuses for %d user(s):\n\n", len(statuses)))
	for i, status := range statuses {
		format.WriteStatus(&result, format.StatusEntry{
			HeaderLabel: fmt.Sprintf("Status %d", i+1),
			Status:      status,
		})
	}
	return result.String(), nil
}

// toolGetUserCustomStatus implements the get_user_custom_status tool. There is no
// getter endpoint; the custom status is read from the user profile props.
func (p *MattermostToolProvider) toolGetUserCustomStatus(mcpContext *MCPToolContext, args GetUserCustomStatusArgs) (string, error) {
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	user, _, err := mcpContext.Client.GetUser(mcpContext.Ctx, args.UserID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user: %w", err)
	}
	var result strings.Builder
	format.WriteCustomStatus(&result, args.UserID, user.GetCustomStatus())
	return result.String(), nil
}

// toolSetStatus implements the set_status tool.
func (p *MattermostToolProvider) toolSetStatus(mcpContext *MCPToolContext, args SetStatusArgs) (string, error) {
	switch args.Status {
	case model.StatusOnline, model.StatusAway, model.StatusOffline, model.StatusDnd:
	default:
		return "", fmt.Errorf("invalid status %q (must be online, away, offline, or dnd)", args.Status)
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	if _, _, err := mcpContext.Client.UpdateUserStatus(mcpContext.Ctx, userID, &model.Status{UserId: userID, Status: args.Status}); err != nil {
		return "", fmt.Errorf("error setting status: %w", err)
	}
	return fmt.Sprintf("Successfully set your status to %s", args.Status), nil
}

// toolSetDnd implements the set_dnd tool.
func (p *MattermostToolProvider) toolSetDnd(mcpContext *MCPToolContext, args SetDndArgs) (string, error) {
	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	status := &model.Status{UserId: userID, Status: model.StatusDnd}
	if args.EndTime != "" {
		endMillis, parseErr := parseTimeMillis(args.EndTime)
		if parseErr != nil {
			return "", parseErr
		}
		// DNDEndTime is expressed in seconds.
		status.DNDEndTime = endMillis / 1000
	}

	if _, _, err := mcpContext.Client.UpdateUserStatus(mcpContext.Ctx, userID, status); err != nil {
		return "", fmt.Errorf("error setting do not disturb: %w", err)
	}

	if args.EndTime != "" {
		return fmt.Sprintf("Successfully set Do Not Disturb until %s", args.EndTime), nil
	}
	return "Successfully set Do Not Disturb", nil
}
