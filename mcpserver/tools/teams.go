// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetTeamInfoArgs represents arguments for the get_team_info tool
type GetTeamInfoArgs struct {
	TeamID   string `json:"team_id,omitempty" jsonschema:"The exact team ID (fastest, most reliable method),maxLength=26"`
	TeamName string `json:"team_name,omitempty" jsonschema:"Team name to search for — matches against both display name and URL name (case-insensitive, supports partial matches)"`
}

// GetTeamMembersArgs represents arguments for the get_team_members tool
type GetTeamMembersArgs struct {
	TeamID      string `json:"team_id" jsonschema:"ID of the team to get members for,minLength=26,maxLength=26"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Number of members to return (default: 50, max: 200),minimum=1,maximum=200"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	ExcludeBots *bool  `json:"exclude_bots,omitempty" jsonschema:"Exclude bot accounts from results (default: true)"`
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
	UserID string `json:"user_id" jsonschema:"ID of the user to add,minLength=26,maxLength=26"`
	TeamID string `json:"team_id" jsonschema:"ID of the team to add user to,minLength=26,maxLength=26"`
}

// Tool description constants for team-related tools.
const (
	getTeamInfoDescription = "Get information about a team. Provide team_id (fastest) or team_name (matches against both display name and URL name, case-insensitive, supports partial matches). Returns team metadata including ID, names, type, description, and member count. Example: {\"team_name\": \"Engineering\"} or {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\"}"

	getTeamMembersDescription = "Get members of a team with pagination support. Parameters: team_id (required), limit (1-200, default 50), page (0+, default 0), exclude_bots (optional, default true). Returns user details for each member including username, email, display name, and roles. Example: {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\", \"limit\": 10, \"page\": 0}"
)

// getTeamTools returns all team-related tools
func (p *MattermostToolProvider) getTeamTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "get_team_info",
			Description: getTeamInfoDescription,
			Schema:      NewJSONSchemaForAccessMode[GetTeamInfoArgs](string(p.accessMode)),
			Resolver:    typed("get_team_info", p.toolGetTeamInfo),
		},
		{
			Name:        "get_team_members",
			Description: getTeamMembersDescription,
			Schema:      NewJSONSchemaForAccessMode[GetTeamMembersArgs](string(p.accessMode)),
			Resolver:    typed("get_team_members", p.toolGetTeamMembers),
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
			Resolver:    typed("create_team", p.toolCreateTeam),
		},
		{
			Name:        "add_user_to_team",
			Description: "Add a user to a team (dev mode only)",
			Schema:      NewJSONSchemaForAccessMode[AddUserToTeamArgs](string(p.accessMode)),
			Resolver:    typed("add_user_to_team", p.toolAddUserToTeam),
		},
	}
}

// toolGetTeamInfo implements the get_team_info tool
func (p *MattermostToolProvider) toolGetTeamInfo(mcpContext *MCPToolContext, args GetTeamInfoArgs) (string, error) {
	var err error

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var team *model.Team

	switch {
	case args.TeamID != "":
		if validationErr := requireID("team_id", args.TeamID); validationErr != nil {
			return "", validationErr
		}
		team, _, err = client.GetTeam(ctx, args.TeamID, "")
		if err != nil {
			return "", fmt.Errorf("error fetching team by ID: %w", err)
		}
	case args.TeamName != "":
		var msg string
		team, msg, err = p.resolveTeamByName(mcpContext, args.TeamName)
		if err != nil {
			return "", err
		}
		if msg != "" {
			// No unique match — surface the disambiguation list or the
			// not-found guidance to the model (not an error).
			return msg, nil
		}
	default:
		return "", fmt.Errorf("insufficient parameters for team lookup")
	}

	// Get member count
	var memberCount int64 = -1
	teamStats, _, err := client.GetTeamStats(ctx, team.Id, "")
	if err == nil {
		memberCount = teamStats.TotalMemberCount
	}

	// Format the response
	var result strings.Builder
	format.WriteTeam(&result, format.TeamEntry{
		Team:        team,
		MemberCount: memberCount,
	})

	return result.String(), nil
}

// resolveTeamByName resolves a team by name using multiple strategies:
// 1. Exact display name match (case-insensitive) from user's teams
// 2. Exact URL name match from user's teams
// 3. Substring display name match from user's teams
// 4. SearchTeams API as final fallback
//
// Returns (team, "", nil) on a unique match; (nil, message, nil) when there is no
// unique match, where message is either a disambiguation list (multiple matches)
// or recovery guidance (none found) to relay to the model; or (nil, "", error)
// when a lookup API call fails.
func (p *MattermostToolProvider) resolveTeamByName(mcpContext *MCPToolContext, name string) (*model.Team, string, error) {
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	user, _, userErr := client.GetMe(ctx, "")
	if userErr != nil {
		return nil, "", fmt.Errorf("error getting current user: %w", userErr)
	}

	teams, _, teamsErr := client.GetTeamsForUser(ctx, user.Id, "")
	if teamsErr != nil {
		return nil, "", fmt.Errorf("error fetching user teams: %w", teamsErr)
	}

	// 1. Exact display name match (case-insensitive)
	for _, t := range teams {
		if strings.EqualFold(t.DisplayName, name) {
			return t, "", nil
		}
	}

	// 2. Exact URL name match
	for _, t := range teams {
		if strings.EqualFold(t.Name, name) {
			return t, "", nil
		}
	}

	// 3. Substring match on display name (case-insensitive)
	nameLower := strings.ToLower(name)
	var substringMatches []*model.Team
	for _, t := range teams {
		if strings.Contains(strings.ToLower(t.DisplayName), nameLower) {
			substringMatches = append(substringMatches, t)
		}
	}

	if len(substringMatches) == 1 {
		return substringMatches[0], "", nil
	}
	if len(substringMatches) > 1 {
		return nil, formatTeamDisambiguation(name, substringMatches), nil
	}

	// 4. SearchTeams API as fallback for teams the user may not be a member of
	searchResults, _, searchErr := client.SearchTeams(ctx, &model.TeamSearch{Term: name})
	if searchErr == nil && len(searchResults) == 1 {
		return searchResults[0], "", nil
	}
	if searchErr == nil && len(searchResults) > 1 {
		return nil, formatTeamDisambiguation(name, searchResults), nil
	}
	if searchErr != nil {
		return nil, "", fmt.Errorf("error searching teams: %w", searchErr)
	}

	// Nothing found — surface recovery guidance to the model as an informational
	// result (not an error), so the guidance actually reaches the model.
	msg := fmt.Sprintf("No team found matching '%s'.", name)
	msg += "\n\nACTION REQUIRED - Try these alternatives before asking the user:\n"
	msg += "1. Call get_user_channels to list all channels (includes team info) you have access to\n"
	msg += "2. Only ask the user for help after trying alternatives above."
	return nil, msg, nil
}

// formatTeamDisambiguation builds a message listing multiple team matches for the LLM to choose from.
func formatTeamDisambiguation(searchTerm string, teams []*model.Team) string {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Multiple teams match '%s'. Please specify which one by calling get_team_info with team_id:\n\n", searchTerm))
	for _, t := range teams {
		msg.WriteString(fmt.Sprintf("- '%s' (URL name: %s, ID: %s)\n", t.DisplayName, t.Name, t.Id))
	}
	return msg.String()
}

// toolGetTeamMembers implements the get_team_members tool
func (p *MattermostToolProvider) toolGetTeamMembers(mcpContext *MCPToolContext, args GetTeamMembersArgs) (string, error) {
	// Validate required fields
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
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
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Default exclude_bots to true
	excludeBots := args.ExcludeBots == nil || *args.ExcludeBots

	// Get team members
	members, _, err := client.GetTeamMembers(ctx, args.TeamID, args.Page, args.Limit, "")
	if err != nil {
		return "", fmt.Errorf("error fetching team members: %w", err)
	}

	if len(members) == 0 {
		return "no members found in this team", nil
	}

	rendered := make([]renderMember, len(members))
	for i, member := range members {
		rendered[i] = renderMember{
			userID: member.UserId,
			role:   format.MemberRole(member.SchemeAdmin, member.SchemeGuest, member.SchemeUser),
		}
	}

	return p.renderMembers(ctx, client, "Team Members", args.Page, rendered, excludeBots), nil
}

// toolCreateTeam implements the create_team tool using the context client
func (p *MattermostToolProvider) toolCreateTeam(mcpContext *MCPToolContext, args CreateTeamArgs) (string, error) {
	// Validate required fields
	if args.Name == "" {
		return "", fmt.Errorf("name cannot be empty")
	}
	if args.DisplayName == "" {
		return "", fmt.Errorf("display_name cannot be empty")
	}
	if args.Type == "" {
		return "", fmt.Errorf("type cannot be empty")
	}

	// Validate team type
	if args.Type != "O" && args.Type != "I" {
		return "", fmt.Errorf("invalid team type: %s", args.Type)
	}

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Create the team
	team := &model.Team{
		Name:        args.Name,
		DisplayName: args.DisplayName,
		Type:        args.Type,
		Description: args.Description,
	}

	createdTeam, _, err := client.CreateTeam(ctx, team)
	if err != nil {
		return "", fmt.Errorf("error creating team: %w", err)
	}

	var teamIconMessage string
	// Upload team icon if specified
	if args.TeamIcon != "" {
		// Validate image file type
		fileName := extractFileNameForLocal(args.TeamIcon, mcpContext.AccessMode)
		if !isValidImageFile(fileName) {
			teamIconMessage = " (team icon upload failed: unsupported file type, only .jpeg, .jpg, .png, .gif are supported)"
		} else {
			imageData, err := fetchFileDataForLocal(mcpContext.Ctx, args.TeamIcon, mcpContext.AccessMode)
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
func (p *MattermostToolProvider) toolAddUserToTeam(mcpContext *MCPToolContext, args AddUserToTeamArgs) (string, error) {
	// Validate required fields
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Add user to team
	_, _, err := client.AddTeamMember(ctx, args.TeamID, args.UserID)
	if err != nil {
		return "", fmt.Errorf("error adding user to team: %w", err)
	}

	// Get user and team info for confirmation
	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	team, _, teamErr := client.GetTeam(ctx, args.TeamID, "")

	if userErr != nil || teamErr != nil {
		return fmt.Sprintf("Successfully added user %s to team %s", args.UserID, args.TeamID), nil
	}

	return fmt.Sprintf("Successfully added user '%s' to team '%s'", user.Username, team.DisplayName), nil
}
