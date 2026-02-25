// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"net/url"
	"strings"
)

// ApprovedMCPServer defines a pre-approved MCP server with classified tools.
// Tools listed in AutoApproveTools are READ-only and can be auto-executed
// without user approval in channel (multiplayer) contexts.
type ApprovedMCPServer struct {
	// ID is a stable unique identifier for custom servers. Used by the admin UI
	// for React keys and id-based update/delete. Omitted for built-in servers.
	ID string `json:"id,omitempty"`

	// Name is a human-readable identifier for this approved server.
	// Used as the merge key when combining built-in and user-defined servers.
	Name string `json:"name"`

	// URLPatterns are hostnames used for host-only matching of MCP server BaseURLs.
	// A configured MCP server matches if its BaseURL's parsed host equals a pattern
	// or is a subdomain of it (host == pattern or host ends with "."+pattern).
	// Path, query, fragment, and port are ignored; parse failures yield no match.
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

// IsToolAutoApproved checks if a given tool from a given MCP server URL
// is pre-approved for auto-execution (i.e., it's a READ-only tool on an approved server).
// Returns true only if:
// 1. An enabled approved server has a URLPattern matching the serverBaseURL
// 2. The toolName is in that server's AutoApproveTools list
func (c *ApprovedMCPServersConfig) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
	if c == nil || len(c.Servers) == 0 || serverBaseURL == "" || toolName == "" {
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

// GetAutoApprovedToolNames returns the names of all tools that would be auto-approved
// given their server origins. This is used to build the auto-run tools list for DMs.
func (c *ApprovedMCPServersConfig) GetAutoApprovedToolNames(tools []struct{ Name, ServerOrigin string }) []string {
	if c == nil || len(c.Servers) == 0 {
		return nil
	}
	var result []string
	for _, tool := range tools {
		if c.IsToolAutoApproved(tool.ServerOrigin, tool.Name) {
			result = append(result, tool.Name)
		}
	}
	return result
}

// matchesURLPattern checks if baseURL's host equals or is a subdomain of any pattern.
// Uses net/url to parse baseURL and extract hostname (port stripped). On parse failure, returns false.
func matchesURLPattern(baseURL string, patterns []string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if host == pattern || strings.HasSuffix(host, "."+pattern) {
			return true
		}
	}
	return false
}

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
