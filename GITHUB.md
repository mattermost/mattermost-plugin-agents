# GitHub Remote MCP Server - Tool Classification

## Overview

The **GitHub MCP Server** is GitHub's official remote MCP server for providing AI agents with structured access to GitHub data and operations.

**Remote Endpoint:** `https://api.githubcopilot.com/mcp/` (Streamable HTTP)
**Repository:** [github/github-mcp-server](https://github.com/github/github-mcp-server)
**Version Basis:** v0.30.3 (February 2026)

---

## Tool Classification Summary

| Classification | Count | Percentage |
|---------------|-------|------------|
| **READ**      | 56    | 63.6%      |
| **WRITE**     | 25    | 28.4%      |
| **MIXED**     | 5     | 5.7%       |
| **DELETE**     | 2     | 2.3%       |
| **Total**     | **88** | 100%      |

---

## Toolset: `context` (Default)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_me` | Get the authenticated user's profile information. | **READ** | Only retrieves current user's profile data. |
| `get_team_members` | Get members of a specific team. | **READ** | Only retrieves team membership information. |
| `get_teams` | Get teams for an organization. | **READ** | Only retrieves team listing data. |

---

## Toolset: `repos` (Default)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `create_branch` | Create a new branch in a GitHub repository. | **WRITE** | Creates a new git branch reference. |
| `create_or_update_file` | Create or update a single file in a repository. | **WRITE** | Creates or modifies file content. |
| `create_repository` | Create a new GitHub repository. | **WRITE** | Creates a new repository resource. |
| `delete_file` | Delete a file from a GitHub repository. | **DELETE** | Removes a file from the repository. |
| `fork_repository` | Fork a GitHub repository. | **WRITE** | Creates a new forked repository. |
| `get_commit` | Get details for a specific commit. | **READ** | Only retrieves commit metadata and diff. |
| `get_file_contents` | Get the contents of a file or directory. | **READ** | Only retrieves file/directory content. |
| `get_latest_release` | Get the latest release for a repository. | **READ** | Only retrieves release information. |
| `get_release_by_tag` | Get a release by its tag name. | **READ** | Only retrieves release information. |
| `get_tag` | Get details about a specific git tag. | **READ** | Only retrieves tag metadata. |
| `list_branches` | List branches in a repository. | **READ** | Only retrieves branch listing. |
| `list_commits` | Get list of commits of a branch. | **READ** | Only retrieves commit history. |
| `list_releases` | List releases for a repository. | **READ** | Only retrieves release listing. |
| `list_tags` | List git tags in a repository. | **READ** | Only retrieves tag listing. |
| `push_files` | Push multiple files in a single commit. | **WRITE** | Creates a new commit with file changes. |
| `search_code` | Search for code across GitHub repositories. | **READ** | Only searches and returns code matches. |
| `search_repositories` | Search for GitHub repositories. | **READ** | Only searches and returns repository matches. |

---

## Toolset: `issues` (Default)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `add_issue_comment` | Add a comment to a specific issue. | **WRITE** | Creates a new comment on an issue. |
| `get_label` | Get a specific label from a repository. | **READ** | Only retrieves label information. |
| `issue_read` | Get issue details (consolidated read methods via `method` param). | **READ** | Only retrieves issue data, comments, and metadata. |
| `issue_write` | Create or update an issue (consolidated write via `method` param). | **WRITE** | Creates new issues or modifies existing ones. |
| `list_issue_types` | List available issue types for a repository. | **READ** | Only retrieves issue type definitions. |
| `list_issues` | List issues in a repository with filtering. | **READ** | Only retrieves issue listing. |
| `search_issues` | Search for issues across GitHub repositories. | **READ** | Only searches and returns issue matches. |
| `sub_issue_write` | Add, remove, or reorder sub-issues. | **WRITE** | Modifies the sub-issue relationship hierarchy. |

---

## Toolset: `pull_requests` (Default)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `add_comment_to_pending_review` | Add a review comment to a pending pull request review. | **WRITE** | Creates a new review comment. |
| `add_reply_to_pull_request_comment` | Add a reply to an existing PR comment. | **WRITE** | Creates a new reply comment. |
| `create_pull_request` | Open a new pull request. | **WRITE** | Creates a new pull request. |
| `list_pull_requests` | List pull requests in a repository. | **READ** | Only retrieves pull request listing. |
| `merge_pull_request` | Merge a pull request. | **WRITE** | Performs the merge operation. |
| `pull_request_read` | Get PR details (consolidated read: details, diff, files, reviews, comments). | **READ** | Only retrieves PR data. |
| `pull_request_review_write` | Write operations on PR reviews: create, submit, delete pending. | **MIXED** | Can create (WRITE), submit (WRITE), and delete (DELETE) reviews. |
| `search_pull_requests` | Search for pull requests across repositories. | **READ** | Only searches and returns PR matches. |
| `update_pull_request` | Edit an existing pull request. | **WRITE** | Modifies existing pull request properties. |
| `update_pull_request_branch` | Update the branch of a pull request. | **WRITE** | Triggers a branch update/merge operation. |

---

## Toolset: `users` (Default)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `search_users` | Search for GitHub users. | **READ** | Only searches and returns user matches. |

---

## Toolset: `actions`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `actions_get` | Get details of GitHub Actions resources. | **READ** | Only retrieves Actions resource details. |
| `actions_list` | List workflows and workflow runs. | **READ** | Only retrieves workflow/run listings. |
| `actions_run_trigger` | Trigger workflow actions (run, re-run, cancel). | **MIXED** | Can trigger new runs (WRITE), re-run (WRITE), cancel (DELETE). |
| `get_job_logs` | Get workflow job logs. | **READ** | Only retrieves log content. |

---

## Toolset: `code_security`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_code_scanning_alert` | Get details of a code scanning alert. | **READ** | Only retrieves alert details. |
| `list_code_scanning_alerts` | List code scanning alerts. | **READ** | Only retrieves alert listings. |

---

## Toolset: `dependabot`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_dependabot_alert` | Get details of a Dependabot alert. | **READ** | Only retrieves alert details. |
| `list_dependabot_alerts` | List Dependabot alerts. | **READ** | Only retrieves alert listings. |

---

## Toolset: `discussions`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_discussion` | Get details of a specific discussion. | **READ** | Only retrieves discussion content. |
| `get_discussion_comments` | Get comments for a discussion. | **READ** | Only retrieves discussion comments. |
| `list_discussion_categories` | List discussion categories. | **READ** | Only retrieves category definitions. |
| `list_discussions` | List discussions in a repository. | **READ** | Only retrieves discussion listings. |

---

## Toolset: `gists`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `create_gist` | Create a new GitHub Gist. | **WRITE** | Creates a new gist resource. |
| `get_gist` | Get the content of a specific Gist. | **READ** | Only retrieves gist content. |
| `list_gists` | List Gists for the authenticated user. | **READ** | Only retrieves gist listings. |
| `update_gist` | Update an existing Gist. | **WRITE** | Modifies existing gist content. |

---

## Toolset: `git`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_repository_tree` | Get the file/directory tree of a repository. | **READ** | Only retrieves repository tree structure. |

---

## Toolset: `labels`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `label_write` | Write operations on labels (create, update, delete). | **MIXED** | Can create (WRITE), update (WRITE), delete (DELETE) labels. |
| `list_label` | List labels from a repository. | **READ** | Only retrieves label listings. |

---

## Toolset: `notifications`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `dismiss_notification` | Mark a notification as read or done. | **WRITE** | Modifies the notification's read/done state. |
| `get_notification_details` | Get details for a specific notification. | **READ** | Only retrieves notification details. |
| `list_notifications` | List all notifications for the authenticated user. | **READ** | Only retrieves notification listings. |
| `manage_notification_subscription` | Manage a notification thread subscription (ignore, watch, delete). | **MIXED** | Can watch (WRITE), ignore (WRITE), delete (DELETE) subscriptions. |
| `manage_repository_notification_subscription` | Manage a repository notification subscription. | **MIXED** | Can subscribe (WRITE), unsubscribe (WRITE), ignore (WRITE). |
| `mark_all_notifications_read` | Mark all notifications as read. | **WRITE** | Bulk-modifies notification read state. |

---

## Toolset: `orgs`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `search_orgs` | Search for GitHub organizations. | **READ** | Only searches and returns organization matches. |

---

## Toolset: `projects`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `projects_get` | Get details about a specific GitHub Project. | **READ** | Only retrieves project details. |
| `projects_list` | List GitHub Projects. | **READ** | Only retrieves project listings. |
| `projects_write` | Create, update, and manage project items. | **WRITE** | Creates and modifies project items. |

---

## Toolset: `secret_protection`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_secret_scanning_alert` | Get details of a secret scanning alert. | **READ** | Only retrieves alert details. |
| `list_secret_scanning_alerts` | List secret scanning alerts. | **READ** | Only retrieves alert listings. |

---

## Toolset: `security_advisories`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_global_security_advisory` | Get a global security advisory by ID. | **READ** | Only retrieves advisory details. |
| `list_global_security_advisories` | List global security advisories. | **READ** | Only retrieves advisory listings. |
| `list_org_repository_security_advisories` | List advisories for an organization's repositories. | **READ** | Only retrieves advisory listings. |
| `list_repository_security_advisories` | List advisories for a specific repository. | **READ** | Only retrieves advisory listings. |

---

## Toolset: `stargazers`

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `list_starred_repositories` | List repositories starred by the user. | **READ** | Only retrieves starred repo listings. |
| `star_repository` | Star (bookmark) a repository. | **WRITE** | Creates a star association. |
| `unstar_repository` | Remove a star from a repository. | **DELETE** | Removes the star association. |

---

## Toolset: `copilot` (Remote-only)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `assign_copilot_to_issue` | Assign Copilot coding agent to work on an issue. | **WRITE** | Triggers Copilot to begin autonomous work. |
| `create_pull_request_with_copilot` | Perform a task with Copilot coding agent. | **WRITE** | Creates a new draft PR and triggers code generation. |
| `get_copilot_job_status` | Check on Copilot coding agent's progress. | **READ** | Only retrieves the status of a Copilot job. |
| `request_copilot_review` | Request a Copilot code review for a PR. | **WRITE** | Triggers an automated code review. |

---

## Toolset: `copilot_spaces` (Remote-only)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `get_copilot_space` | Get details of a specific Copilot Space. | **READ** | Only retrieves space content and metadata. |
| `list_copilot_spaces` | List Copilot Spaces accessible to the user. | **READ** | Only retrieves space listings. |

---

## Toolset: `github_support_docs_search` (Remote-only, Beta)

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `github_support_docs_search` | Search GitHub product and support documentation. | **READ** | Only searches and retrieves documentation. |

---

## Auto-Approvable READ Tools

The following 56 tools are classified as READ-only and are candidates for auto-approval:

```json
{
  "server_name": "GitHub",
  "server_url_pattern": "api.githubcopilot.com",
  "read_tools": [
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
    "github_support_docs_search"
  ]
}
```

---

## Key Configuration Notes

- **Read-only mode**: GitHub MCP server supports `--read-only` flag / `X-MCP-Readonly` header that filters out all WRITE/DELETE/MIXED tools.
- **Toolset selection**: Use `X-MCP-Toolsets` header to control which toolsets are active.
- **Tool-specific selection**: Use `X-MCP-Tools` header with comma-separated tool names.
- **MIXED tools are excluded** from auto-approval since they can perform write/delete operations via the `method` parameter.

---

## Sources

- [GitHub MCP Server Repository](https://github.com/github/github-mcp-server)
- [GitHub Docs: Configuring Toolsets](https://docs.github.com/en/copilot/how-tos/provide-context/use-mcp/configure-toolsets)
- [GitHub Blog: Practical Guide to GitHub MCP Server](https://github.blog/ai-and-ml/generative-ai/a-practical-guide-on-how-to-use-the-github-mcp-server/)
