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

// GetTeamInfoArgs represents arguments for the get_team_info tool
type GetTeamInfoArgs struct {
	TeamID          string `json:"team_id,omitempty" jsonschema:"The exact team ID (fastest, most reliable method)"`
	TeamDisplayName string `json:"team_display_name,omitempty" jsonschema:"The human-readable display name users see (e.g. 'Engineering Team')"`
	TeamName        string `json:"team_name,omitempty" jsonschema:"The URL-friendly team name (e.g. 'engineering-team')"`
}

// GetTeamMembersArgs represents arguments for the get_team_members tool
type GetTeamMembersArgs struct {
	TeamID string `json:"team_id" jsonschema:"ID of the team to get members for,minLength=26,maxLength=26"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Number of members to return (default: 50, max: 200),minimum=1,maximum=200"`
	Page   int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
}

// CreateTeamArgs represents arguments for the create_team tool (dev mode only)
type CreateTeamArgs struct {
	Name        string `json:"name" jsonschema:"URL name for the team,minLength=1,maxLength=64"`
	DisplayName string `json:"display_name" jsonschema:"Display name for the team,minLength=1,maxLength=64"`
	Type        string `json:"type" jsonschema:"Team type,enum=O,enum=I"`
	Description string `json:"description" jsonschema:"Team description,maxLength=255"`
	TeamIcon    string `json:"team_icon,omitempty" access:"local" jsonschema:"File path or URL to set as team icon (supports .jpeg, .jpg, .png, .gif)"`
}

// AddUserToTeamArgs represents arguments for the add_user_to_team tool (dev mode only)
type AddUserToTeamArgs struct {
	UserID string `json:"user_id" jsonschema:"ID of the user to add"`
	TeamID string `json:"team_id" jsonschema:"ID of the team to add user to"`
}

// getTeamTools returns all team-related tools
func (p *MattermostToolProvider) getTeamTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "get_team_info",
			Description: "Get information about a team. Provide ONE of the following parameters: team_id (fastest), team_display_name (user-visible name), or team_name (URL name). Returns team metadata including ID, names, type, description, and creation date. Example: {\"team_display_name\": \"Engineering Team\"} or {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\"}",
			Schema:      NewJSONSchemaForAccessMode[GetTeamInfoArgs](string(p.accessMode)),
			Resolver:    p.toolGetTeamInfo,
		},
		{
			Name:        "get_team_members",
			Description: "Get members of a team with pagination support. Parameters: team_id (required), limit (1-200, default 50), page (0+, default 0). Returns user details for each member including username, email, display name, and roles. Example: {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\", \"limit\": 10, \"page\": 0}",
			Schema:      NewJSONSchemaForAccessMode[GetTeamMembersArgs](string(p.accessMode)),
			Resolver:    p.toolGetTeamMembers,
		},
	}
}

// getDevTeamTools returns development team-related tools for MCP
func (p *MattermostToolProvider) getDevTeamTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "create_team",
			Description: "Create a new team (dev mode only)",
			Schema:      NewJSONSchemaForAccessMode[CreateTeamArgs](string(p.accessMode)),
			Resolver:    p.toolCreateTeam,
		},
		{
			Name:        "add_user_to_team",
			Description: "Add a user to a team (dev mode only)",
			Schema:      NewJSONSchemaForAccessMode[AddUserToTeamArgs](string(p.accessMode)),
			Resolver:    p.toolAddUserToTeam,
		},
	}
}

