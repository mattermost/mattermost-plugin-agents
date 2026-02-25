# Phase 1: Data Model & Configuration - Implementation Plan

## Overview

This plan covers the creation of the `ApprovedMCPServer` data model, the built-in approved server definitions for Atlassian/GitHub/Figma, extending the plugin configuration to include `ApprovedServers`, implementing merge logic for built-in + user-defined servers, and exhaustive unit tests for all of the above.

**Scope:**
- Step 1.1: Define `ApprovedMCPServer` Configuration Type
- Step 1.2: Add Built-in Approved Server Definitions
- Step 1.3: Extend Plugin Configuration with `ApprovedServers`
- Step 1.4: Merge Logic for Built-in + User-Defined Approved Servers
- Exhaustive unit tests

---

## Code Style Requirements (from CLAUDE.md)

- Go standard formatting (goimports)
- snake_case for file names
- License header: `// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.\n// See LICENSE.txt for license information.`
- Table-driven tests
- No mocking / no new testing libraries (use `github.com/stretchr/testify/require` which is already in use)
- Check all errors explicitly
- Descriptive variable and function names
- Small, focused functions
- Document all public APIs

---

## Step 1.1: Define `ApprovedMCPServer` Configuration Type

**New File: `mcp/approved_servers.go`**

This file defines the core data types and matching/lookup logic.

### Imports

```go
import (
    "strings"
)
```

### Type Definitions

```go
// ApprovedMCPServer defines a pre-approved MCP server with classified tools.
// Tools listed in AutoApproveTools are READ-only and can be auto-executed
// without user approval in channel (multiplayer) contexts.
type ApprovedMCPServer struct {
    // Name is a human-readable identifier for this approved server.
    // Used as the merge key when combining built-in and user-defined servers.
    Name string `json:"name"`

    // URLPatterns are URL substrings used to match MCP server BaseURLs.
    // A configured MCP server matches if its BaseURL contains any of these patterns.
    URLPatterns []string `json:"url_patterns"`

    // AutoApproveTools lists tool names that are READ-only and can be
    // auto-executed without user approval in channel contexts.
    AutoApproveTools []string `json:"auto_approve_tools"`

    // Enabled controls whether this approved server config is active.
    Enabled bool `json:"enabled"`
}

// ApprovedMCPServersConfig holds the merged list of all approved server configurations
// and provides lookup methods.
type ApprovedMCPServersConfig struct {
    Servers []ApprovedMCPServer
}
```

### Functions

#### `IsToolAutoApproved`

```go
// IsToolAutoApproved checks if a given tool from a given MCP server URL
// is pre-approved for auto-execution (i.e., it's a READ-only tool on an approved server).
// Returns true only if:
// 1. An enabled approved server has a URLPattern matching the serverBaseURL
// 2. The toolName is in that server's AutoApproveTools list
func (c *ApprovedMCPServersConfig) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
    if c == nil || len(c.Servers) == 0 {
        return false
    }
    for _, server := range c.Servers {
        if !server.Enabled {
            continue
        }
        if !matchesURLPattern(serverBaseURL, server.URLPatterns) {
            continue
        }
        for _, approvedTool := range server.AutoApproveTools {
            if approvedTool == toolName {
                return true
            }
        }
    }
    return false
}
```

#### `matchesURLPattern`

```go
// matchesURLPattern checks if a base URL contains any of the given pattern substrings.
func matchesURLPattern(baseURL string, patterns []string) bool {
    for _, pattern := range patterns {
        if pattern != "" && strings.Contains(baseURL, pattern) {
            return true
        }
    }
    return false
}
```

#### `MergeApprovedServers`

```go
// MergeApprovedServers combines built-in and user-defined approved server configs.
// User-defined configs with the same Name as a built-in one override the built-in version entirely.
// User-defined configs with new names are appended.
// The resulting order is: built-in servers (in original order, unless overridden), then new user-defined servers.
func MergeApprovedServers(builtin []ApprovedMCPServer, userDefined []ApprovedMCPServer) *ApprovedMCPServersConfig {
    if len(builtin) == 0 && len(userDefined) == 0 {
        return &ApprovedMCPServersConfig{}
    }

    // Build a set of user-defined server names for quick lookup
    userByName := make(map[string]ApprovedMCPServer, len(userDefined))
    for _, s := range userDefined {
        userByName[s.Name] = s
    }

    // Start with built-in servers, replacing any that have user overrides
    merged := make([]ApprovedMCPServer, 0, len(builtin)+len(userDefined))
    seen := make(map[string]bool, len(builtin)+len(userDefined))

    for _, b := range builtin {
        if override, exists := userByName[b.Name]; exists {
            merged = append(merged, override)
        } else {
            merged = append(merged, b)
        }
        seen[b.Name] = true
    }

    // Append user-defined servers that are not overrides of built-in ones
    for _, u := range userDefined {
        if !seen[u.Name] {
            merged = append(merged, u)
        }
    }

    return &ApprovedMCPServersConfig{Servers: merged}
}
```

