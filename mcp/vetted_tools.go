// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/url"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// IsRemoteServerOrigin reports whether an MCP server origin points at a
// remote/external server. Built-in tools carry an empty origin and the
// embedded Mattermost server uses EmbeddedClientKey; neither counts as
// remote. Every other origin — remote HTTP servers and plugin-registered
// servers — belongs to the licensed "MCP Support" feature.
func IsRemoteServerOrigin(origin string) bool {
	origin = llm.NormalizeMCPServerOrigin(origin)
	return origin != "" && origin != EmbeddedClientKey
}

// SeedVettedToolConfigs returns one-time seed tool configs for vetted MCP hosts.
//
// Only Mattermost-curated READ-only tools are seeded. Most are enabled with
// policy auto_run_in_dm; GitHub security-scanning reads default to policy ask
// (admins may switch). Non-READ tools are intentionally not persisted here;
// tools without config fall back to DefaultToolPolicy (ask, or
// auto_run_in_dm when the server marks the tool read-only), enabled=true,
// until an admin explicitly configures them. A remote server whose read
// tools are all verified to carry readOnlyHint needs no seed for them; only
// deliberate exceptions (such as GitHub's security reads) stay listed.
func SeedVettedToolConfigs(baseURL string) []ToolConfig {
	if baseURL == EmbeddedClientKey {
		return cloneToolConfigs(mattermostVettedToolConfigs)
	}

	host, ok := vettedHostFromBaseURL(baseURL)
	if !ok {
		return nil
	}

	switch {
	case host == "mcp.atlassian.com" || strings.HasSuffix(host, ".mcp.atlassian.com"):
		return cloneToolConfigs(atlassianVettedToolConfigs)
	case host == "api.githubcopilot.com" || strings.HasSuffix(host, ".api.githubcopilot.com"):
		return cloneToolConfigs(githubVettedToolConfigs)
	case host == "mcp.figma.com" || strings.HasSuffix(host, ".mcp.figma.com"):
		return cloneToolConfigs(figmaVettedToolConfigs)
	default:
		return nil
	}
}

func vettedHostFromBaseURL(baseURL string) (string, bool) {
	if baseURL == "" {
		return "", false
	}

	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return "", false
	}

	host := u.Hostname()
	if host == "" {
		return "", false
	}

	return host, true
}

func cloneToolConfigs(src []ToolConfig) []ToolConfig {
	if len(src) == 0 {
		return nil
	}

	dst := make([]ToolConfig, len(src))
	copy(dst, src)
	return dst
}

// mergeSeedConfigs appends seed entries for tool names missing from stored, so
// stored entries win. Inputs are not mutated.
func mergeSeedConfigs(stored, seed []ToolConfig) []ToolConfig {
	if len(stored) == 0 {
		return seed
	}

	present := make(map[string]bool, len(stored))
	merged := make([]ToolConfig, 0, len(stored)+len(seed))
	for _, tc := range stored {
		present[tc.Name] = true
		merged = append(merged, tc)
	}
	for _, tc := range seed {
		if !present[tc.Name] {
			merged = append(merged, tc)
		}
	}
	return merged
}

func autoRunInDMToolConfigs(toolNames []string) []ToolConfig {
	configs := make([]ToolConfig, 0, len(toolNames))
	for _, toolName := range toolNames {
		configs = append(configs, ToolConfig{
			Name:    toolName,
			Policy:  ToolPolicyAutoRunInDM,
			Enabled: true,
		})
	}
	return configs
}

func askToolConfigs(toolNames []string) []ToolConfig {
	configs := make([]ToolConfig, 0, len(toolNames))
	for _, toolName := range toolNames {
		configs = append(configs, ToolConfig{
			Name:    toolName,
			Policy:  ToolPolicyAsk,
			Enabled: true,
		})
	}
	return configs
}

// githubSecurityAskTools are GitHub Copilot MCP reads that surface vulnerability
// and secret-scanning posture; they default to ask rather than auto-run.
var githubSecurityAskTools = map[string]struct{}{
	"get_code_scanning_alert":                 {},
	"list_code_scanning_alerts":               {},
	"get_dependabot_alert":                    {},
	"list_dependabot_alerts":                  {},
	"get_secret_scanning_alert":               {},
	"list_secret_scanning_alerts":             {},
	"list_org_repository_security_advisories": {},
	"list_repository_security_advisories":     {},
}

