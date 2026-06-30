// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// GetMeArgs represents arguments for the get_me tool.
type GetMeArgs struct{}

// GetUserArgs represents arguments for the get_user tool.
type GetUserArgs struct {
	UserID string `json:"user_id" jsonschema:"The ID of the user,minLength=26,maxLength=26"`
}

// GetUserByUsernameArgs represents arguments for the get_user_by_username tool.
type GetUserByUsernameArgs struct {
	Username string `json:"username" jsonschema:"The username (without @),minLength=1"`
}

// GetUserByEmailArgs represents arguments for the get_user_by_email tool.
type GetUserByEmailArgs struct {
	Email string `json:"email" jsonschema:"The user's email address,minLength=1"`
}

// GetUsersByIDsArgs represents arguments for the get_users_by_ids tool.
type GetUsersByIDsArgs struct {
	UserIDs []string `json:"user_ids" jsonschema:"The user IDs to fetch"`
}

// GetUsersByUsernamesArgs represents arguments for the get_users_by_usernames tool.
type GetUsersByUsernamesArgs struct {
	Usernames []string `json:"usernames" jsonschema:"The usernames (without @) to fetch"`
}

// GetUserStatsArgs represents arguments for the get_user_stats tool.
type GetUserStatsArgs struct{}

// GetUserCPAValuesArgs represents arguments for the get_user_cpa_values tool.
type GetUserCPAValuesArgs struct {
	UserID string `json:"user_id" jsonschema:"The user whose custom profile attribute (CPA) values to fetch,minLength=26,maxLength=26"`
}

// ListCPAFieldsArgs represents arguments for the list_cpa_fields tool.
type ListCPAFieldsArgs struct{}

// UpdateUserArgs represents arguments for the update_user tool.
type UpdateUserArgs struct {
	UserID    string  `json:"user_id,omitempty" jsonschema:"The user to update; omit for yourself,maxLength=26"`
	Nickname  *string `json:"nickname,omitempty" jsonschema:"New nickname (optional)"`
	FirstName *string `json:"first_name,omitempty" jsonschema:"New first name (optional)"`
	LastName  *string `json:"last_name,omitempty" jsonschema:"New last name (optional)"`
	Position  *string `json:"position,omitempty" jsonschema:"New position/title (optional)"`
}

const (
	getMeDescription               = "Get the authenticated agent/bot's own user profile (current user). No parameters."
	getUserDescription             = "Get a single user's full profile by ID. Parameters: user_id (required)."
	getUserByUsernameDescription   = "Get a user's profile by username. Parameters: username (required, without @)."
	getUserByEmailDescription      = "Get a user's profile by email. Parameters: email (required)."
	getUsersByIDsDescription       = "Batch-fetch user profiles by a list of IDs. Parameters: user_ids (required list)."
	getUsersByUsernamesDescription = "Batch-fetch user profiles by a list of usernames. Parameters: usernames (required list, without @)."
	getUserStatsDescription        = "Get the total user count on the server. No parameters."
	getUserCPAValuesDescription    = "Get a user's Custom Profile Attribute (CPA) values. Parameters: user_id (required)."
	listCPAFieldsDescription       = "List the server's Custom Profile Attribute (CPA) field definitions. No parameters."
	updateUserDescription          = "Update profile fields (nickname, first name, last name, position). Parameters: user_id (optional, defaults to you), plus any of nickname, first_name, last_name, position."
)

