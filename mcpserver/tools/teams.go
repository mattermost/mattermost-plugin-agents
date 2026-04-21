// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetTeamInfoArgs represents arguments for the get_team_info tool
type GetTeamInfoArgs struct {
	TeamID   string `json:"team_id,omitempty" jsonschema:"The exact team ID (fastest, most reliable method)"`
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
	UserID string `json:"user_id" jsonschema:"ID of the user to add"`
	TeamID string `json:"team_id" jsonschema:"ID of the team to add user to"`
}

// provideTeamTools registers all team-related MCP tools.
func (p *MattermostToolProvider) provideTeamTools(s *mcp.Server) {
	registerTool(s, p, "get_team_info",
		"Get information about a team. Provide team_id (fastest) or team_name (matches against both display name and URL name, case-insensitive, supports partial matches). Returns team metadata including ID, names, type, description, and member count. Example: {\"team_name\": \"Engineering\"} or {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\"}",
		NewJSONSchemaForAccessMode[GetTeamInfoArgs](string(p.accessMode)),
		p.toolGetTeamInfo,
		format.TeamInfoOutput,
	)
	registerTool(s, p, "get_team_members",
		"Get members of a team with pagination support. Parameters: team_id (required), limit (1-200, default 50), page (0+, default 0). Returns user details for each member including username, email, display name, and roles. Example: {\"team_id\": \"w1jkn9ebkiby7qezqfxk7o5ney\", \"limit\": 10, \"page\": 0}",
		NewJSONSchemaForAccessMode[GetTeamMembersArgs](string(p.accessMode)),
		p.toolGetTeamMembers,
		format.TeamMembersOutput,
	)
}

// provideDevTeamTools registers development team-related MCP tools.
func (p *MattermostToolProvider) provideDevTeamTools(s *mcp.Server) {
	registerTool(s, p, "create_team",
		"Create a new team (dev mode only)",
		NewJSONSchemaForAccessMode[CreateTeamArgs](string(p.accessMode)),
		p.toolCreateTeam,
		format.CreateTeamOutput,
	)
	registerTool(s, p, "add_user_to_team",
		"Add a user to a team (dev mode only)",
		NewJSONSchemaForAccessMode[AddUserToTeamArgs](string(p.accessMode)),
		p.toolAddUserToTeam,
		format.AddUserToTeamOutput,
	)
}

// toolGetTeamInfo implements the get_team_info tool
func (p *MattermostToolProvider) toolGetTeamInfo(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.TeamInfoOutput, error) {
	var args GetTeamInfoArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.TeamInfoOutput{}, fmt.Errorf("failed to get arguments for tool get_team_info: %w", err)
	}

	if mcpContext.Client == nil {
		return mcptool.TeamInfoOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	var team *model.Team

	switch {
	case args.TeamID != "":
		if !model.IsValidId(args.TeamID) {
			return mcptool.TeamInfoOutput{}, fmt.Errorf("invalid team_id format")
		}
		team, _, err = client.GetTeam(ctx, args.TeamID, "")
		if err != nil {
			return mcptool.TeamInfoOutput{}, fmt.Errorf("error fetching team by ID: %w", err)
		}

	case args.TeamName != "":
		var candidates []*model.Team
		team, candidates, err = p.resolveTeamByName(mcpContext, args.TeamName)
		if err != nil {
			return mcptool.TeamInfoOutput{}, err
		}
		switch {
		case team != nil:
			// unique match — fall through to the happy-path emission below
		case len(candidates) > 0:
			return mcptool.TeamInfoOutput{Teams: candidates}, nil
		default:
			return mcptool.TeamInfoOutput{}, fmt.Errorf("no team found matching '%s'. ACTION REQUIRED - try get_user_channels to list channels (includes team info) you have access to before asking the user", args.TeamName)
		}

	default:
		return mcptool.TeamInfoOutput{}, fmt.Errorf("either team_id or team_name must be provided")
	}

	var memberCount int64 = -1
	teamStats, _, err := client.GetTeamStats(ctx, team.Id, "")
	if err == nil {
		memberCount = teamStats.TotalMemberCount
	}

	return mcptool.TeamInfoOutput{
		Teams:       []*model.Team{team},
		MemberCount: memberCount,
	}, nil
}

