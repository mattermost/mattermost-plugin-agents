// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

// ToolPolicyChecker looks up the per-tool policy for a given MCP server/tool.
type ToolPolicyChecker interface {
	GetToolPolicy(serverBaseURL string, toolName string) (policy string, enabled bool)
}

// ToolPolicyFunc is a function adapter that implements ToolPolicyChecker.
type ToolPolicyFunc func(serverBaseURL string, toolName string) (string, bool)

// GetToolPolicy implements ToolPolicyChecker.
func (f ToolPolicyFunc) GetToolPolicy(serverBaseURL string, toolName string) (string, bool) {
	return f(serverBaseURL, toolName)
}
