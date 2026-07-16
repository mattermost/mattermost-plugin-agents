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

// ServerIDByOrigin maps a runtime ServerOrigin (BaseURL for external servers)
// to the stable server ID. ID-less servers are omitted; on duplicate BaseURLs
// the last entry wins (matching filterToolsByConfig); disabled servers are
// included because policy CRUD needs the mapping regardless of enablement.
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

// ErrMCPServerIDConflict is the base error for every identity problem
// ReconcileMCPServerIDs can detect; callers map it to a client error (stale
// or corrupt admin console payload).
var ErrMCPServerIDConflict = errors.New("MCP server identity conflict")

// ReconcileMCPServerIDs carries stable IDs forward from prev onto ID-less
// entries in next (e.g. from webapp bundles predating the ID field). Server
// IDs are ABAC policy identities, so identity mistakes are never papered
// over: minting a fresh ID for an existing server detaches its policy (which
// fails open). Matching runs in global phases over the whole payload so the
// outcome cannot depend on payload order; anything suspicious errors:
//
//  1. Explicit-ID claims: each must reference a distinct existing prev entry.
//  2. Exact (Name, BaseURL) claims against the full stored list; claiming an
//     already-claimed prev entry is an error, never a silent fallback.
//  3. Weak claims against the unclaimed remainder: by unique Name or unique
//     BaseURL; any ambiguity or double-claim is an error.
//  4. Entries matching nothing stay ID-less; the caller mints a fresh ID.
//
// Each prev entry is claimed at most once across all phases.
func ReconcileMCPServerIDs(next []MCPServerConfig, prev []MCPServerConfig) ([]MCPServerConfig, error) {
	// Duplicate stored IDs mean the persisted config is already corrupt;
	// keeping the last row would let two servers share one policy ID.
	prevByID := make(map[string]int, len(prev))
	for j := range prev {
		id := prev[j].ID
		if id == "" {
			continue
		}
		if _, dup := prevByID[id]; dup {
			return nil, fmt.Errorf("%w: stored configuration contains two servers with ID %q", ErrMCPServerIDConflict, id)
		}
		prevByID[id] = j
	}

	claimed := make([]bool, len(prev))

	// Phase 1: entries arriving with an ID claim the prev entry holding it.
	seenIncoming := make(map[string]bool, len(next))
	for i := range next {
		id := next[i].ID
		if id == "" {
			continue
		}
		if seenIncoming[id] {
			return nil, fmt.Errorf("%w: server %q duplicates the ID of another entry in the payload", ErrMCPServerIDConflict, next[i].Name)
		}
		seenIncoming[id] = true
		j, ok := prevByID[id]
		if !ok {
			return nil, fmt.Errorf("%w: server %q carries ID %q which does not exist in the stored configuration", ErrMCPServerIDConflict, next[i].Name, id)
		}
		claimed[j] = true
	}

	// Phase 2: exact (Name, BaseURL) claims, resolved for the whole payload
	// before any weak claim so a weak match can never shadow an exact one.
	exactMatch := make([]int, len(next))
	for i := range next {
		exactMatch[i] = -1
		if next[i].ID != "" {
			continue
		}
		candidate := -1
		for j := range prev {
			if prev[j].ID == "" || prev[j].Name != next[i].Name || prev[j].BaseURL != next[i].BaseURL {
				continue
			}
			if candidate >= 0 {
				return nil, fmt.Errorf("%w: server %q matches multiple identical stored servers", ErrMCPServerIDConflict, next[i].Name)
			}
			candidate = j
		}
		if candidate < 0 {
			continue
		}
		if claimed[candidate] {
			return nil, fmt.Errorf("%w: server %q claims a stored server another entry in the payload already claims", ErrMCPServerIDConflict, next[i].Name)
		}
		claimed[candidate] = true
		exactMatch[i] = candidate
	}
	for i, j := range exactMatch {
		if j >= 0 {
			next[i].ID = prev[j].ID
		}
	}

	// Phase 3: weak claims against the unclaimed remainder; every entry sees
	// the same post-phase-2 snapshot, so this phase is order-independent too.
	weakClaimed := make(map[int]bool, len(prev))
	weakMatch := make([]int, len(next))
	for i := range next {
		weakMatch[i] = -1
		if next[i].ID != "" {
			continue
		}

		var nameMatches, urlMatches []int
		for j := range prev {
			if claimed[j] || prev[j].ID == "" {
				continue
			}
			if prev[j].Name == next[i].Name {
				nameMatches = append(nameMatches, j)
			}
			if prev[j].BaseURL == next[i].BaseURL {
				urlMatches = append(urlMatches, j)
			}
		}

		var match int
		switch {
		case len(nameMatches) == 0 && len(urlMatches) == 0:
			// Genuinely new: no identity claim to resolve.
			continue
		case len(nameMatches) == 1 && len(urlMatches) == 0:
			match = nameMatches[0]
		case len(nameMatches) == 0 && len(urlMatches) == 1:
			match = urlMatches[0]
		default:
			return nil, fmt.Errorf("%w: server %q ambiguously matches more than one stored server", ErrMCPServerIDConflict, next[i].Name)
		}
		if weakClaimed[match] {
			return nil, fmt.Errorf("%w: server %q claims a stored server another entry in the payload already claims", ErrMCPServerIDConflict, next[i].Name)
		}
		weakClaimed[match] = true
		weakMatch[i] = match
	}
	for i, j := range weakMatch {
		if j >= 0 {
			next[i].ID = prev[j].ID
		}
	}

	return next, nil
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
