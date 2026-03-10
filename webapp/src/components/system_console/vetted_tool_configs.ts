// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Vetted host patterns and their pre-seeded tool configs.
// This mirrors the backend mcp/vetted_tools.go for client-side pre-seeding.

import {MCPToolConfig} from './mcp_servers';

type VettedHost = {
    patterns: string[];
    toolConfigs: MCPToolConfig[];
};

const autoRun = (name: string): MCPToolConfig => ({name, policy: 'auto_run', enabled: true});

const VETTED_HOSTS: VettedHost[] = [
    {
        patterns: ['mcp.atlassian.com'],
        toolConfigs: [
            autoRun('search'), autoRun('fetch'), autoRun('atlassianUserInfo'),
            autoRun('getAccessibleAtlassianResources'),
            autoRun('getConfluenceSpaces'), autoRun('getConfluencePage'),
            autoRun('getPagesInConfluenceSpace'), autoRun('getConfluencePageAncestors'),
            autoRun('getConfluencePageDescendants'), autoRun('getConfluencePageFooterComments'),
            autoRun('getConfluencePageInlineComments'), autoRun('searchConfluenceUsingCql'),
            autoRun('getJiraIssue'), autoRun('getJiraIssueRemoteIssueLinks'),
            autoRun('getTransitionsForJiraIssue'), autoRun('getVisibleJiraProjects'),
            autoRun('getJiraProjectIssueTypesMetadata'), autoRun('getJiraIssueTypeMetaWithFields'),
            autoRun('lookupJiraAccountId'), autoRun('searchJiraIssuesUsingJql'),
        ],
    },
    {
        patterns: ['api.githubcopilot.com'],
        toolConfigs: [
            autoRun('get_me'), autoRun('get_team_members'), autoRun('get_teams'),
            autoRun('get_commit'), autoRun('get_file_contents'), autoRun('get_latest_release'),
            autoRun('get_release_by_tag'), autoRun('get_tag'), autoRun('list_branches'),
            autoRun('list_commits'), autoRun('list_releases'), autoRun('list_tags'),
            autoRun('search_code'), autoRun('search_repositories'), autoRun('get_label'),
            autoRun('issue_read'), autoRun('list_issue_types'), autoRun('list_issues'),
            autoRun('search_issues'), autoRun('list_pull_requests'), autoRun('pull_request_read'),
            autoRun('search_pull_requests'), autoRun('search_users'),
            autoRun('actions_get'), autoRun('actions_list'), autoRun('get_job_logs'),
            autoRun('get_code_scanning_alert'), autoRun('list_code_scanning_alerts'),
            autoRun('get_dependabot_alert'), autoRun('list_dependabot_alerts'),
            autoRun('get_discussion'), autoRun('get_discussion_comments'),
            autoRun('list_discussion_categories'), autoRun('list_discussions'),
            autoRun('get_gist'), autoRun('list_gists'), autoRun('get_repository_tree'),
            autoRun('list_label'), autoRun('get_notification_details'), autoRun('list_notifications'),
            autoRun('search_orgs'), autoRun('projects_get'), autoRun('projects_list'),
            autoRun('get_secret_scanning_alert'), autoRun('list_secret_scanning_alerts'),
            autoRun('get_global_security_advisory'), autoRun('list_global_security_advisories'),
            autoRun('list_org_repository_security_advisories'),
            autoRun('list_repository_security_advisories'),
            autoRun('list_starred_repositories'),
            autoRun('get_copilot_job_status'), autoRun('get_copilot_space'),
            autoRun('list_copilot_spaces'), autoRun('github_support_docs_search'),
        ],
    },
    {
        patterns: ['mcp.figma.com'],
        toolConfigs: [
            autoRun('get_design_context'), autoRun('get_metadata'), autoRun('get_screenshot'),
            autoRun('get_variable_defs'), autoRun('get_figjam'),
            autoRun('get_code_connect_map'), autoRun('get_code_connect_suggestions'), autoRun('whoami'),
        ],
    },
    {
        patterns: ['mattermost'],
        toolConfigs: [
            autoRun('read_post'), autoRun('read_channel'), autoRun('get_channel_info'),
            autoRun('get_channel_members'), autoRun('get_team_info'), autoRun('get_team_members'),
            autoRun('search_posts'), autoRun('search_users'),
        ],
    },
];

/**
 * Returns the vetted host pattern that matches the given URL, or null if no match.
 * Used to detect when the vetted host identity changes between URL edits.
 */
export function getVettedHostIdentity(baseURL: string): string | null {
    if (!baseURL) {
        return null;
    }

    const lower = baseURL.toLowerCase();
    for (const host of VETTED_HOSTS) {
        for (const pattern of host.patterns) {
            if (lower.includes(pattern.toLowerCase())) {
                return pattern;
            }
        }
    }
    return null;
}

/**
 * Returns pre-seeded tool configs if the baseURL matches a known vetted host.
 * Returns null for non-vetted hosts.
 */
export function seedVettedToolConfigs(baseURL: string): MCPToolConfig[] | null {
    if (!baseURL) {
        return null;
    }

    const lower = baseURL.toLowerCase();
    for (const host of VETTED_HOSTS) {
        for (const pattern of host.patterns) {
            if (lower.includes(pattern.toLowerCase())) {
                // Return a deep copy
                return host.toolConfigs.map((tc) => ({...tc}));
            }
        }
    }
    return null;
}
