// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedToolReadOnly is the explicit classification of every built-in MCP
// tool: true means read-only. A newly added tool without a row fails the test.
var expectedToolReadOnly = map[string]bool{
	// posts
	"read_post":           true,
	"create_post":         false,
	"dm":                  false,
	"group_message":       false,
	"get_post_info":       true,
	"list_pinned_posts":   true,
	"list_saved_posts":    true,
	"update_post":         false,
	"delete_post":         false,
	"pin_post":            false,
	"unpin_post":          false,
	"save_post":           false,
	"acknowledge_post":    false,
	"create_post_as_user": false, // dev

	// scheduled posts
	"list_scheduled_posts":  true,
	"create_scheduled_post": false,
	"update_scheduled_post": false,
	"delete_scheduled_post": false,
	"set_post_reminder":     false,

	// reactions
	"get_post_reactions":  true,
	"list_custom_emoji":   true,
	"search_custom_emoji": true,
	"add_reaction":        false,
	"remove_reaction":     false,

	// threads
	"get_threads":             true,
	"get_mentions":            true,
	"get_unread_counts":       true,
	"get_channel_unread":      true,
	"get_posts_around_unread": true,
	"mark_channel_read":       false,
	"mark_channels_viewed":    false,
	"mark_post_unread":        false,
	"set_thread_follow":       false,

	// channels
	"read_channel":              true,
	"create_channel":            false,
	"get_channel_info":          true,
	"get_channel_members":       true,
	"add_channel_member":        false,
	"get_user_channels":         true,
	"get_channel_stats":         true,
	"get_channel_member_counts": true,
	"search_channels":           true,
	"list_team_channels":        true,
	"list_archived_channels":    true,
	"update_channel":            false,
	"archive_channel":           false,
	"restore_channel":           false,
	"convert_channel_privacy":   false,

	// channel members
	"get_channel_member":            true,
	"get_channel_members_by_ids":    true,
	"get_channel_members_by_status": true,
	"get_user_channel_memberships":  true,
	"get_users_not_in_channel":      true,
	"search_users_in_channel":       true,
	"list_sidebar_categories":       true,
	"add_channel_members":           false,
	"remove_channel_member":         false,
	"set_channel_mute":              false,
	"set_channel_favorite":          false,
	"update_channel_notify_props":   false,

	// bookmarks
	"list_channel_bookmarks":  true,
	"create_channel_bookmark": false,
	"update_channel_bookmark": false,
	"delete_channel_bookmark": false,

	// users
	"get_me":                 true,
	"get_user":               true,
	"get_user_by_username":   true,
	"get_user_by_email":      true,
	"get_users_by_ids":       true,
	"get_users_by_usernames": true,
	"get_user_stats":         true,
	"get_user_cpa_values":    true,
	"list_cpa_fields":        true,
	"update_user":            false,
	"create_user":            false, // dev

	// status
	"get_user_status":        true,
	"get_users_statuses":     true,
	"get_user_custom_status": true,
	"set_status":             false,
	"set_dnd":                false,

	// teams
	"get_team_info":                     true,
	"get_team_members":                  true,
	"add_team_member":                   false,
	"get_team_member":                   true,
	"get_team_stats":                    true,
	"get_user_teams":                    true,
	"get_users_in_team":                 true,
	"get_users_not_in_team":             true,
	"get_new_users_in_team":             true,
	"get_dm_common_teams":               true,
	"search_teams":                      true,
	"search_users_in_team":              true,
	"add_team_members":                  false,
	"remove_team_member":                false,
	"update_team":                       false,
	"invite_users_to_team":              false,
	"invite_users_to_team_and_channels": false,
	"create_team":                       false, // dev

	// search
	"search_posts": true,
	"search_users": true,

	// files
	"read_file":      true,
	"get_file_info":  true,
	"get_post_files": true,
	"get_file_link":  true,
	"search_files":   true,
	"upload_file":    false,

	// integrations
	"get_bot":                true,
	"list_bots":              true,
	"list_incoming_webhooks": true,
	"list_outgoing_webhooks": true,

	// groups
	"get_group_info":              true,
	"list_groups":                 true,
	"get_user_groups":             true,
	"get_channel_groups":          true,
	"get_team_groups":             true,
	"get_users_in_group_channels": true,

	// roles
	"get_role":                    true,
	"get_channel_moderations":     true,
	"update_channel_member_roles": false,
	"update_team_member_roles":    false,

	// agents
	"list_agents": true,

	// automations
	"list_automations":            true,
	"get_automation_instructions": true,
	"create_automation":           false,
	"update_automation":           false,
	"delete_automation":           false,
}

func TestMCPToolClassification(t *testing.T) {
	seen := map[string]bool{}
	for _, devMode := range []bool{false, true} {
		provider := &MattermostToolProvider{
			logger:     &testLogger{t: t},
			accessMode: AccessModeRemote,
			devMode:    devMode,
		}
		for _, tool := range provider.mcpTools() {
			want, ok := expectedToolReadOnly[tool.Name]
			require.True(t, ok, "missing expected classification row for %q (devMode=%t); add it to expectedToolReadOnly", tool.Name, devMode)
			assert.Equal(t, want, tool.ReadOnly, "ReadOnly mismatch for %q (devMode=%t)", tool.Name, devMode)
			seen[tool.Name] = true
		}
	}
	for name := range expectedToolReadOnly {
		assert.True(t, seen[name], "expected tool %q was not returned by mcpTools() with dev mode on or off", name)
	}
}
