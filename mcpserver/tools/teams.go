// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
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

// AddTeamMemberArgs represents arguments for the add_team_member tool
type AddTeamMemberArgs struct {
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
		mcpTool(p, "get_team_info", getTeamInfoDescription, p.toolGetTeamInfo),
		mcpTool(p, "get_team_members", getTeamMembersDescription, p.toolGetTeamMembers),
		mcpTool(p, "add_team_member", "Add a user to a team (team membership). Parameters: user_id (required), team_id (required). Returns confirmation message.", p.toolAddTeamMember),
		mcpTool(p, "get_team_member", getTeamMemberDescription, p.toolGetTeamMember),
		mcpTool(p, "get_team_stats", getTeamStatsDescription, p.toolGetTeamStats),
		mcpTool(p, "get_user_teams", getUserTeamsDescription, p.toolGetUserTeams),
		mcpTool(p, "get_users_in_team", getUsersInTeamDescription, p.toolGetUsersInTeam),
		mcpTool(p, "get_users_not_in_team", getUsersNotInTeamDescription, p.toolGetUsersNotInTeam),
		mcpTool(p, "get_new_users_in_team", getNewUsersInTeamDescription, p.toolGetNewUsersInTeam),
		mcpTool(p, "get_dm_common_teams", getDMCommonTeamsDescription, p.toolGetDMCommonTeams),
		mcpTool(p, "search_teams", searchTeamsDescription, p.toolSearchTeams),
		mcpTool(p, "search_users_in_team", searchUsersInTeamDescription, p.toolSearchUsersInTeam),
		mcpTool(p, "add_team_members", addTeamMembersDescription, p.toolAddTeamMembers),
		mcpTool(p, "remove_team_member", removeTeamMemberDescription, p.toolRemoveTeamMember),
		mcpTool(p, "update_team", updateTeamDescription, p.toolUpdateTeam),
		mcpTool(p, "invite_users_to_team", inviteUsersToTeamDescription, p.toolInviteUsersToTeam),
		mcpTool(p, "invite_users_to_team_and_channels", inviteUsersToTeamAndChannelsDescription, p.toolInviteUsersToTeamAndChannels),
	}
}

