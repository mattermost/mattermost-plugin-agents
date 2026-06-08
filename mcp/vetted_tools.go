// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/url"
	"strings"
)

// IsVettedHost returns true when the baseURL host matches one of the
// Mattermost-curated vetted MCP server hosts.
//
// Matching semantics intentionally preserve the previous approved-server behavior:
// - host-only matching
// - path/query/fragment/port ignored
// - exact host or subdomain match
// - supports embedded://mattermost
func IsVettedHost(baseURL string) bool {
	if baseURL == EmbeddedClientKey {
		return true
	}

	host, ok := vettedHostFromBaseURL(baseURL)
	if !ok {
		return false
	}

	for _, pattern := range vettedHostPatterns() {
		if host == pattern || strings.HasSuffix(host, "."+pattern) {
			return true
		}
	}

	return false
}

// SeedVettedToolConfigs returns one-time seed tool configs for vetted MCP hosts.
//
// Only Mattermost-curated READ-only tools are seeded. Most are enabled with
// policy auto_run_in_dm; GitHub security-scanning reads default to policy ask
// (admins may switch). Non-READ tools are intentionally not persisted here;
// tools without config fall back to the runtime default of policy="ask",
// enabled=true until an admin explicitly configures them.
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

func vettedHostPatterns() []string {
	return []string{
		"mcp.atlassian.com",
		"api.githubcopilot.com",
		"mcp.figma.com",
	}
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
	orderedNames := []string{
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
	}

	out := make([]ToolConfig, 0, len(orderedNames))
	for _, name := range orderedNames {
		if _, securityAsk := githubSecurityAskTools[name]; securityAsk {
			out = append(out, askToolConfigs([]string{name})...)
		} else {
			out = append(out, autoRunInDMToolConfigs([]string{name})...)
		}
	}
	return out
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
// are visible to everyone. read_file belongs here (not auto-run-everywhere): it
// only checks the caller's own access, so auto-running it in a channel could
// surface a file the channel can't see.
var mattermostVettedToolConfigs = autoRunInDMToolConfigs([]string{
	"read_post",
	"read_channel",
	"get_channel_info",
	"get_channel_members",
	"get_team_info",
	"get_team_members",
	"search_posts",
	"search_users",
	"get_user_channels",
	"read_file",
})
