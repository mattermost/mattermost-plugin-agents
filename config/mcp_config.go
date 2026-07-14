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
	// ID is the immutable, globally unique stable identifier for this server,
	// assigned by the Agents plugin (model.NewId()). It is the policy identity
	// for ABAC: it survives Name/BaseURL edits and is never reused. It is NOT
	// used for OAuth keying (Name) or runtime tool origin (BaseURL) — those
	// identity systems are intentionally unchanged.
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

// ServerIDByOrigin maps a runtime ServerOrigin (llm.Tool.ServerOrigin, which is
// the server BaseURL for external servers) to the stable server ID. Servers with
// no assigned ID are omitted. On duplicate BaseURLs the last config entry wins,
// matching filterToolsByConfig's serverByOrigin overwrite semantics. Disabled
// servers are included: they produce no tools at runtime, and policy CRUD needs
// the mapping regardless of enablement.
func (c *MCPConfig) ServerIDByOrigin() map[string]string {
	out := make(map[string]string, len(c.Servers))
	for i := range c.Servers {
		if c.Servers[i].ID == "" {
			continue
		}
		out[c.Servers[i].BaseURL] = c.Servers[i].ID
	}
	return out
}

// OriginByServerID is the inverse of ServerIDByOrigin: stable ID -> current BaseURL.
func (c *MCPConfig) OriginByServerID() map[string]string {
	out := make(map[string]string, len(c.Servers))
	for i := range c.Servers {
		if c.Servers[i].ID == "" {
			continue
		}
		out[c.Servers[i].ID] = c.Servers[i].BaseURL
	}
	return out
}

// ReconcileMCPServerIDs carries stable IDs forward from prev onto entries in
// next that arrived without one (e.g. from webapp bundles predating the ID
// field). ID transfer between servers is worse than ID loss (a transferred
// policy attachment silently guards the wrong server; a lost one is
// recoverable), so matching never guesses:
//
//  1. Incoming entries that already carry an ID consume the matching prev
//     entry first, regardless of position, so an ID-less entry can never
//     steal an identity that an ID-bearing entry still holds.
//  2. Each ID-less entry then matches unconsumed prev entries by exact
//     (Name, BaseURL), else by Name with no BaseURL match elsewhere, else by
//     BaseURL with no Name match elsewhere.
//  3. Any ambiguity — multiple candidates in a tier, or Name and BaseURL
//     pointing at different prev entries — leaves the entry ID-less, treated
//     as NEW (normalizeAdminConfig assigns a fresh ID).
//
// Each prev entry is consumed at most once.
func ReconcileMCPServerIDs(next []MCPServerConfig, prev []MCPServerConfig) []MCPServerConfig {
	consumed := make([]bool, len(prev))

	// Pass 1: entries arriving with an ID keep it and consume the prev entry
	// holding that ID.
	for i := range next {
		if next[i].ID == "" {
			continue
		}
		for j := range prev {
			if !consumed[j] && prev[j].ID == next[i].ID {
				consumed[j] = true
				break
			}
		}
	}

	// Pass 2: ID-less entries match against remaining prev entries.
	for i := range next {
		if next[i].ID != "" {
			continue
		}

		var nameMatches, urlMatches, exactMatches []int
		for j := range prev {
			if consumed[j] || prev[j].ID == "" {
				continue
			}
			nameEq := prev[j].Name == next[i].Name
			urlEq := prev[j].BaseURL == next[i].BaseURL
			if nameEq {
				nameMatches = append(nameMatches, j)
			}
			if urlEq {
				urlMatches = append(urlMatches, j)
			}
			if nameEq && urlEq {
				exactMatches = append(exactMatches, j)
			}
		}

		match := -1
		switch {
		case len(exactMatches) == 1:
			match = exactMatches[0]
		case len(exactMatches) > 1:
			// Identical duplicates in prev: ambiguous.
		case len(nameMatches) == 1 && len(urlMatches) == 0:
			match = nameMatches[0]
		case len(nameMatches) == 0 && len(urlMatches) == 1:
			match = urlMatches[0]
			// Everything else (multiple candidates on an axis, or Name and
			// BaseURL matching different prev entries) is ambiguous: NEW.
		}
		if match >= 0 {
			next[i].ID = prev[match].ID
			consumed[match] = true
		}
	}
	return next
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
