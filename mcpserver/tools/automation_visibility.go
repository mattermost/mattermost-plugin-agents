// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// automationListVisibility applies automation tools/list visibility rules from resolved facts.
// For open/private channels, openPrivateFetchFailed is true when membership could not be loaded;
// openPrivateSchemeAdmin is ignored unless the channel is open/private and fetch succeeded.
func automationListVisibility(isSysadmin bool, channel *model.Channel, openPrivateSchemeAdmin, openPrivateFetchFailed bool) bool {
	if isSysadmin {
		return true
	}
	if channel == nil {
		return true
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return true
	}
	if openPrivateFetchFailed {
		return false
	}
	return openPrivateSchemeAdmin
}

// automationToolsVisibleInList reports whether automation tools should appear in tools/list for the
// authenticated user and Mattermost channel. It uses the Mattermost REST client (session or OAuth).
func automationToolsVisibleInList(ctx context.Context, client *model.Client4, user *model.User, channel *model.Channel) bool {
	isSysadmin := user != nil && user.IsSystemAdmin()
	if channel == nil {
		return automationListVisibility(isSysadmin, channel, false, false)
	}
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		return automationListVisibility(isSysadmin, channel, false, false)
	}
	if user == nil {
		return automationListVisibility(isSysadmin, channel, false, true)
	}
	member, _, err := client.GetChannelMember(ctx, channel.Id, user.Id, "")
	if err != nil {
		return automationListVisibility(isSysadmin, channel, false, true)
	}
	return automationListVisibility(isSysadmin, channel, member.SchemeAdmin, false)
}