---

## Step 1.2: Add Built-in Approved Server Definitions

**New File: `mcp/approved_servers_builtin.go`**

This file contains the three Mattermost-curated approved server definitions as compile-time defaults.

### Imports

None required (only uses types from the same package).

### Functions

#### `BuiltinApprovedServers`

```go
// BuiltinApprovedServers returns the Mattermost-curated list of approved MCP servers.
// These are compiled into the plugin and represent Mattermost's assessment of which
// tools are READ-only on well-known MCP servers.
func BuiltinApprovedServers() []ApprovedMCPServer {
    return []ApprovedMCPServer{
        atlassianApprovedServer(),
        githubApprovedServer(),
        figmaApprovedServer(),
    }
}
```

#### `atlassianApprovedServer`

```go
// atlassianApprovedServer returns the approved server definition for the Atlassian Remote MCP Server.
// Source: ATLASSIAN.md - 20 READ-only tools out of 29 total.
// Endpoint: https://mcp.atlassian.com/v1/mcp
func atlassianApprovedServer() ApprovedMCPServer {
    return ApprovedMCPServer{
        Name:        "Atlassian",
        URLPatterns: []string{"mcp.atlassian.com"},
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
        Enabled: true,
    }
}
```

#### `githubApprovedServer`

```go
// githubApprovedServer returns the approved server definition for the GitHub Remote MCP Server.
// Source: GITHUB.md - 54 READ-only tools out of 88 total.
// Endpoint: https://api.githubcopilot.com/mcp/
func githubApprovedServer() ApprovedMCPServer {
    return ApprovedMCPServer{
        Name:        "GitHub",
        URLPatterns: []string{"api.githubcopilot.com"},
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
        Enabled: true,
    }
}
```

#### `figmaApprovedServer`

```go
// figmaApprovedServer returns the approved server definition for the Figma Remote MCP Server.
// Source: FIGMA.md - 9 READ-only tools out of 13 total.
// Endpoint: https://mcp.figma.com/mcp
func figmaApprovedServer() ApprovedMCPServer {
    return ApprovedMCPServer{
        Name:        "Figma",
        URLPatterns: []string{"mcp.figma.com"},
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
        Enabled: true,
    }
}
```

---

## Step 1.3: Extend Plugin Configuration with `ApprovedServers`

### Modify `mcp/mcp.go`

Add the `ApprovedServers` field to the existing `Config` struct.

**Current struct (lines 40-46):**

```go
type Config struct {
    Enabled            bool                 `json:"enabled"`
    EnablePluginServer bool                 `json:"enablePluginServer"`
    Servers            []ServerConfig       `json:"servers"`
    EmbeddedServer     EmbeddedServerConfig `json:"embeddedServer"`
    IdleTimeoutMinutes int                  `json:"idleTimeoutMinutes"`
}
```

**Modified struct:**

```go
type Config struct {
    Enabled            bool                 `json:"enabled"`
    EnablePluginServer bool                 `json:"enablePluginServer"`
    Servers            []ServerConfig       `json:"servers"`
    EmbeddedServer     EmbeddedServerConfig `json:"embeddedServer"`
    IdleTimeoutMinutes int                  `json:"idleTimeoutMinutes"`
    ApprovedServers    []ApprovedMCPServer  `json:"approvedServers,omitempty"`
}
```

The `omitempty` tag ensures the field is not serialized when empty, maintaining backward compatibility with existing configurations that don't have this field.

### Modify `config/config.go`

Add a new accessor method `ApprovedMCPServers()` to the `Container` type. Place it after the existing `MCP()` method (line 113).

**New method:**

```go
// ApprovedMCPServers returns the merged approved MCP servers configuration,
// combining built-in Mattermost-curated servers with any user-defined overrides.
func (c *Container) ApprovedMCPServers() *mcp.ApprovedMCPServersConfig {
    cfg := c.cfg.Load()
    if cfg == nil {
        return mcp.MergeApprovedServers(mcp.BuiltinApprovedServers(), nil)
    }
    return mcp.MergeApprovedServers(mcp.BuiltinApprovedServers(), cfg.MCP.ApprovedServers)
}
```

