// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// CreateUserArgs represents arguments for the create_user tool (dev mode only)
type CreateUserArgs struct {
	Username     string `json:"username" jsonschema_description:"Username for the new user"`
	Email        string `json:"email" jsonschema_description:"Email address for the new user"`
	Password     string `json:"password" jsonschema_description:"Password for the new user"`
	FirstName    string `json:"first_name" jsonschema_description:"First name of the user"`
	LastName     string `json:"last_name" jsonschema_description:"Last name of the user"`
	Nickname     string `json:"nickname" jsonschema_description:"Nickname for the user"`
	ProfileImage string `json:"profile_image,omitempty" jsonschema_description:"Optional file path or URL to profile image (supports .jpeg, .jpg, .png, .gif)"`
}

// getDevUserTools returns development user-related tools for MCP
func (p *MattermostToolProvider) getDevUserTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "create_user",
			Description: "Create a new user account (dev mode only)",
			Schema:      llm.NewJSONSchemaFromStruct[CreateUserArgs](),
			Resolver:    p.toolCreateUser,
		},
	}
}

// toolCreateUser implements the create_user tool using the context client
func (p *MattermostToolProvider) toolCreateUser(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args CreateUserArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool create_user: %w", err)
	}

	// Validate required fields
	if args.Username == "" {
		return "username is required", fmt.Errorf("username cannot be empty")
	}
	if args.Email == "" {
		return "email is required", fmt.Errorf("email cannot be empty")
	}
	if args.Password == "" {
		return "password is required", fmt.Errorf("password cannot be empty")
	}

	// Get client from context
	if mcpContext.Client == nil {
		return "client not available", fmt.Errorf("client not available in context")
	}
	client := mcpContext.Client
	ctx := context.Background()

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
		return "failed to create user", fmt.Errorf("error creating user: %w", err)
	}

	var profileImageMessage string
	// Upload profile image if specified
	if args.ProfileImage != "" {
		// Validate image file type
		fileName := getFileNameFromSpec(args.ProfileImage)
		if !isValidImageFile(fileName) {
			profileImageMessage = " (profile image upload failed: unsupported file type, only .jpeg, .jpg, .png, .gif are supported)"
		} else {
			imageData, err := fetchFileData(args.ProfileImage)
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