// resolveTeamByName resolves a team by name using multiple strategies:
//  1. Exact display name match (case-insensitive) from user's teams
//  2. Exact URL name match from user's teams
//  3. Substring display name match from user's teams
//  4. SearchTeams API as final fallback
//
// Returns one of:
//   - (team, nil, nil)      — unique match
//   - (nil, candidates, nil) — multiple matches; caller renders disambiguation
//   - (nil, nil, nil)       — no matches found (not an error; caller renders not-found)
//   - (nil, nil, err)       — upstream/API error
func (p *MattermostToolProvider) resolveTeamByName(mcpContext *MCPToolContext, name string) (*model.Team, []*model.Team, error) {
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	user, _, userErr := client.GetMe(ctx, "")
	if userErr != nil {
		return nil, nil, fmt.Errorf("error getting current user: %w", userErr)
	}

	teams, _, teamsErr := client.GetTeamsForUser(ctx, user.Id, "")
	if teamsErr != nil {
		return nil, nil, fmt.Errorf("error fetching user teams: %w", teamsErr)
	}

	// 1. Exact display name match (case-insensitive)
	for _, t := range teams {
		if strings.EqualFold(t.DisplayName, name) {
			return t, nil, nil
		}
	}

	// 2. Exact URL name match
	for _, t := range teams {
		if strings.EqualFold(t.Name, name) {
			return t, nil, nil
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
		return substringMatches[0], nil, nil
	}
	if len(substringMatches) > 1 {
		return nil, substringMatches, nil
	}

	// 4. SearchTeams API as fallback for teams the user may not be a member of
	searchResults, _, searchErr := client.SearchTeams(ctx, &model.TeamSearch{Term: name})
	if searchErr == nil && len(searchResults) == 1 {
		return searchResults[0], nil, nil
	}
	if searchErr == nil && len(searchResults) > 1 {
		return nil, searchResults, nil
	}

	return nil, nil, nil
}

// toolGetTeamMembers implements the get_team_members tool
func (p *MattermostToolProvider) toolGetTeamMembers(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.TeamMembersOutput, error) {
	var args GetTeamMembersArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.TeamMembersOutput{}, fmt.Errorf("failed to get arguments for tool get_team_members: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.TeamID) {
		return mcptool.TeamMembersOutput{}, fmt.Errorf("team_id must be a valid ID")
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
		return mcptool.TeamMembersOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Default exclude_bots to true
	excludeBots := args.ExcludeBots == nil || *args.ExcludeBots

	// Get team members
	members, _, err := client.GetTeamMembers(ctx, args.TeamID, args.Page, args.Limit, "")
	if err != nil {
		return mcptool.TeamMembersOutput{}, fmt.Errorf("error fetching team members: %w", err)
	}

	if len(members) == 0 {
		return mcptool.TeamMembersOutput{
			Rows:        nil,
			Page:        args.Page,
			ExcludeBots: excludeBots,
		}, nil
	}

	rows := make([]mcptool.TeamMemberRow, 0, len(members))
	for _, member := range members {
		user, _, userErr := client.GetUser(ctx, member.UserId, "")
		if userErr != nil {
			p.logger.Warn("failed to get user details for member", "user_id", member.UserId, "error", userErr)
			rows = append(rows, mcptool.TeamMemberRow{
				User:        &model.User{Id: member.UserId, Username: "details unavailable"},
				SchemeAdmin: member.SchemeAdmin,
				SchemeGuest: member.SchemeGuest,
				SchemeUser:  member.SchemeUser,
			})
			continue
		}
		rows = append(rows, mcptool.TeamMemberRow{
			User:        user,
			SchemeAdmin: member.SchemeAdmin,
			SchemeGuest: member.SchemeGuest,
			SchemeUser:  member.SchemeUser,
		})
	}

	out := mcptool.TeamMembersOutput{
		Rows:        rows,
		Page:        args.Page,
		ExcludeBots: excludeBots,
	}
	return out, nil
}

// toolCreateTeam implements the create_team tool using the context client
func (p *MattermostToolProvider) toolCreateTeam(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.CreateTeamOutput, error) {
	var args CreateTeamArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("failed to get arguments for tool create_team: %w", err)
	}

	// Validate required fields
	if args.Name == "" {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("name cannot be empty")
	}
	if args.DisplayName == "" {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("display_name cannot be empty")
	}
	if args.Type == "" {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("type cannot be empty")
	}

	// Validate team type
	if args.Type != "O" && args.Type != "I" {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("invalid team type: %s", args.Type)
	}

	// Get client from context
	if mcpContext.Client == nil {
		return mcptool.CreateTeamOutput{}, fmt.Errorf("client not available in context")
	}
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
		return mcptool.CreateTeamOutput{}, fmt.Errorf("error creating team: %w", err)
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

	out := mcptool.CreateTeamOutput{
		Team:            createdTeam,
		TeamIconMessage: teamIconMessage,
	}
	return out, nil
}

// toolAddUserToTeam implements the add_user_to_team tool using the context client
func (p *MattermostToolProvider) toolAddUserToTeam(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (mcptool.AddUserToTeamOutput, error) {
	var args AddUserToTeamArgs
	err := argsGetter(&args)
	if err != nil {
		return mcptool.AddUserToTeamOutput{}, fmt.Errorf("failed to get arguments for tool add_user_to_team: %w", err)
	}

	// Validate required fields
	if !model.IsValidId(args.UserID) {
		return mcptool.AddUserToTeamOutput{}, fmt.Errorf("user_id must be a valid ID")
	}
	if !model.IsValidId(args.TeamID) {
		return mcptool.AddUserToTeamOutput{}, fmt.Errorf("team_id must be a valid ID")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return mcptool.AddUserToTeamOutput{}, fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	_, _, err = client.AddTeamMember(ctx, args.TeamID, args.UserID)
	if err != nil {
		return mcptool.AddUserToTeamOutput{}, fmt.Errorf("error adding user to team: %w", err)
	}

	user, _, userErr := client.GetUser(ctx, args.UserID, "")
	team, _, teamErr := client.GetTeam(ctx, args.TeamID, "")

	out := mcptool.AddUserToTeamOutput{
		UserID: args.UserID,
		TeamID: args.TeamID,
	}
	if userErr == nil {
		out.User = user
	}
	if teamErr == nil {
		out.Team = team
	}
	return out, nil
}
