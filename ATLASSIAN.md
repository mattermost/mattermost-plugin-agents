# Atlassian Remote MCP Server - Tool Classification

## Overview

The **Atlassian Rovo MCP Server** is Atlassian's official cloud-hosted remote MCP server, available at `https://mcp.atlassian.com/v1/mcp`. It provides secure access to **Jira**, **Confluence**, and **Compass** data via OAuth 2.1 authentication, respecting existing user permissions. Generally available since September 9, 2025.

**Endpoint:** `https://mcp.atlassian.com/v1/mcp` (Streamable HTTP)
**Repository:** [atlassian/atlassian-mcp-server](https://github.com/atlassian/atlassian-mcp-server)

---

## Tool Classification Summary

| Classification | Count | Percentage |
|---------------|-------|------------|
| **READ**      | 20    | 69.0%      |
| **WRITE**     | 9     | 31.0%      |
| **DELETE**     | 0     | 0%         |
| **MIXED**     | 0     | 0%         |
| **Total**     | **29** | 100%      |

---

## Rovo / Shared Platform Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `search` | Rovo semantic search across Jira and Confluence. Interprets natural language queries to find relevant issues, pages, and services. | **READ** | Only retrieves and returns search results. |
| `fetch` | Rovo fetch retrieves specific details or content from Jira or Confluence using a simple prompt. | **READ** | Only retrieves and returns existing content. |

---

## General / Account Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `atlassianUserInfo` | Get current authenticated user information (display name, email, account ID). | **READ** | Returns the authenticated user's profile only. |
| `getAccessibleAtlassianResources` | Get the cloud ID(s) for accessible Atlassian sites. | **READ** | Returns metadata about accessible resources. |

---

## Confluence Tools

### Confluence - READ Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `getConfluenceSpaces` | Get a list of spaces from Confluence, optionally filtered. | **READ** | Retrieves a list of Confluence spaces. |
| `getConfluencePage` | Get a specific Confluence page or live doc by page ID, including body content. | **READ** | Retrieves a single page's content and metadata. |
| `getPagesInConfluenceSpace` | Get all pages within a specific Confluence space. | **READ** | Lists pages in a given space. |
| `getConfluencePageAncestors` | Get all ancestor (parent) pages of a specific Confluence page. | **READ** | Retrieves the parent page chain. |
| `getConfluencePageDescendants` | Get all descendant (child) pages of a specific Confluence page. | **READ** | Retrieves the child page tree. |
| `getConfluencePageFooterComments` | Get footer comments for a Confluence page (top-level only). | **READ** | Retrieves existing footer comments. |
| `getConfluencePageInlineComments` | Get inline comments for a Confluence page or blog post. | **READ** | Retrieves existing inline comments. |
| `searchConfluenceUsingCql` | Search content in Confluence using CQL (Confluence Query Language). | **READ** | Executes a CQL search query and returns matches. |

### Confluence - WRITE Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `createConfluencePage` | Create a new page in Confluence (regular page or live doc). | **WRITE** | Creates a new Confluence page. |
| `updateConfluencePage` | Update an existing Confluence page or live doc. | **WRITE** | Modifies an existing Confluence page. |
| `createConfluenceFooterComment` | Create a footer comment on a Confluence page or blog post. | **WRITE** | Creates a new comment. |
| `createConfluenceInlineComment` | Create an inline comment on a Confluence page or blog post. | **WRITE** | Creates a new inline comment. |

---

## Jira Tools

### Jira - READ Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `getJiraIssue` | Get the details of a Jira issue by issue ID or key (e.g., "PROJ-123"). | **READ** | Retrieves a single issue's full details. |
| `getJiraIssueRemoteIssueLinks` | Get remote issue links of an existing Jira issue. | **READ** | Retrieves external links attached to an issue. |
| `getTransitionsForJiraIssue` | Get the available status transitions for an existing Jira issue. | **READ** | Retrieves the possible workflow transitions. |
| `getVisibleJiraProjects` | Get visible Jira projects for the user. | **READ** | Lists accessible projects. |
| `getJiraProjectIssueTypesMetadata` | Get issue type metadata for a specified project. | **READ** | Retrieves project-level issue type configuration. |
| `getJiraIssueTypeMetaWithFields` | Get detailed issue type metadata including field definitions. | **READ** | Retrieves field-level metadata for an issue type. |
| `lookupJiraAccountId` | Look up account IDs of existing users in Jira. | **READ** | Searches for user accounts. |
| `searchJiraIssuesUsingJql` | Search Jira issues using JQL (Jira Query Language). | **READ** | Executes a JQL search and returns matching issues. |

### Jira - WRITE Tools

| Tool Name | Description | Classification | Justification |
|-----------|-------------|:-:|---------------|
| `createJiraIssue` | Create a new Jira issue in a given project. | **WRITE** | Creates a new Jira issue. |
| `editJiraIssue` | Update the details of an existing Jira issue. | **WRITE** | Modifies an existing issue's field values. |
| `transitionJiraIssue` | Transition an existing Jira issue to a new status. | **WRITE** | Changes the workflow status of an issue. |
| `addCommentToJiraIssue` | Add a comment to an existing Jira issue. | **WRITE** | Creates a new comment on an issue. |
| `addWorklogToJiraIssue` | Add a worklog entry (time tracking) to a Jira issue. | **WRITE** | Creates a new worklog record on an issue. |

---

## Auto-Approvable READ Tools

The following 20 tools are classified as READ-only and are candidates for auto-approval:

```json
{
  "server_name": "Atlassian",
  "server_url_pattern": "mcp.atlassian.com",
  "read_tools": [
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
    "searchJiraIssuesUsingJql"
  ]
}
```

---

## Key Observations

1. **No DELETE tools**: The Atlassian MCP server does not expose any delete operations. This is a deliberate security design choice.
2. **No attachment support**: The MCP server cannot download or read binary attachments from Jira issues.
3. **`cloudId` dependency**: Most tools require a `cloudId` parameter, obtained first via `getAccessibleAtlassianResources`.
4. **Compass & JSM tools**: Additional tools exist for Compass (service catalog) and JSM (service management) but their specific programmatic names are not yet publicly documented.

---

## Sources

- [Atlassian Rovo MCP Server - Official Product Page](https://www.atlassian.com/platform/remote-mcp-server)
- [Atlassian Rovo MCP Server - Supported Tools](https://support.atlassian.com/atlassian-rovo-mcp-server/docs/supported-tools/)
- [GitHub: atlassian/atlassian-mcp-server](https://github.com/atlassian/atlassian-mcp-server)
