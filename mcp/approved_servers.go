// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import "strings"

// ApprovedMCPServer defines a pre-approved MCP server with classified tools.
// Tools listed in AutoApproveTools are READ-only and can be auto-executed
// without user approval in channel contexts.
type ApprovedMCPServer struct {
	// Name is a human-readable identifier for this approved server
	Name string `json:"name"`

	// URLPatterns are URL substrings used to match MCP server BaseURLs.
	// A configured MCP server matches if its BaseURL contains any of these patterns.
	URLPatterns []string `json:"url_patterns"`

	// AutoApproveTools lists tool names that are READ-only and can be
	// auto-executed without user approval in channel contexts.
	AutoApproveTools []string `json:"auto_approve_tools"`

	// Enabled controls whether this approved server config is active
	Enabled bool `json:"enabled"`
}

// ApprovedMCPServersConfig holds the resolved list of all approved server configurations.
type ApprovedMCPServersConfig struct {
	Servers []ApprovedMCPServer
}

// IsToolAutoApproved checks if a given tool from a given MCP server URL
// is pre-approved for auto-execution (i.e., it's a READ-only tool on a known server).
func (c *ApprovedMCPServersConfig) IsToolAutoApproved(serverBaseURL string, toolName string) bool {
	if c == nil || serverBaseURL == "" || toolName == "" {
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

// matchesURLPattern checks if a base URL contains any of the given patterns.
func matchesURLPattern(baseURL string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(baseURL, pattern) {
			return true
		}
	}
	return false
}