// getUserTools returns the user profile read/update tools.
func (p *MattermostToolProvider) getUserTools() []MCPTool {
	return []MCPTool{
		{Name: "get_me", Description: getMeDescription, Schema: NewJSONSchemaForAccessMode[GetMeArgs](string(p.accessMode)), Resolver: typed("get_me", p.toolGetMe)},
		{Name: "get_user", Description: getUserDescription, Schema: NewJSONSchemaForAccessMode[GetUserArgs](string(p.accessMode)), Resolver: typed("get_user", p.toolGetUser)},
		{Name: "get_user_by_username", Description: getUserByUsernameDescription, Schema: NewJSONSchemaForAccessMode[GetUserByUsernameArgs](string(p.accessMode)), Resolver: typed("get_user_by_username", p.toolGetUserByUsername)},
		{Name: "get_user_by_email", Description: getUserByEmailDescription, Schema: NewJSONSchemaForAccessMode[GetUserByEmailArgs](string(p.accessMode)), Resolver: typed("get_user_by_email", p.toolGetUserByEmail)},
		{Name: "get_users_by_ids", Description: getUsersByIDsDescription, Schema: NewJSONSchemaForAccessMode[GetUsersByIDsArgs](string(p.accessMode)), Resolver: typed("get_users_by_ids", p.toolGetUsersByIDs)},
		{Name: "get_users_by_usernames", Description: getUsersByUsernamesDescription, Schema: NewJSONSchemaForAccessMode[GetUsersByUsernamesArgs](string(p.accessMode)), Resolver: typed("get_users_by_usernames", p.toolGetUsersByUsernames)},
		{Name: "get_user_stats", Description: getUserStatsDescription, Schema: NewJSONSchemaForAccessMode[GetUserStatsArgs](string(p.accessMode)), Resolver: typed("get_user_stats", p.toolGetUserStats)},
		{Name: "get_user_cpa_values", Description: getUserCPAValuesDescription, Schema: NewJSONSchemaForAccessMode[GetUserCPAValuesArgs](string(p.accessMode)), Resolver: typed("get_user_cpa_values", p.toolGetUserCPAValues)},
		{Name: "list_cpa_fields", Description: listCPAFieldsDescription, Schema: NewJSONSchemaForAccessMode[ListCPAFieldsArgs](string(p.accessMode)), Resolver: typed("list_cpa_fields", p.toolListCPAFields)},
		{Name: "update_user", Description: updateUserDescription, Schema: NewJSONSchemaForAccessMode[UpdateUserArgs](string(p.accessMode)), Resolver: typed("update_user", p.toolUpdateUser)},
	}
}

// toolGetMe implements the get_me tool.
func (p *MattermostToolProvider) toolGetMe(mcpContext *MCPToolContext, _ GetMeArgs) (string, error) {
	user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, "")
	if err != nil {
		return "", fmt.Errorf("error fetching current user: %w", err)
	}
	return formatSingleUser(user), nil
}

// toolGetUser implements the get_user tool.
func (p *MattermostToolProvider) toolGetUser(mcpContext *MCPToolContext, args GetUserArgs) (string, error) {
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	user, _, err := mcpContext.Client.GetUser(mcpContext.Ctx, args.UserID, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user: %w", err)
	}
	return formatSingleUser(user), nil
}

// toolGetUserByUsername implements the get_user_by_username tool.
func (p *MattermostToolProvider) toolGetUserByUsername(mcpContext *MCPToolContext, args GetUserByUsernameArgs) (string, error) {
	username := strings.TrimPrefix(strings.TrimSpace(args.Username), "@")
	if username == "" {
		return "", fmt.Errorf("username cannot be empty")
	}
	user, _, err := mcpContext.Client.GetUserByUsername(mcpContext.Ctx, username, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user by username: %w", err)
	}
	return formatSingleUser(user), nil
}

// toolGetUserByEmail implements the get_user_by_email tool.
func (p *MattermostToolProvider) toolGetUserByEmail(mcpContext *MCPToolContext, args GetUserByEmailArgs) (string, error) {
	if strings.TrimSpace(args.Email) == "" {
		return "", fmt.Errorf("email cannot be empty")
	}
	user, _, err := mcpContext.Client.GetUserByEmail(mcpContext.Ctx, args.Email, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user by email: %w", err)
	}
	return formatSingleUser(user), nil
}

