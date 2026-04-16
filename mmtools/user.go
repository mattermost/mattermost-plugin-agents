// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

type LookupMattermostUserArgs struct {
	Username string `jsonschema_description:"The username of the user to lookup without a leading '@'. Example: 'firstname.lastname'"`
}

func (p *MMToolProvider) toolResolveLookupMattermostUser(context *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args LookupMattermostUserArgs
	err := argsGetter(&args)
	if err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for tool LookupMattermostUser: %w", err)
	}

	if !model.IsValidUsername(args.Username) {
		return "invalid username", errors.New("invalid username")
	}

	// Check permissions
	if !p.pluginAPI.HasPermissionTo(context.RequestingUser.Id, model.PermissionViewMembers) {
		return "user doesn't have permissions", errors.New("user doesn't have permission to lookup users")
	}

	user, err := p.pluginAPI.GetUserByUsername(args.Username)
	if err != nil {
		return "user not found", nil
	}

	userStatus, err := p.pluginAPI.GetUserStatus(user.Id)
	if err != nil {
		return "failed to lookup user", fmt.Errorf("failed to get user status: %w", err)
	}

	// Build result based on privacy settings
	config := p.pluginAPI.GetConfig()
	result := fmt.Sprintf("Username: %s", user.Username)

	if config.PrivacySettings.ShowFullName != nil && *config.PrivacySettings.ShowFullName {
		if user.FirstName != "" || user.LastName != "" {
			result += fmt.Sprintf("\nFull Name: %s %s", user.FirstName, user.LastName)
		}
	}

	if config.PrivacySettings.ShowEmailAddress != nil && *config.PrivacySettings.ShowEmailAddress {
		result += fmt.Sprintf("\nEmail: %s", user.Email)
	}

	if user.Nickname != "" {
		result += fmt.Sprintf("\nNickname: %s", user.Nickname)
	}
	if user.Position != "" {
		result += fmt.Sprintf("\nPosition: %s", user.Position)
	}
	if user.Locale != "" {
		result += fmt.Sprintf("\nLocale: %s", user.Locale)
	}

	result += fmt.Sprintf("\nTimezone: %s", model.GetPreferredTimezone(user.Timezone))
	result += fmt.Sprintf("\nLast Activity: %s", model.GetTimeForMillis(userStatus.LastActivityAt).Format("2006-01-02 15:04:05 MST"))

	// Exclude manual statuses because they could be prompt injections
	if userStatus.Status != "" && !userStatus.Manual {
		result += fmt.Sprintf("\nStatus: %s", userStatus.Status)
	}

	return result, nil
}