**Note:** The import for the `mcp` package is already present in `config/config.go` (line 13: `"github.com/mattermost/mattermost-plugin-ai/mcp"`). No new imports needed.

---

## Step 1.4: Unit Tests

**New File: `mcp/approved_servers_test.go`**

All tests are table-driven as required by CLAUDE.md. Tests use `github.com/stretchr/testify/require` which is already used in the package's existing tests. No mocking libraries or new dependencies.

### Imports

```go
import (
    "testing"

    "github.com/stretchr/testify/require"
)
```

### Test: `TestIsToolAutoApproved`

```go
func TestIsToolAutoApproved(t *testing.T) {
    tests := []struct {
        name          string
        config        *ApprovedMCPServersConfig
        serverBaseURL string
        toolName      string
        expected      bool
    }{
        {
            name:          "nil config returns false",
            config:        nil,
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name:          "empty servers returns false",
            config:        &ApprovedMCPServersConfig{Servers: []ApprovedMCPServer{}},
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "approved tool from matching server returns true",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me", "list_branches"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      true,
        },
        {
            name: "non-approved tool from matching server returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me", "list_branches"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "create_repository",
            expected:      false,
        },
        {
            name: "tool from non-matching server returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "disabled server returns false even with matching tool",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          false,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "empty URL patterns returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "empty auto-approve tools returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "multiple URL patterns - second matches",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"github.example.com", "api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      true,
        },
        {
            name: "multiple servers - second matches",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "Atlassian",
                        URLPatterns:      []string{"mcp.atlassian.com"},
                        AutoApproveTools: []string{"search"},
                        Enabled:          true,
                    },
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      true,
        },
        {
            name: "empty server base URL returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name: "empty tool name returns false",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"api.githubcopilot.com"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "",
            expected:      false,
        },
        {
            name: "URL pattern is substring match not exact match",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "GitHub",
                        URLPatterns:      []string{"githubcopilot"},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      true,
        },
        {
            name: "empty string URL pattern is skipped",
            config: &ApprovedMCPServersConfig{
                Servers: []ApprovedMCPServer{
                    {
                        Name:             "Bad",
                        URLPatterns:      []string{""},
                        AutoApproveTools: []string{"get_me"},
                        Enabled:          true,
                    },
                },
            },
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := tt.config.IsToolAutoApproved(tt.serverBaseURL, tt.toolName)
            require.Equal(t, tt.expected, result)
        })
    }
}
```

### Test: `TestMatchesURLPattern`

```go
func TestMatchesURLPattern(t *testing.T) {
    tests := []struct {
        name     string
        baseURL  string
        patterns []string
        expected bool
    }{
        {
            name:     "exact domain match",
            baseURL:  "https://mcp.atlassian.com/v1/mcp",
            patterns: []string{"mcp.atlassian.com"},
            expected: true,
        },
        {
            name:     "no match",
            baseURL:  "https://example.com/mcp",
            patterns: []string{"mcp.atlassian.com"},
            expected: false,
        },
        {
            name:     "empty patterns",
            baseURL:  "https://mcp.atlassian.com/v1/mcp",
            patterns: []string{},
            expected: false,
        },
        {
            name:     "nil patterns",
            baseURL:  "https://mcp.atlassian.com/v1/mcp",
            patterns: nil,
            expected: false,
        },
        {
            name:     "empty base URL",
            baseURL:  "",
            patterns: []string{"mcp.atlassian.com"},
            expected: false,
        },
        {
            name:     "empty pattern string is skipped",
            baseURL:  "https://anything.com",
            patterns: []string{""},
            expected: false,
        },
        {
            name:     "multiple patterns first matches",
            baseURL:  "https://mcp.atlassian.com/v1/mcp",
            patterns: []string{"mcp.atlassian.com", "api.githubcopilot.com"},
            expected: true,
        },
        {
            name:     "multiple patterns second matches",
            baseURL:  "https://api.githubcopilot.com/mcp/",
            patterns: []string{"mcp.atlassian.com", "api.githubcopilot.com"},
            expected: true,
        },
        {
            name:     "partial substring match",
            baseURL:  "https://api.githubcopilot.com/mcp/",
            patterns: []string{"githubcopilot"},
            expected: true,
        },
        {
            name:     "case sensitive no match",
            baseURL:  "https://api.githubcopilot.com/mcp/",
            patterns: []string{"API.GITHUBCOPILOT.COM"},
            expected: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := matchesURLPattern(tt.baseURL, tt.patterns)
            require.Equal(t, tt.expected, result)
        })
    }
}
```

