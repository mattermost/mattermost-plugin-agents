// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

// scopedUnsupportedTools are the read-only tools that cannot work under a
// scoped session: every one needs a REST route outside the scoped route
// allowlist (membership lists, per-user state, admin data, or links that
// outlive the session), and those routes stay denied however wide the scope.
// The agent plugin leaves them out of a scoped agent's catalog.
var scopedUnsupportedTools = map[string]struct{}{
	// channels
	"get_channel_members":       {},
	"get_user_channels":         {},
	"get_channel_member_counts": {},
	"list_archived_channels":    {},

	// channel members
	"get_channel_member":            {},
	"get_channel_members_by_ids":    {},
	"get_channel_members_by_status": {},
	"get_user_channel_memberships":  {},
	"get_users_not_in_channel":      {},
	"search_users_in_channel":       {},
	"list_sidebar_categories":       {},

	// posts, threads, and unreads
	"get_post_info":           {},
	"list_saved_posts":        {},
	"get_threads":             {},
	"get_unread_counts":       {},
	"get_channel_unread":      {},
	"get_posts_around_unread": {},

	// files
	"get_file_link": {},

	// teams
	"get_team_members":      {},
	"get_team_member":       {},
	"get_team_stats":        {},
	"get_user_teams":        {},
	"get_users_in_team":     {},
	"get_users_not_in_team": {},
	"get_new_users_in_team": {},
	"search_users_in_team":  {},
	"get_dm_common_teams":   {},

	// users and status
	"get_user_by_email":   {},
	"get_user_status":     {},
	"get_users_statuses":  {},
	"get_user_stats":      {},
	"get_user_cpa_values": {},
	"list_cpa_fields":     {},

	// bookmarks, groups, integrations, roles, emoji, drafts, scheduled posts, agents
	"list_channel_bookmarks":      {},
	"get_group_info":              {},
	"list_groups":                 {},
	"get_user_groups":             {},
	"get_channel_groups":          {},
	"get_team_groups":             {},
	"get_users_in_group_channels": {},
	"get_bot":                     {},
	"list_bots":                   {},
	"list_incoming_webhooks":      {},
	"list_outgoing_webhooks":      {},
	"get_role":                    {},
	"get_channel_moderations":     {},
	"list_custom_emoji":           {},
	"search_custom_emoji":         {},
	"list_drafts":                 {},
	"list_scheduled_posts":        {},
	"list_agents":                 {},
}
