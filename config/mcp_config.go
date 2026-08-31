// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"
)

const (
	MCPToolPolicyAsk               = "ask"
	MCPToolPolicyAutoRunInDM       = "auto_run_in_dm"
	MCPToolPolicyAutoRunEverywhere = "auto_run_everywhere"

	// MCPEmbeddedServerOrigin is the runtime ServerOrigin for the embedded
	// Mattermost MCP server (matches mcp.EmbeddedClientKey).
	MCPEmbeddedServerOrigin = "embedded://mattermost"
)

// PluginServerOrigin returns the runtime ServerOrigin for a plugin-registered
// MCP server. Identity is keyed by PluginID only — Path is not part of the origin.
func PluginServerOrigin(pluginID string) string {
	return "plugin://" + pluginID
}

// MCPToolConfig represents per-tool configuration for an MCP server.
type MCPToolConfig struct {
	Name                         string `json:"name"`
	Policy                       string `json:"policy"` // "auto_run_in_dm" | "auto_run_everywhere" | "ask"
	Enabled                      bool   `json:"enabled"`
	RetrievalDescriptionOverride string `json:"retrieval_description_override,omitempty"`
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
	// ID is the immutable plugin-assigned ABAC policy identity. It survives
	// enablement/tool-config edits and is never reused. Runtime tool origin
	// is MCPEmbeddedServerOrigin.
	ID          string          `json:"id,omitempty"`
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
	// ID is the immutable plugin-assigned ABAC policy identity: it survives
	// Name/BaseURL edits and is never reused. OAuth keying (Name) and runtime
	// tool origin (BaseURL) are separate identity systems.
	ID           string            `json:"id,omitempty"`
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

// ServerIDByOrigin maps a runtime ServerOrigin to the stable server ID.
// Origins are BaseURL (external), MCPEmbeddedServerOrigin (embedded), or
// PluginServerOrigin(PluginID) (plugin). ID-less servers are omitted; on
// duplicate origins the last entry wins (matching filterToolsByConfig);
// disabled servers are included because policy CRUD needs the mapping
// regardless of enablement.
func (c *MCPConfig) ServerIDByOrigin() map[string]string {
	out := make(map[string]string, len(c.Servers)+len(c.PluginServers)+1)
	for i := range c.Servers {
		if c.Servers[i].ID == "" {
			continue
		}
		out[c.Servers[i].BaseURL] = c.Servers[i].ID
	}
	if c.EmbeddedServer.ID != "" {
		out[MCPEmbeddedServerOrigin] = c.EmbeddedServer.ID
	}
	for i := range c.PluginServers {
		if c.PluginServers[i].ID == "" || c.PluginServers[i].PluginID == "" {
			continue
		}
		out[PluginServerOrigin(c.PluginServers[i].PluginID)] = c.PluginServers[i].ID
	}
	return out
}

// OriginByServerID is the inverse of ServerIDByOrigin: stable ID -> current origin.
func (c *MCPConfig) OriginByServerID() map[string]string {
	out := make(map[string]string, len(c.Servers)+len(c.PluginServers)+1)
	for i := range c.Servers {
		if c.Servers[i].ID == "" {
			continue
		}
		out[c.Servers[i].ID] = c.Servers[i].BaseURL
	}
	if c.EmbeddedServer.ID != "" {
		out[c.EmbeddedServer.ID] = MCPEmbeddedServerOrigin
	}
	for i := range c.PluginServers {
		if c.PluginServers[i].ID == "" || c.PluginServers[i].PluginID == "" {
			continue
		}
		out[c.PluginServers[i].ID] = PluginServerOrigin(c.PluginServers[i].PluginID)
	}
	return out
}

// ErrMCPServerIDConflict is returned when the config being written contains
// duplicate non-empty MCP server IDs, or when an incoming embedded server ID
// differs from the stored ID.
var ErrMCPServerIDConflict = errors.New("MCP server identity conflict")

// ReconcileMCPConfigIDs is the single entry point for MCP identity on config
// save. It:
//
//  1. Treats PluginServers as server-owned: prev is carried forward
//     unconditionally. Full config save never accepts client-provided plugin
//     rows (bridge register/unregister and PUT /admin/mcp/plugin-servers/:id
//     are the only writers).
//  2. Copies the embedded server ID from prev when next is empty. A
//     non-empty next ID that differs from a non-empty prev ID is a conflict.
//     Empty remote IDs stay empty for the caller to mint.
//  3. Rejects any ID shared by two MCP resources of any kind on the config
//     being written. Duplicate IDs in prev alone are not a conflict.
func ReconcileMCPConfigIDs(next, prev MCPConfig) (MCPConfig, error) {
	next.PluginServers = append([]PluginServerConfig(nil), prev.PluginServers...)

	embedded, err := ReconcileEmbeddedMCPServerID(next.EmbeddedServer, prev.EmbeddedServer)
	if err != nil {
		return MCPConfig{}, err
	}
	next.EmbeddedServer = embedded

	if err := ValidateMCPServerIDUniqueness(next); err != nil {
		return MCPConfig{}, err
	}
	return next, nil
}

// ValidateMCPServerIDUniqueness returns ErrMCPServerIDConflict when any two
// MCP resources (remote, embedded, or plugin) share the same non-empty ID.
func ValidateMCPServerIDUniqueness(cfg MCPConfig) error {
	seen := make(map[string]string, len(cfg.Servers)+len(cfg.PluginServers)+1)
	claim := func(id, label string) error {
		if id == "" {
			return nil
		}
		if other, ok := seen[id]; ok {
			return fmt.Errorf("%w: %s and %s share ID %q", ErrMCPServerIDConflict, other, label, id)
		}
		seen[id] = label
		return nil
	}
	for i := range cfg.Servers {
		label := fmt.Sprintf("remote server %q", cfg.Servers[i].Name)
		if err := claim(cfg.Servers[i].ID, label); err != nil {
			return err
		}
	}
	if err := claim(cfg.EmbeddedServer.ID, "embedded server"); err != nil {
		return err
	}
	for i := range cfg.PluginServers {
		label := fmt.Sprintf("plugin server %q", cfg.PluginServers[i].PluginID)
		if err := claim(cfg.PluginServers[i].ID, label); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileEmbeddedMCPServerID copies prev.ID onto an ID-less next (one
// embedded server, not a list heuristic). A non-empty next ID that differs
// from a non-empty prev ID is a conflict. Caller-chosen IDs on a previously
// ID-less embedded server are kept; both empty leaves next ID-less for the
// caller to mint.
func ReconcileEmbeddedMCPServerID(next, prev MCPEmbeddedServerConfig) (MCPEmbeddedServerConfig, error) {
	if next.ID == "" {
		next.ID = prev.ID
		return next, nil
	}
	if prev.ID != "" && next.ID != prev.ID {
		return MCPEmbeddedServerConfig{}, fmt.Errorf("%w: embedded server carries ID %q that differs from the stored ID %q", ErrMCPServerIDConflict, next.ID, prev.ID)
	}
	return next, nil
}

// PluginServerConfig describes an MCP server registered by another plugin.
type PluginServerConfig struct {
	// ID is the immutable plugin-assigned ABAC policy identity. Identity is
	// keyed by PluginID: it survives re-registration and Path changes and is
	// never reused. Runtime tool origin is PluginServerOrigin(PluginID).
	ID             string          `json:"id,omitempty"`
	PluginID       string          `json:"plugin_id"`
	Name           string          `json:"name"`
	Path           string          `json:"path"`
	Enabled        bool            `json:"enabled"`
	ExposeExternal bool            `json:"expose_external"`
	ToolConfigs    []MCPToolConfig `json:"tool_configs,omitempty"`
}