### Test: `TestMergeApprovedServers`

```go
func TestMergeApprovedServers(t *testing.T) {
    tests := []struct {
        name        string
        builtin     []ApprovedMCPServer
        userDefined []ApprovedMCPServer
        validate    func(t *testing.T, result *ApprovedMCPServersConfig)
    }{
        {
            name:        "both empty returns empty config",
            builtin:     nil,
            userDefined: nil,
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.NotNil(t, result)
                require.Empty(t, result.Servers)
            },
        },
        {
            name: "builtin only returns builtin servers",
            builtin: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
                {Name: "Atlassian", URLPatterns: []string{"atlassian.com"}, AutoApproveTools: []string{"search"}, Enabled: true},
            },
            userDefined: nil,
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 2)
                require.Equal(t, "GitHub", result.Servers[0].Name)
                require.Equal(t, "Atlassian", result.Servers[1].Name)
            },
        },
        {
            name:    "user defined only returns user servers",
            builtin: nil,
            userDefined: []ApprovedMCPServer{
                {Name: "Custom", URLPatterns: []string{"custom.com"}, AutoApproveTools: []string{"my_tool"}, Enabled: true},
            },
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 1)
                require.Equal(t, "Custom", result.Servers[0].Name)
            },
        },
        {
            name: "user override replaces builtin with same name",
            builtin: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me", "list_branches"}, Enabled: true},
            },
            userDefined: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github-enterprise.myco.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
            },
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 1)
                require.Equal(t, "GitHub", result.Servers[0].Name)
                // User version should win: different URL pattern and fewer tools
                require.Equal(t, []string{"github-enterprise.myco.com"}, result.Servers[0].URLPatterns)
                require.Equal(t, []string{"get_me"}, result.Servers[0].AutoApproveTools)
            },
        },
        {
            name: "user can disable a builtin server",
            builtin: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
            },
            userDefined: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: false},
            },
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 1)
                require.Equal(t, "GitHub", result.Servers[0].Name)
                require.False(t, result.Servers[0].Enabled)
            },
        },
        {
            name: "user adds new server alongside builtin",
            builtin: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
            },
            userDefined: []ApprovedMCPServer{
                {Name: "Internal API", URLPatterns: []string{"internal.myco.com"}, AutoApproveTools: []string{"get_status"}, Enabled: true},
            },
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 2)
                // Builtin first, then new user-defined
                require.Equal(t, "GitHub", result.Servers[0].Name)
                require.Equal(t, "Internal API", result.Servers[1].Name)
            },
        },
        {
            name: "user overrides one builtin and adds a new one",
            builtin: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me"}, Enabled: true},
                {Name: "Atlassian", URLPatterns: []string{"atlassian.com"}, AutoApproveTools: []string{"search"}, Enabled: true},
            },
            userDefined: []ApprovedMCPServer{
                {Name: "GitHub", URLPatterns: []string{"github.com"}, AutoApproveTools: []string{"get_me", "extra_tool"}, Enabled: true},
                {Name: "Custom", URLPatterns: []string{"custom.com"}, AutoApproveTools: []string{"do_thing"}, Enabled: true},
            },
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.Len(t, result.Servers, 3)
                // Order: GitHub (overridden), Atlassian (unchanged), Custom (new)
                require.Equal(t, "GitHub", result.Servers[0].Name)
                require.Equal(t, []string{"get_me", "extra_tool"}, result.Servers[0].AutoApproveTools)
                require.Equal(t, "Atlassian", result.Servers[1].Name)
                require.Equal(t, "Custom", result.Servers[2].Name)
            },
        },
        {
            name:        "empty builtin empty user returns empty",
            builtin:     []ApprovedMCPServer{},
            userDefined: []ApprovedMCPServer{},
            validate: func(t *testing.T, result *ApprovedMCPServersConfig) {
                require.NotNil(t, result)
                require.Empty(t, result.Servers)
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := MergeApprovedServers(tt.builtin, tt.userDefined)
            require.NotNil(t, result)
            tt.validate(t, result)
        })
    }
}
```

### Test: `TestBuiltinApprovedServers`

