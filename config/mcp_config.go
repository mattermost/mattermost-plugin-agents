// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

const (
	MCPToolPolicyAsk               = "ask"
	MCPToolPolicyAutoRunInDM       = "auto_run_in_dm"
	MCPToolPolicyAutoRunEverywhere = "auto_run_everywhere"
)

// MCPToolConfig represents per-tool configuration for an MCP server.
type MCPToolConfig struct {
	Name    string `json:"name"`
	Policy  string `json:"policy"` // "auto_run_in_dm" | "auto_run_everywhere" | "ask"
	Enabled bool   `json:"enabled"`
}

// IsToolPolicyAutoRunInDM returns true when the policy auto-executes in a DM
// without user approval. Both auto_run_in_dm (DM-only) and auto_run_everywhere
// satisfy this; the difference is whether the tool also auto-runs in channels.
func IsToolPolicyAutoRunInDM(policy string) bool {
	return policy == MCPToolPolicyAutoRunInDM || policy == MCPToolPolicyAutoRunEverywhere
}

// IsToolPolicyAutoRunEverywhere returns true only for policies that auto-execute
// without approval regardless of conversation context (DM or channel).
func IsToolPolicyAutoRunEverywhere(policy string) bool {
	return policy == MCPToolPolicyAutoRunEverywhere
}

// MCPEmbeddedServerConfig contains configuration for the embedded MCP server
type MCPEmbeddedServerConfig struct {
	Enabled     bool            `json:"enabled"`
	ToolConfigs []MCPToolConfig `json:"tool_configs,omitempty"`
}

// MCPConfig contains the configuration for the MCP servers
type MCPConfig struct {
	Enabled            bool                    `json:"enabled"`
	EnablePluginServer bool                    `json:"enablePluginServer"`
	Servers            []MCPServerConfig       `json:"servers"`
	PluginServers      []PluginServerConfig    `json:"plugin_servers,omitempty"`
	EmbeddedServer     MCPEmbeddedServerConfig `json:"embeddedServer"`
	IdleTimeoutMinutes int                     `json:"idleTimeoutMinutes"`
}

// MCPServerConfig contains the configuration for a single MCP server
type MCPServerConfig struct {
	Name         string            `json:"name"`
	Enabled      bool              `json:"enabled"`
	BaseURL      string            `json:"baseURL"`
	Headers      map[string]string `json:"headers,omitempty"`
	ClientID     string            `json:"clientID,omitempty"`
	ClientSecret string            `json:"clientSecret,omitempty"`
	ToolConfigs  []MCPToolConfig   `json:"tool_configs,omitempty"`
}

// GetToolPolicy returns the policy and enabled state for a tool.
// If the receiver is nil or the tool name is empty, it returns ("ask", false).
// If no matching config entry exists, it returns ("ask", true) — unconfigured
// tools default to enabled with ask policy. Invalid or empty policies are
// normalized to "ask". When duplicate entries exist the last matching entry wins.
func (s *MCPServerConfig) GetToolPolicy(toolName string) (string, bool) {
	if s == nil || toolName == "" {
		return MCPToolPolicyAsk, false
	}

	if !s.Enabled {
		return MCPToolPolicyAsk, false
	}

	found := false
	policy := MCPToolPolicyAsk
	enabled := false

	for _, tc := range s.ToolConfigs {
		if tc.Name == toolName {
			found = true
			policy = tc.Policy
			enabled = tc.Enabled
		}
	}

	if !found {
		return MCPToolPolicyAsk, true
	}

	if !IsToolPolicyAutoRunInDM(policy) && policy != MCPToolPolicyAsk {
		policy = MCPToolPolicyAsk
	}

	return policy, enabled
}

// IsToolAutoRunInDM returns true when the tool is enabled and configured to
// auto-run in a DM (either the DM-only or everywhere policy).
func (s *MCPServerConfig) IsToolAutoRunInDM(toolName string) bool {
	policy, enabled := s.GetToolPolicy(toolName)
	return IsToolPolicyAutoRunInDM(policy) && enabled
}

// PluginServerConfig describes an MCP server registered by another plugin.
type PluginServerConfig struct {
	PluginID       string          `json:"plugin_id"`
	Name           string          `json:"name"`
	Path           string          `json:"path"`
	Enabled        bool            `json:"enabled"`
	ExposeExternal bool            `json:"expose_external"`
	ToolConfigs    []MCPToolConfig `json:"tool_configs,omitempty"`
}