// getDevTeamTools returns development team-related tools for MCP
func (p *MattermostToolProvider) getDevTeamTools() []MCPTool {
	return []MCPTool{
		mcpTool(p, "create_team", "Create a new team (dev mode only)", p.toolCreateTeam),
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
	fmt.Fprintf(&msg, "Multiple teams match '%s'. Please specify which one by calling get_team_info with team_id:\n\n", searchTerm)
	for _, t := range teams {
		fmt.Fprintf(&msg, "- '%s' (URL name: %s, ID: %s)\n", t.DisplayName, t.Name, t.Id)
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

	return p.renderMembers(ctx, client, "Team Members", args.Page, teamRenderMembers(members), excludeBots), nil
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

// toolAddTeamMember implements the add_team_member tool using the context client
func (p *MattermostToolProvider) toolAddTeamMember(mcpContext *MCPToolContext, args AddTeamMemberArgs) (string, error) {
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

// --- Additional team tools (members, stats, search, invites) ---

// GetTeamMemberArgs represents arguments for the get_team_member tool.
type GetTeamMemberArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	UserID string `json:"user_id,omitempty" jsonschema:"The user; omit for yourself,maxLength=26"`
}

// GetTeamStatsArgs represents arguments for the get_team_stats tool.
type GetTeamStatsArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
}

// GetUserTeamsArgs represents arguments for the get_user_teams tool.
type GetUserTeamsArgs struct {
	UserID string `json:"user_id,omitempty" jsonschema:"The user; omit for yourself,maxLength=26"`
}

// GetUsersInTeamArgs represents arguments for the get_users_in_team tool.
type GetUsersInTeamArgs struct {
	TeamID  string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Users per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// GetUsersNotInTeamArgs represents arguments for the get_users_not_in_team tool.
type GetUsersNotInTeamArgs struct {
	TeamID  string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Users per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// GetNewUsersInTeamArgs represents arguments for the get_new_users_in_team tool.
type GetNewUsersInTeamArgs struct {
	TeamID  string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (default: 0),minimum=0"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Users per page (default: 50, max: 200),minimum=1,maximum=200"`
}

// GetDMCommonTeamsArgs represents arguments for the get_dm_common_teams tool.
type GetDMCommonTeamsArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The direct message or group message channel,minLength=26,maxLength=26"`
}

// SearchTeamsArgs represents arguments for the search_teams tool.
type SearchTeamsArgs struct {
	Term string `json:"term" jsonschema:"Search term to match team name or display name,minLength=1,maxLength=64"`
}

// SearchUsersInTeamArgs represents arguments for the search_users_in_team tool.
type SearchUsersInTeamArgs struct {
	Term   string `json:"term" jsonschema:"Search term,minLength=1,maxLength=64"`
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	NotIn  bool   `json:"not_in,omitempty" jsonschema:"If true, search users NOT in the team instead of in it"`
}

// AddTeamMembersArgs represents arguments for the add_team_members tool.
type AddTeamMembersArgs struct {
	TeamID  string   `json:"team_id" jsonschema:"The team to add users to,minLength=26,maxLength=26"`
	UserIDs []string `json:"user_ids" jsonschema:"The user IDs to add"`
}

// RemoveTeamMemberArgs represents arguments for the remove_team_member tool.
type RemoveTeamMemberArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team,minLength=26,maxLength=26"`
	UserID string `json:"user_id" jsonschema:"The user to remove,minLength=26,maxLength=26"`
}

// UpdateTeamArgs represents arguments for the update_team tool.
type UpdateTeamArgs struct {
	TeamID          string  `json:"team_id" jsonschema:"The team to update,minLength=26,maxLength=26"`
	DisplayName     *string `json:"display_name,omitempty" jsonschema:"New display name (optional)"`
	Description     *string `json:"description,omitempty" jsonschema:"New description (optional)"`
	AllowOpenInvite *bool   `json:"allow_open_invite,omitempty" jsonschema:"Whether anyone can join without an invite (optional)"`
}

// InviteUsersToTeamArgs represents arguments for the invite_users_to_team tool.
type InviteUsersToTeamArgs struct {
	TeamID string   `json:"team_id" jsonschema:"The team to invite users to,minLength=26,maxLength=26"`
	Emails []string `json:"emails" jsonschema:"The email addresses to invite"`
}

// InviteUsersToTeamAndChannelsArgs represents arguments for the invite_users_to_team_and_channels tool.
type InviteUsersToTeamAndChannelsArgs struct {
	TeamID     string   `json:"team_id" jsonschema:"The team to invite users to,minLength=26,maxLength=26"`
	Emails     []string `json:"emails" jsonschema:"The email addresses to invite"`
	ChannelIDs []string `json:"channel_ids" jsonschema:"The channel IDs to add the invited users to"`
	Message    string   `json:"message,omitempty" jsonschema:"Optional custom invite message"`
}

const (
	getTeamMemberDescription                = "Get a user's team membership record (roles). Parameters: team_id (required), user_id (optional, defaults to you)."
	getTeamStatsDescription                 = "Get a team's member counts (total and active). Parameters: team_id (required)."
	getUserTeamsDescription                 = "Get the teams a user belongs to. Parameters: user_id (optional, defaults to you)."
	getUsersInTeamDescription               = "Get the member profiles of a team. Parameters: team_id (required), page, per_page."
	getUsersNotInTeamDescription            = "Get users who are not members of a team. Parameters: team_id (required), page, per_page."
	getNewUsersInTeamDescription            = "Get the most recently joined members of a team. Parameters: team_id (required), page, per_page."
	getDMCommonTeamsDescription             = "Get teams shared by all members of a direct message (DM) or group message (GM) channel. Parameters: channel_id (required)."
	searchTeamsDescription                  = "Search teams by name or display name. Parameters: term (required)."
	searchUsersInTeamDescription            = "Search for users within (or excluding) a team. Parameters: term (required), team_id (required), not_in (optional)."
	addTeamMembersDescription               = "Add several users to a team (bulk). Parameters: team_id (required), user_ids (required list)."
	removeTeamMemberDescription             = "Remove a user from a team. Parameters: team_id (required), user_id (required)."
	updateTeamDescription                   = "Update team fields (display name, description, open-invite setting). Parameters: team_id (required), plus any of display_name, description, allow_open_invite."
	inviteUsersToTeamDescription            = "Email-invite users to a team. Parameters: team_id (required), emails (required list)."
	inviteUsersToTeamAndChannelsDescription = "Email-invite users to a team and specific channels. Parameters: team_id (required), emails (required list), channel_ids (required list), message (optional)."
)

// toolGetTeamMember implements the get_team_member tool.
func (p *MattermostToolProvider) toolGetTeamMember(mcpContext *MCPToolContext, args GetTeamMemberArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := optionalID("user_id", args.UserID); err != nil {
		return "", err
	}
	userID, err := p.resolveUserIDOrDefault(mcpContext, args.UserID)
	if err != nil {
		return "", err
	}

	member, _, err := mcpContext.Client.GetTeamMember(mcpContext.Ctx, args.TeamID, userID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching team member: %w", err)
	}

	var result strings.Builder
	format.WriteTeamMember(&result, format.TeamMemberEntry{
		Member:   member,
		Username: p.usernameFor(mcpContext.Ctx, mcpContext.Client, member.UserId),
	})
	return result.String(), nil
}

// toolGetTeamStats implements the get_team_stats tool.
func (p *MattermostToolProvider) toolGetTeamStats(mcpContext *MCPToolContext, args GetTeamStatsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	stats, _, err := mcpContext.Client.GetTeamStats(mcpContext.Ctx, args.TeamID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching team stats: %w", err)
	}
	var result strings.Builder
	format.WriteTeamStats(&result, stats)
	return result.String(), nil
}

// toolGetUserTeams implements the get_user_teams tool.
func (p *MattermostToolProvider) toolGetUserTeams(mcpContext *MCPToolContext, args GetUserTeamsArgs) (string, error) {
	if err := optionalID("user_id", args.UserID); err != nil {
		return "", err
	}
	userID, err := p.resolveUserIDOrDefault(mcpContext, args.UserID)
	if err != nil {
		return "", err
	}

	teams, _, err := mcpContext.Client.GetTeamsForUser(mcpContext.Ctx, userID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user teams: %w", err)
	}
	return formatTeamList(teams), nil
}

// toolGetUsersInTeam implements the get_users_in_team tool.
func (p *MattermostToolProvider) toolGetUsersInTeam(mcpContext *MCPToolContext, args GetUsersInTeamArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	users, _, err := mcpContext.Client.GetUsersInTeam(mcpContext.Ctx, args.TeamID, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching team users: %w", err)
	}
	return p.formatUserList(users, fmt.Sprintf("users in team (page %d)", page), false), nil
}

// toolGetUsersNotInTeam implements the get_users_not_in_team tool.
func (p *MattermostToolProvider) toolGetUsersNotInTeam(mcpContext *MCPToolContext, args GetUsersNotInTeamArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	users, _, err := mcpContext.Client.GetUsersNotInTeam(mcpContext.Ctx, args.TeamID, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching users not in team: %w", err)
	}
	return p.formatUserList(users, fmt.Sprintf("users not in team (page %d)", page), false), nil
}

// toolGetNewUsersInTeam implements the get_new_users_in_team tool.
func (p *MattermostToolProvider) toolGetNewUsersInTeam(mcpContext *MCPToolContext, args GetNewUsersInTeamArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	page, perPage := normalizePage(args.Page, args.PerPage, 50, 200)
	users, _, err := mcpContext.Client.GetNewUsersInTeam(mcpContext.Ctx, args.TeamID, page, perPage, "")
	if err != nil {
		return "", fmt.Errorf("error fetching new users in team: %w", err)
	}
	return p.formatUserList(users, fmt.Sprintf("recently joined users (page %d)", page), false), nil
}

// toolGetDMCommonTeams implements the get_dm_common_teams tool. There is no direct
// endpoint; it intersects the team memberships of all channel members.
func (p *MattermostToolProvider) toolGetDMCommonTeams(mcpContext *MCPToolContext, args GetDMCommonTeamsArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}

	client := mcpContext.Client
	ctx := mcpContext.Ctx

	members, _, err := client.GetChannelMembers(ctx, args.ChannelID, 0, 200, "")
	if err != nil {
		return "", fmt.Errorf("error fetching channel members: %w", err)
	}
	if len(members) == 0 {
		return "no members found in this channel", nil
	}

	// Intersect each member's teams.
	var common map[string]*model.Team
	for _, member := range members {
		teams, _, teamsErr := client.GetTeamsForUser(ctx, member.UserId, "")
		if teamsErr != nil {
			return "", fmt.Errorf("error fetching teams for user %s: %w", member.UserId, teamsErr)
		}
		teamSet := make(map[string]*model.Team, len(teams))
		for _, team := range teams {
			teamSet[team.Id] = team
		}
		if common == nil {
			common = teamSet
			continue
		}
		for id := range common {
			if _, ok := teamSet[id]; !ok {
				delete(common, id)
			}
		}
	}

	if len(common) == 0 {
		return "the members of this conversation have no teams in common", nil
	}

	teams := make([]*model.Team, 0, len(common))
	for _, team := range common {
		teams = append(teams, team)
	}
	return formatTeamList(teams), nil
}

// toolSearchTeams implements the search_teams tool.
func (p *MattermostToolProvider) toolSearchTeams(mcpContext *MCPToolContext, args SearchTeamsArgs) (string, error) {
	if args.Term == "" {
		return "", fmt.Errorf("term cannot be empty")
	}
	teams, _, err := mcpContext.Client.SearchTeams(mcpContext.Ctx, &model.TeamSearch{Term: args.Term})
	if err != nil {
		return "", fmt.Errorf("error searching teams: %w", err)
	}
	return formatTeamList(teams), nil
}

// toolSearchUsersInTeam implements the search_users_in_team tool.
func (p *MattermostToolProvider) toolSearchUsersInTeam(mcpContext *MCPToolContext, args SearchUsersInTeamArgs) (string, error) {
	if args.Term == "" {
		return "", fmt.Errorf("term cannot be empty")
	}
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	search := &model.UserSearch{Term: args.Term, Limit: 100}
	if args.NotIn {
		search.NotInTeamId = args.TeamID
	} else {
		search.TeamId = args.TeamID
	}

	users, _, err := mcpContext.Client.SearchUsers(mcpContext.Ctx, search)
	if err != nil {
		return "", fmt.Errorf("error searching users in team: %w", err)
	}
	return p.formatUserList(users, fmt.Sprintf("users matching '%s'", args.Term), false), nil
}

// toolAddTeamMembers implements the add_team_members tool.
func (p *MattermostToolProvider) toolAddTeamMembers(mcpContext *MCPToolContext, args AddTeamMembersArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireIDs("user_ids", args.UserIDs); err != nil {
		return "", err
	}

	// Use the graceful path so one invalid or already-a-member user does not
	// abort the whole batch; per-user outcomes are surfaced to the caller.
	members, _, err := mcpContext.Client.AddTeamMembersGracefully(mcpContext.Ctx, args.TeamID, args.UserIDs)
	if err != nil {
		return "", fmt.Errorf("error adding team members: %w", err)
	}
	return formatTeamMemberAddResults(args.TeamID, members), nil
}

// toolRemoveTeamMember implements the remove_team_member tool.
func (p *MattermostToolProvider) toolRemoveTeamMember(mcpContext *MCPToolContext, args RemoveTeamMemberArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	if _, err := mcpContext.Client.RemoveTeamMember(mcpContext.Ctx, args.TeamID, args.UserID); err != nil {
		return "", fmt.Errorf("error removing team member: %w", err)
	}
	return fmt.Sprintf("Successfully removed user %s from team %s", args.UserID, args.TeamID), nil
}

// toolUpdateTeam implements the update_team tool.
func (p *MattermostToolProvider) toolUpdateTeam(mcpContext *MCPToolContext, args UpdateTeamArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if args.DisplayName == nil && args.Description == nil && args.AllowOpenInvite == nil {
		return "", fmt.Errorf("provide at least one of display_name, description, allow_open_invite to update")
	}

	patch := &model.TeamPatch{
		DisplayName:     args.DisplayName,
		Description:     args.Description,
		AllowOpenInvite: args.AllowOpenInvite,
	}
	updated, _, err := mcpContext.Client.PatchTeam(mcpContext.Ctx, args.TeamID, patch)
	if err != nil {
		return "", fmt.Errorf("error updating team: %w", err)
	}
	return fmt.Sprintf("Successfully updated team '%s' (ID: %s)", updated.DisplayName, updated.Id), nil
}

// toolInviteUsersToTeam implements the invite_users_to_team tool.
func (p *MattermostToolProvider) toolInviteUsersToTeam(mcpContext *MCPToolContext, args InviteUsersToTeamArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if len(args.Emails) == 0 {
		return "", fmt.Errorf("emails cannot be empty")
	}

	invites, _, err := mcpContext.Client.InviteUsersToTeamGracefully(mcpContext.Ctx, args.TeamID, args.Emails)
	if err != nil {
		return "", fmt.Errorf("error inviting users to team: %w", err)
	}
	return formatInviteResults(invites), nil
}

// toolInviteUsersToTeamAndChannels implements the invite_users_to_team_and_channels tool.
func (p *MattermostToolProvider) toolInviteUsersToTeamAndChannels(mcpContext *MCPToolContext, args InviteUsersToTeamAndChannelsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}
	if len(args.Emails) == 0 {
		return "", fmt.Errorf("emails cannot be empty")
	}
	if err := requireIDs("channel_ids", args.ChannelIDs); err != nil {
		return "", err
	}

	invites, _, err := mcpContext.Client.InviteUsersToTeamAndChannelsGracefully(mcpContext.Ctx, args.TeamID, args.Emails, args.ChannelIDs, args.Message)
	if err != nil {
		return "", fmt.Errorf("error inviting users to team and channels: %w", err)
	}
	return formatInviteResults(invites), nil
}

// formatTeamList renders a slice of teams.
func formatTeamList(teams []*model.Team) string {
	if len(teams) == 0 {
		return "no teams found"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "Found %d team(s):\n\n", len(teams))
	for _, team := range teams {
		format.WriteTeam(&result, format.TeamEntry{Team: team, MemberCount: -1})
		result.WriteString("\n")
	}
	return result.String()
}

// formatTeamMemberAddResults summarizes the per-user outcomes of a graceful bulk add.
func formatTeamMemberAddResults(teamID string, members []*model.TeamMemberWithError) string {
	if len(members) == 0 {
		return fmt.Sprintf("no users added to team %s", teamID)
	}
	var result strings.Builder
	fmt.Fprintf(&result, "Add team member results for team %s (%d):\n", teamID, len(members))
	for _, member := range members {
		if member.Error != nil {
			fmt.Fprintf(&result, "- %s: failed (%s)\n", member.UserId, member.Error.Message)
		} else {
			fmt.Fprintf(&result, "- %s: added\n", member.UserId)
		}
	}
	return result.String()
}

// formatInviteResults summarizes the per-email invite outcomes.
func formatInviteResults(invites []*model.EmailInviteWithError) string {
	if len(invites) == 0 {
		return "invitations sent"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "Invitation results (%d):\n", len(invites))
	for _, invite := range invites {
		if invite.Error != nil {
			fmt.Fprintf(&result, "- %s: failed (%s)\n", invite.Email, invite.Error.Message)
		} else {
			fmt.Fprintf(&result, "- %s: invited\n", invite.Email)
		}
	}
	return result.String()
}