// toolGetUsersByIDs implements the get_users_by_ids tool.
func (p *MattermostToolProvider) toolGetUsersByIDs(mcpContext *MCPToolContext, args GetUsersByIDsArgs) (string, error) {
	if len(args.UserIDs) == 0 {
		return "", fmt.Errorf("user_ids cannot be empty")
	}
	for _, id := range args.UserIDs {
		if err := requireID("user_ids", id); err != nil {
			return "", err
		}
	}
	users, _, err := mcpContext.Client.GetUsersByIds(mcpContext.Ctx, args.UserIDs)
	if err != nil {
		return "", fmt.Errorf("error fetching users: %w", err)
	}
	return p.formatUserList(users, "users", false), nil
}

// toolGetUsersByUsernames implements the get_users_by_usernames tool.
func (p *MattermostToolProvider) toolGetUsersByUsernames(mcpContext *MCPToolContext, args GetUsersByUsernamesArgs) (string, error) {
	if len(args.Usernames) == 0 {
		return "", fmt.Errorf("usernames cannot be empty")
	}
	cleaned := make([]string, 0, len(args.Usernames))
	for _, u := range args.Usernames {
		cleaned = append(cleaned, strings.TrimPrefix(strings.TrimSpace(u), "@"))
	}
	users, _, err := mcpContext.Client.GetUsersByUsernames(mcpContext.Ctx, cleaned)
	if err != nil {
		return "", fmt.Errorf("error fetching users: %w", err)
	}
	return p.formatUserList(users, "users", false), nil
}

// toolGetUserStats implements the get_user_stats tool.
func (p *MattermostToolProvider) toolGetUserStats(mcpContext *MCPToolContext, _ GetUserStatsArgs) (string, error) {
	stats, _, err := mcpContext.Client.GetTotalUsersStats(mcpContext.Ctx, "")
	if err != nil {
		return "", fmt.Errorf("error fetching user stats: %w", err)
	}
	return fmt.Sprintf("Total users: %d", stats.TotalUsersCount), nil
}

// toolGetUserCPAValues implements the get_user_cpa_values tool.
func (p *MattermostToolProvider) toolGetUserCPAValues(mcpContext *MCPToolContext, args GetUserCPAValuesArgs) (string, error) {
	if err := requireID("user_id", args.UserID); err != nil {
		return "", err
	}
	values, _, err := mcpContext.Client.ListCPAValues(mcpContext.Ctx, args.UserID)
	if err != nil {
		return "", fmt.Errorf("error fetching custom profile attribute values: %w", err)
	}
	if len(values) == 0 {
		return "no custom profile attribute (CPA) values set for this user", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Custom Profile Attribute (CPA) values for user %s:\n", args.UserID))
	for fieldID, raw := range values {
		result.WriteString(fmt.Sprintf("%s: %s\n", fieldID, string(raw)))
	}
	return result.String(), nil
}

// toolListCPAFields implements the list_cpa_fields tool.
func (p *MattermostToolProvider) toolListCPAFields(mcpContext *MCPToolContext, _ ListCPAFieldsArgs) (string, error) {
	fields, _, err := mcpContext.Client.ListCPAFields(mcpContext.Ctx)
	if err != nil {
		return "", fmt.Errorf("error fetching custom profile attribute fields: %w", err)
	}
	if len(fields) == 0 {
		return "no custom profile attribute (CPA) fields defined", nil
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Custom Profile Attribute (CPA) fields (%d):\n\n", len(fields)))
	for i, field := range fields {
		format.WriteCPAField(&result, format.CPAFieldEntry{
			HeaderLabel: fmt.Sprintf("Field %d", i+1),
			Field:       field,
		})
	}
	return result.String(), nil
}

// toolUpdateUser implements the update_user tool.
func (p *MattermostToolProvider) toolUpdateUser(mcpContext *MCPToolContext, args UpdateUserArgs) (string, error) {
	if err := optionalID("user_id", args.UserID); err != nil {
		return "", err
	}
	if args.Nickname == nil && args.FirstName == nil && args.LastName == nil && args.Position == nil {
		return "", fmt.Errorf("provide at least one of nickname, first_name, last_name, position to update")
	}

	userID := args.UserID
	if userID == "" {
		resolved, err := p.resolveUserID(mcpContext)
		if err != nil {
			return "", err
		}
		userID = resolved
	}

	patch := &model.UserPatch{
		Nickname:  args.Nickname,
		FirstName: args.FirstName,
		LastName:  args.LastName,
		Position:  args.Position,
	}
	updated, _, err := mcpContext.Client.PatchUser(mcpContext.Ctx, userID, patch)
	if err != nil {
		return "", fmt.Errorf("error updating user: %w", err)
	}
	return fmt.Sprintf("Successfully updated user '%s' (ID: %s)", updated.Username, updated.Id), nil
}

// formatSingleUser renders a single user profile.
func formatSingleUser(user *model.User) string {
	var result strings.Builder
	format.WriteUser(&result, format.UserEntry{HeaderLabel: "User", User: user})
	return result.String()
}

// CreateUserArgs represents arguments for the create_user tool (dev mode only)
type CreateUserArgs struct {
	Username     string `json:"username" jsonschema:"Username for the new user"`
	Email        string `json:"email" jsonschema:"Email address for the new user"`
	Password     string `json:"password" jsonschema:"Password for the new user"`
	FirstName    string `json:"first_name" jsonschema:"First name of the user"`
	LastName     string `json:"last_name" jsonschema:"Last name of the user"`
	Nickname     string `json:"nickname" jsonschema:"Nickname for the user"`
	ProfileImage string `json:"profile_image,omitempty" access:"local" jsonschema:"Optional file path or URL to profile image (supports .jpeg, .jpg, .png, .gif)"`
}

// getDevUserTools returns development user-related tools for MCP
func (p *MattermostToolProvider) getDevUserTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "create_user",
			Description: "Create a new user account (dev mode only)",
			Schema:      NewJSONSchemaForAccessMode[CreateUserArgs](string(p.accessMode)),
			Resolver:    typed("create_user", p.toolCreateUser),
		},
	}
}