```go
func TestBuiltinApprovedServers(t *testing.T) {
    servers := BuiltinApprovedServers()

    require.Len(t, servers, 3, "should have exactly 3 built-in approved servers")

    t.Run("Atlassian server", func(t *testing.T) {
        s := servers[0]
        require.Equal(t, "Atlassian", s.Name)
        require.Equal(t, []string{"mcp.atlassian.com"}, s.URLPatterns)
        require.True(t, s.Enabled)
        require.Len(t, s.AutoApproveTools, 20, "Atlassian should have 20 READ tools")

        // Verify a few key tools are present
        expectedTools := []string{"search", "fetch", "getJiraIssue", "getConfluencePage", "searchJiraIssuesUsingJql"}
        for _, tool := range expectedTools {
            require.Contains(t, s.AutoApproveTools, tool, "Atlassian should contain tool: %s", tool)
        }

        // Verify WRITE tools are NOT present
        writeTools := []string{"createConfluencePage", "updateConfluencePage", "createJiraIssue", "editJiraIssue", "transitionJiraIssue"}
        for _, tool := range writeTools {
            require.NotContains(t, s.AutoApproveTools, tool, "Atlassian should not contain WRITE tool: %s", tool)
        }
    })

    t.Run("GitHub server", func(t *testing.T) {
        s := servers[1]
        require.Equal(t, "GitHub", s.Name)
        require.Equal(t, []string{"api.githubcopilot.com"}, s.URLPatterns)
        require.True(t, s.Enabled)
        require.Len(t, s.AutoApproveTools, 54, "GitHub should have 54 READ tools")

        // Verify a few key tools are present
        expectedTools := []string{"get_me", "list_branches", "issue_read", "pull_request_read", "search_code"}
        for _, tool := range expectedTools {
            require.Contains(t, s.AutoApproveTools, tool, "GitHub should contain tool: %s", tool)
        }

        // Verify WRITE/MIXED/DELETE tools are NOT present
        excludedTools := []string{"create_branch", "create_repository", "delete_file", "push_files", "merge_pull_request", "pull_request_review_write", "actions_run_trigger"}
        for _, tool := range excludedTools {
            require.NotContains(t, s.AutoApproveTools, tool, "GitHub should not contain non-READ tool: %s", tool)
        }
    })

    t.Run("Figma server", func(t *testing.T) {
        s := servers[2]
        require.Equal(t, "Figma", s.Name)
        require.Equal(t, []string{"mcp.figma.com"}, s.URLPatterns)
        require.True(t, s.Enabled)
        require.Len(t, s.AutoApproveTools, 9, "Figma should have 9 READ tools")

        // Verify a few key tools are present
        expectedTools := []string{"get_design_context", "get_screenshot", "whoami", "get_figjam"}
        for _, tool := range expectedTools {
            require.Contains(t, s.AutoApproveTools, tool, "Figma should contain tool: %s", tool)
        }

        // Verify WRITE tools are NOT present
        writeTools := []string{"generate_diagram", "generate_figma_design", "add_code_connect_map", "send_code_connect_mappings"}
        for _, tool := range writeTools {
            require.NotContains(t, s.AutoApproveTools, tool, "Figma should not contain WRITE tool: %s", tool)
        }
    })
}
```

### Test: `TestBuiltinApprovedServersIntegration`

This test verifies that the built-in servers work correctly with `IsToolAutoApproved`.

```go
func TestBuiltinApprovedServersIntegration(t *testing.T) {
    config := MergeApprovedServers(BuiltinApprovedServers(), nil)

    tests := []struct {
        name          string
        serverBaseURL string
        toolName      string
        expected      bool
    }{
        {
            name:          "GitHub READ tool auto-approved",
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "get_me",
            expected:      true,
        },
        {
            name:          "GitHub WRITE tool not auto-approved",
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "create_repository",
            expected:      false,
        },
        {
            name:          "Atlassian READ tool auto-approved",
            serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
            toolName:      "getJiraIssue",
            expected:      true,
        },
        {
            name:          "Atlassian WRITE tool not auto-approved",
            serverBaseURL: "https://mcp.atlassian.com/v1/mcp",
            toolName:      "createJiraIssue",
            expected:      false,
        },
        {
            name:          "Figma READ tool auto-approved",
            serverBaseURL: "https://mcp.figma.com/mcp",
            toolName:      "get_design_context",
            expected:      true,
        },
        {
            name:          "Figma WRITE tool not auto-approved",
            serverBaseURL: "https://mcp.figma.com/mcp",
            toolName:      "generate_diagram",
            expected:      false,
        },
        {
            name:          "unknown server not auto-approved",
            serverBaseURL: "https://unknown-mcp-server.com/mcp",
            toolName:      "get_me",
            expected:      false,
        },
        {
            name:          "unknown tool on known server not auto-approved",
            serverBaseURL: "https://api.githubcopilot.com/mcp/",
            toolName:      "nonexistent_tool",
            expected:      false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := config.IsToolAutoApproved(tt.serverBaseURL, tt.toolName)
            require.Equal(t, tt.expected, result)
        })
    }
}
```

