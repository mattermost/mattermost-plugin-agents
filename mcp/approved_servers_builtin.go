// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

// BuiltinApprovedServers returns the Mattermost-curated list of approved MCP servers.
// These are compiled into the plugin and represent Mattermost's assessment of which
// tools are READ-only on well-known MCP servers. The Enabled field on each is set to
// false by default; it is overridden at runtime by the ApprovedProviders config toggles.
func BuiltinApprovedServers() []ApprovedMCPServer {
	return []ApprovedMCPServer{
		atlassianApprovedServer(),
		githubApprovedServer(),
		figmaApprovedServer(),
	}
}

// atlassianApprovedServer returns the approved server definition for the Atlassian Rovo MCP Server.
// Source: ATLASSIAN.md — 20 READ-only tools out of 29 total.
func atlassianApprovedServer() ApprovedMCPServer {
	return ApprovedMCPServer{
		Name:        "Atlassian",
		URLPatterns: []string{"mcp.atlassian.com"},
		Enabled:     false,
		AutoApproveTools: []string{
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
		},
	}
}

// githubApprovedServer returns the approved server definition for the GitHub MCP Server.
// Source: GITHUB.md — 56 READ-only tools out of 88 total.
func githubApprovedServer() ApprovedMCPServer {
	return ApprovedMCPServer{
		Name:        "GitHub",
		URLPatterns: []string{"api.githubcopilot.com"},
		Enabled:     false,
		AutoApproveTools: []string{
			"get_me",
			"get_team_members",
			"get_teams",
			"get_commit",
			"get_file_contents",
			"get_latest_release",
			"get_release_by_tag",
			"get_tag",
			"list_branches",
			"list_commits",
			"list_releases",
			"list_tags",
			"search_code",
			"search_repositories",
			"get_label",
			"issue_read",
			"list_issue_types",
			"list_issues",
			"search_issues",
			"list_pull_requests",
			"pull_request_read",
			"search_pull_requests",
			"search_users",
			"actions_get",
			"actions_list",
			"get_job_logs",
			"get_code_scanning_alert",
			"list_code_scanning_alerts",
			"get_dependabot_alert",
			"list_dependabot_alerts",
			"get_discussion",
			"get_discussion_comments",
			"list_discussion_categories",
			"list_discussions",
			"get_gist",
			"list_gists",
			"get_repository_tree",
			"list_label",
			"get_notification_details",
			"list_notifications",
			"search_orgs",
			"projects_get",
			"projects_list",
			"get_secret_scanning_alert",
			"list_secret_scanning_alerts",
			"get_global_security_advisory",
			"list_global_security_advisories",
			"list_org_repository_security_advisories",
			"list_repository_security_advisories",
			"list_starred_repositories",
			"get_copilot_job_status",
			"get_copilot_space",
			"list_copilot_spaces",
			"github_support_docs_search",
		},
	}
}

// figmaApprovedServer returns the approved server definition for the Figma MCP Server.
// Source: FIGMA.md — 9 READ-only tools out of 13 total.
func figmaApprovedServer() ApprovedMCPServer {
	return ApprovedMCPServer{
		Name:        "Figma",
		URLPatterns: []string{"mcp.figma.com"},
		Enabled:     false,
		AutoApproveTools: []string{
			"get_design_context",
			"get_metadata",
			"get_screenshot",
			"get_variable_defs",
			"get_figjam",
			"create_design_system_rules",
			"get_code_connect_map",
			"get_code_connect_suggestions",
			"whoami",
		},
	}
}