func buildGithubVettedToolConfigs() []ToolConfig {
	// Every other GitHub read tool is left to DefaultToolPolicy: the GitHub
	// MCP server (github/github-mcp-server, checked at v1.14.0) sets
	// readOnlyHint on all of its read tools, so they already auto-run in DMs.
	// The security reads above are listed to keep them at ask despite the
	// hint. These remaining reads exist only on the hosted server, so their
	// annotations could not be checked from source.
	unverifiedReadTools := []string{
		"get_copilot_job_status",
		"get_copilot_space",
		"list_copilot_spaces",
		"github_support_docs_search",
	}

	securityNames := make([]string, 0, len(githubSecurityAskTools))
	for name := range githubSecurityAskTools {
		securityNames = append(securityNames, name)
	}
	slices.Sort(securityNames)

	return append(askToolConfigs(securityNames), autoRunInDMToolConfigs(unverifiedReadTools)...)
}

var githubVettedToolConfigs = buildGithubVettedToolConfigs()

var atlassianVettedToolConfigs = autoRunInDMToolConfigs([]string{
	"search",
	"fetch",
	"atlassianUserInfo",
	"getAccessibleAtlassianResources",
	"getConfluenceSpaces",
	"getConfluencePage",
	"getPagesInConfluenceSpace",
	"getConfluencePageAncestors",
	"getConfluencePageDescendants",
	"getConfluencePageFooterComments",
	"getConfluencePageInlineComments",
	"searchConfluenceUsingCql",
	"getJiraIssue",
	"getJiraIssueRemoteIssueLinks",
	"getTransitionsForJiraIssue",
	"getVisibleJiraProjects",
	"getJiraProjectIssueTypesMetadata",
	"getJiraIssueTypeMetaWithFields",
	"lookupJiraAccountId",
	"searchJiraIssuesUsingJql",
})

var figmaVettedToolConfigs = autoRunInDMToolConfigs([]string{
	"get_design_context",
	"get_metadata",
	"get_screenshot",
	"get_variable_defs",
	"get_figjam",
	"get_code_connect_map",
	"get_code_connect_suggestions",
	"whoami",
})

// Read-only Mattermost tools auto-run in DMs but ask in channels, where results
// are visible to everyone. Every tool here is a caller-scoped read that only
// returns data the requesting user can already access, so auto-running it in a
// private DM surfaces nothing new to that user.
//
// Deliberately excluded (left at the runtime default of policy="ask"):
//   - get_file_link mints a public, unauthenticated bypass link for a file.
//   - list_incoming_webhooks / list_outgoing_webhooks surface webhook IDs and
//     tokens that are effectively posting credentials.
//
// These mirror the credential-exposing GitHub reads in githubSecurityAskTools.
var mattermostVettedToolConfigs = autoRunInDMToolConfigs([]string{
	// Posts & scheduled posts
	"read_post",
	"get_post_info",
	"list_pinned_posts",
	"list_saved_posts",
	"list_scheduled_posts",

	// Reactions & emoji
	"get_post_reactions",
	"list_custom_emoji",
	"search_custom_emoji",

	// Threads & unread
	"get_threads",
	"get_mentions",
	"get_unread_counts",
	"get_posts_around_unread",
	"get_channel_unread",
	"list_sidebar_categories",

	// Channels
	"read_channel",
	"get_channel_info",
	"get_channel_stats",
	"get_channel_member_counts",
	"search_channels",
	"list_team_channels",
	"list_archived_channels",

	// Channel members & bookmarks
	"get_channel_members",
	"get_channel_member",
	"get_channel_members_by_ids",
	"get_channel_members_by_status",
	"get_users_not_in_channel",
	"search_users_in_channel",
	"get_user_channel_memberships",
	"list_channel_bookmarks",

	// Users & profiles
	"get_me",
	"get_user",
	"get_user_by_username",
	"get_user_by_email",
	"get_users_by_ids",
	"get_users_by_usernames",
	"get_user_stats",
	"get_user_channels",
	"list_cpa_fields",
	"get_user_cpa_values",

	// Status & presence
	"get_user_status",
	"get_users_statuses",
	"get_user_custom_status",

	// Teams
	"get_team_info",
	"get_team_members",
	"get_team_member",
	"get_team_stats",
	"search_teams",
	"get_users_in_team",
	"get_users_not_in_team",
	"get_new_users_in_team",
	"get_dm_common_teams",
	"get_user_teams",
	"search_users_in_team",

	// Search & files. read_file only checks the caller's own access, so it stays
	// auto-run-in-DM (not auto-run-everywhere): auto-running it in a channel
	// could surface a file the channel can't see.
	"search_posts",
	"search_users",
	"search_files",
	"get_file_info",
	"get_post_files",
	"read_file",

	// Integrations
	"get_bot",
	"list_bots",

	// Groups
	"get_group_info",
	"list_groups",
	"get_channel_groups",
	"get_team_groups",
	"get_user_groups",
	"get_users_in_group_channels",

	// Roles
	"get_role",
	"get_channel_moderations",
})