---

## File Summary

| File | Action | Description |
|------|--------|-------------|
| `mcp/approved_servers.go` | **NEW** | `ApprovedMCPServer` type, `ApprovedMCPServersConfig` type, `IsToolAutoApproved()`, `matchesURLPattern()`, `MergeApprovedServers()` |
| `mcp/approved_servers_builtin.go` | **NEW** | `BuiltinApprovedServers()`, `atlassianApprovedServer()`, `githubApprovedServer()`, `figmaApprovedServer()` |
| `mcp/approved_servers_test.go` | **NEW** | `TestIsToolAutoApproved`, `TestMatchesURLPattern`, `TestMergeApprovedServers`, `TestBuiltinApprovedServers`, `TestBuiltinApprovedServersIntegration` |
| `mcp/mcp.go` | **MODIFY** | Add `ApprovedServers []ApprovedMCPServer` field to `Config` struct |
| `config/config.go` | **MODIFY** | Add `ApprovedMCPServers()` accessor method to `Container` |

---

## Implementation Order

1. Create `mcp/approved_servers.go` with types and functions
2. Create `mcp/approved_servers_builtin.go` with built-in definitions
3. Modify `mcp/mcp.go` to add `ApprovedServers` to `Config`
4. Modify `config/config.go` to add `ApprovedMCPServers()` accessor
5. Create `mcp/approved_servers_test.go` with all unit tests
6. Run `make check-style-fix` to fix any formatting
7. Run `go test -v ./mcp/ -run TestIsToolAutoApproved` and all other test functions
8. Run `make test` to verify nothing is broken

---

## Verification Checklist

- [ ] All new files have the license header
- [ ] All file names use snake_case
- [ ] All tests are table-driven
- [ ] No mocking or new testing libraries introduced
- [ ] All public functions are documented with comments
- [ ] `ApprovedMCPServer` JSON tags match the design in PLAN.md
- [ ] All 20 Atlassian READ tools are listed
- [ ] All 54 GitHub READ tools are listed (see note below)
- [ ] All 9 Figma READ tools are listed
- [ ] `MergeApprovedServers` preserves ordering (builtin first, user-defined appended)
- [ ] `IsToolAutoApproved` handles nil receiver, empty config, disabled servers
- [ ] `matchesURLPattern` skips empty pattern strings
- [ ] `Config.ApprovedServers` uses `omitempty` for backward compatibility

---

## Important Note on GitHub Tool Count

The master PLAN.md summary text says "56 READ tools" for GitHub, but careful counting of both the GITHUB.md table classifications and the JSON block reveals exactly **54** READ-only tools. The "56" in the summary text is a documentation error. The implementation uses the authoritative list of **54** tools extracted from the GITHUB.md companion doc tables and JSON block (both agree).

The `githubApprovedServer()` function in Step 1.2 lists exactly 54 tool strings, and the test in `TestBuiltinApprovedServers` asserts `require.Len(t, s.AutoApproveTools, 54, ...)`.

---

## Implementation Completion Summary

**Completed by:** implementer-1

**Files created:**
- `mcp/approved_servers.go` - Core types (`ApprovedMCPServer`, `ApprovedMCPServersConfig`), `IsToolAutoApproved()`, `matchesURLPattern()`, `MergeApprovedServers()`
- `mcp/approved_servers_builtin.go` - `BuiltinApprovedServers()` returning Atlassian (20 tools), GitHub (54 tools), Figma (9 tools)
- `mcp/approved_servers_test.go` - 5 table-driven test functions, 40 total test cases

**Files modified:**
- `mcp/mcp.go` - Added `ApprovedServers []ApprovedMCPServer` field with `json:"approvedServers,omitempty"` to `Config` struct
- `config/config.go` - Added `ApprovedMCPServers()` accessor method to `Container` that merges built-in + user-defined servers

**Test results:** All 40 test cases pass. All files pass goimports formatting. Config package compiles cleanly.