// toolGetTeamInfo implements the get_team_info tool
func (p *MattermostToolProvider) toolGetTeamInfo(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetTeamInfoArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_team_info: %w", err)
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	var team *model.Team

	// Try different lookup methods based on provided parameters
	switch {
	case args.TeamID != "":
		// Validate team ID format
		if !model.IsValidId(args.TeamID) {
			return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
		}
		// Direct ID lookup - fastest method
		team, _, err = client.GetTeam(ctx, args.TeamID, "")
		if err != nil {
			return "team not found by ID", fmt.Errorf("error fetching team by ID: %w", err)
		}
	case args.TeamDisplayName != "":
		// Lookup by display name - get all teams for user and search
		// Get current user ID for the API call
		user, _, userErr := client.GetMe(ctx, "")
		if userErr != nil {
			return "failed to get current user", fmt.Errorf("error getting current user: %w", userErr)
		}

		teams, _, teamsErr := client.GetTeamsForUser(ctx, user.Id, "")
		if teamsErr != nil {
			return "failed to fetch user teams", fmt.Errorf("error fetching user teams: %w", teamsErr)
		}

		for _, t := range teams {
			if t.DisplayName == args.TeamDisplayName {
				team = t
				break
			}
		}

		if team == nil {
			return "team not found by display name", fmt.Errorf("no team found with display name: %s", args.TeamDisplayName)
		}
	case args.TeamName != "":
		// Lookup by name
		team, _, err = client.GetTeamByName(ctx, args.TeamName, "")
		if err != nil {
			return "team not found by name", fmt.Errorf("error fetching team by name: %w", err)
		}
	default:
		return "either team_id, team_display_name, or team_name must be provided", fmt.Errorf("insufficient parameters for team lookup")
	}

	// Format the response
	var result strings.Builder
	result.WriteString("Team Information:\n")
	result.WriteString(fmt.Sprintf("ID: %s\n", team.Id))
	result.WriteString(fmt.Sprintf("Name: %s\n", team.Name))
	result.WriteString(fmt.Sprintf("Display Name: %s\n", team.DisplayName))
	result.WriteString(fmt.Sprintf("Type: %s\n", team.Type))

	if team.Description != "" {
		result.WriteString(fmt.Sprintf("Description: %s\n", team.Description))
	}

	result.WriteString(fmt.Sprintf("Created: %s\n", time.Unix(team.CreateAt/1000, 0).Format("2006-01-02 15:04:05")))

	// Get member count
	teamStats, _, err := client.GetTeamStats(ctx, team.Id, "")
	if err == nil {
		result.WriteString(fmt.Sprintf("Member Count: %s\n", strconv.FormatInt(teamStats.TotalMemberCount, 10)))
	}

	return result.String(), nil
}

// toolGetTeamMembers implements the get_team_members tool
func (p *MattermostToolProvider) toolGetTeamMembers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args GetTeamMembersArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool get_team_members: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
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

	// Get team members
	members, _, err := client.GetTeamMembers(ctx, args.TeamID, args.Page, args.Limit, "")
	if err != nil {
		return "failed to fetch team members", fmt.Errorf("error fetching team members: %w", err)
	}

	if len(members) == 0 {
		return "no members found in this team", nil
	}

	// Get user details for each member
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Team Members (page %d, showing %d members):\n\n", args.Page, len(members)))

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

// toolCreateTeam implements the create_team tool using the context client
func (p *MattermostToolProvider) toolCreateTeam(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateTeamArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_team: %w", err)
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

	// Validate team type
	if args.Type != "O" && args.Type != "I" {
		return "type must be 'O' for open or 'I' for invite only", fmt.Errorf("invalid team type: %s", args.Type)
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Create the team
	team := &model.Team{
		Name:        args.Name,
		DisplayName: args.DisplayName,
		Type:        args.Type,
		Description: args.Description,
	}

	createdTeam, _, err := client.CreateTeam(ctx, team)
	if err != nil {
		return "failed to create team", fmt.Errorf("error creating team: %w", err)
	}

	var teamIconMessage string
	// Upload team icon if specified
	if args.TeamIcon != "" {
		// Validate image file type
		fileName := extractFileNameForLocal(args.TeamIcon, mcpContext.AccessMode)
		if !isValidImageFile(fileName) {
			teamIconMessage = " (team icon upload failed: unsupported file type, only .jpeg, .jpg, .png, .gif are supported)"
		} else {
			imageData, err := fetchFileDataForLocal(args.TeamIcon, mcpContext.AccessMode)
			if err != nil {
				teamIconMessage = fmt.Sprintf(" (team icon upload failed: %v)", err)
			} else {
				_, err = client.SetTeamIcon(ctx, createdTeam.Id, imageData)
				if err != nil {
					teamIconMessage = fmt.Sprintf(" (team icon upload failed: %v)", err)
				} else {
					teamIconMessage = " (team icon uploaded successfully)"
				}
			}
		}
	}

	return fmt.Sprintf("Successfully created team '%s' with ID: %s%s", createdTeam.DisplayName, createdTeam.Id, teamIconMessage), nil
}

// toolAddUserToTeam implements the add_user_to_team tool using the context client
func (p *MattermostToolProvider) toolAddUserToTeam(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args AddUserToTeamArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool add_user_to_team: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.UserID) {
		return "invalid user_id format", fmt.Errorf("user_id must be a valid ID")
	}
	if !model.IsValidId(args.TeamID) {
		return "invalid team_id format", fmt.Errorf("team_id must be a valid ID")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

	// Add user to team
	_, _, err = client.AddTeamMember(ctx, args.TeamID, args.UserID)
	if err != nil {
		return "failed to add user to team", fmt.Errorf("error adding user to team: %w", err)
	}

	// Get user and team info for confirmation
	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	team, _, teamErr := client.GetTeam(ctx, args.TeamID, "")

	if userErr != nil || teamErr != nil {
		return fmt.Sprintf("Successfully added user %s to team %s", args.UserID, args.TeamID), nil
	}

	return fmt.Sprintf("Successfully added user '%s' to team '%s'", user.Username, team.DisplayName), nil
}