// toolCreateUser implements the create_user tool using the context client
func (p *MattermostToolProvider) toolCreateUser(mcpContext *MCPToolContext, args CreateUserArgs) (string, error) {
	// Validate required fields
	if args.Username == "" {
		return "", fmt.Errorf("username cannot be empty")
	}
	if args.Email == "" {
		return "", fmt.Errorf("email cannot be empty")
	}
	if args.Password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	// Get client from context
	client := mcpContext.Client
	ctx := mcpContext.Ctx

	// Create the user
	user := &model.User{
		Username:  args.Username,
		Email:     args.Email,
		Password:  args.Password,
		FirstName: args.FirstName,
		LastName:  args.LastName,
		Nickname:  args.Nickname,
	}

	createdUser, _, err := client.CreateUser(ctx, user)
	if err != nil {
		return "", fmt.Errorf("error creating user: %w", err)
	}

	var profileImageMessage string
	// Upload profile image if specified
	if args.ProfileImage != "" {
		// Validate image file type
		fileName := extractFileNameForLocal(args.ProfileImage, mcpContext.AccessMode)
		if !isValidImageFile(fileName) {
			profileImageMessage = " (profile image upload failed: unsupported file type, only .jpeg, .jpg, .png, .gif are supported)"
		} else {
			imageData, err := fetchFileDataForLocal(mcpContext.Ctx, args.ProfileImage, mcpContext.AccessMode)
			if err != nil {
				profileImageMessage = fmt.Sprintf(" (profile image upload failed: %v)", err)
			} else {
				_, err = client.SetProfileImage(ctx, createdUser.Id, imageData)
				if err != nil {
					profileImageMessage = fmt.Sprintf(" (profile image upload failed: %v)", err)
				} else {
					profileImageMessage = " (profile image uploaded successfully)"
				}
			}
		}
	}

	return fmt.Sprintf("Successfully created user '%s' with ID: %s%s", createdUser.Username, createdUser.Id, profileImageMessage), nil
}
