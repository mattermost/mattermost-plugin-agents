// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolCatalogNoDuplicateNames guards against accidental duplicate tool
// registrations across the (now large) catalog, in both production and dev mode.
func TestToolCatalogNoDuplicateNames(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		provider := &MattermostToolProvider{
			logger:     &testLogger{t: t},
			accessMode: AccessModeRemote,
			devMode:    devMode,
		}

		seen := make(map[string]bool)
		for _, name := range provider.ToolNames() {
			require.False(t, seen[name], "duplicate tool name %q (devMode=%t)", name, devMode)
			seen[name] = true
		}
	}
}

func TestToolCatalogDoesNotContainBulkReactions(t *testing.T) {
	provider := &MattermostToolProvider{
		logger:     &testLogger{t: t},
		accessMode: AccessModeRemote,
	}

	require.NotContains(t, provider.ToolNames(), "get_bulk_reactions")
}

// TestToolCatalogContainsExpectedFamilies verifies a representative tool from
// each new domain family is registered, so a missing group wiring is caught.
func TestToolCatalogContainsExpectedFamilies(t *testing.T) {
	provider := &MattermostToolProvider{
		logger:     &testLogger{t: t},
		accessMode: AccessModeRemote,
	}

	names := make(map[string]bool)
	for _, name := range provider.ToolNames() {
		names[name] = true
	}

	expected := []string{
		// modifications
		"add_channel_member", "add_team_member",
		// posts & scheduled
		"get_post_info", "update_post", "pin_post", "save_post", "acknowledge_post",
		"create_scheduled_post", "set_post_reminder",
		// reactions & emoji
		"get_post_reactions", "add_reaction", "list_custom_emoji",
		// threads & unread
		"get_threads", "get_mentions", "get_unread_counts", "mark_channel_read", "set_thread_follow",
		// channels
		"get_channel_stats", "search_channels", "update_channel", "archive_channel", "convert_channel_privacy",
		// channel members & settings
		"get_channel_member", "add_channel_members", "set_channel_mute", "set_channel_favorite", "update_channel_notify_props",
		// bookmarks
		"list_channel_bookmarks", "create_channel_bookmark",
		// users & profiles
		"get_me", "get_user", "get_user_by_username", "get_users_by_ids", "list_cpa_fields", "update_user",
		// status
		"get_user_status", "set_status", "set_dnd",
		// teams
		"get_team_member", "get_team_stats", "search_teams", "invite_users_to_team", "update_team",
		// files
		"get_file_info", "get_post_files", "get_file_link", "search_files", "upload_file",
		// integrations
		"get_bot", "list_bots", "list_incoming_webhooks", "list_outgoing_webhooks",
		// groups
		"get_group_info", "list_groups", "get_users_in_group_channels",
		// roles
		"get_role", "get_channel_moderations", "update_channel_member_roles", "update_team_member_roles",
	}

	for _, name := range expected {
		assert.True(t, names[name], "expected tool %q to be registered", name)
	}
}
