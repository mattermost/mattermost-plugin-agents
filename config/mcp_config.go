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

// ErrMCPServerIDConflict is the base error for every MCP server identity
// problem ReconcileMCPServerIDs can detect. The wrapped message describes the
// specific conflict; callers map it to a client error (the admin console
// payload is stale or corrupt and the admin should reload).
var ErrMCPServerIDConflict = errors.New("MCP server identity conflict")

// ReconcileMCPServerIDs carries stable IDs forward from prev onto entries in
// next that arrived without one (e.g. from webapp bundles predating the ID
// field). Server IDs are ABAC policy identities, so identity mistakes are
// never papered over: silently minting a fresh ID for an existing server
// detaches its policy (lookup becomes no-policy, which fails open), and
// transferring an ID guards the wrong server. Matching runs in global phases
// over the whole payload rather than entry-by-entry, so the outcome cannot
// depend on payload order, and anything suspicious is an
// ErrMCPServerIDConflict error:
//
//  1. Explicit-ID claims: incoming entries that already carry an ID must each
//     reference a distinct existing prev entry. Duplicate incoming IDs and
//     IDs absent from prev (fabricated/foreign — an admin bundle never
//     invents IDs) are errors.
//  2. Exact claims: each remaining ID-less entry with an exact
//     (Name, BaseURL) match takes that prev entry's ID. Matching is against
//     the full stored list, so a prev entry already claimed — in phase 1 or
//     by another exact claim — is an error, never a silent fallback.
//  3. Weak claims: each still-unmatched entry matches the unclaimed
//     remainder by Name with no BaseURL match elsewhere, else by BaseURL
//     with no Name match elsewhere. Ambiguity (multiple candidates on an
//     axis, or Name and BaseURL pointing at different prev entries) and two
//     entries resolving to the same prev entry are errors.
//  4. Entries matching nothing at all are genuinely new and stay ID-less;
//     the caller mints a fresh ID (the legitimate add-server path).
//
// Each prev entry is claimed at most once across all phases.
func ReconcileMCPServerIDs(next []MCPServerConfig, prev []MCPServerConfig) ([]MCPServerConfig, error) {
	prevByID := make(map[string]int, len(prev))
	for j := range prev {
		if prev[j].ID != "" {
			prevByID[prev[j].ID] = j
		}
	}

	claimed := make([]bool, len(prev))

	// Phase 1: entries arriving with an ID keep it and claim the prev entry
	// holding that ID.
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

	// Phase 2: exact (Name, BaseURL) claims, resolved against the full stored
	// list. Resolving exact claims for the whole payload before any weak
	// claim means an earlier weak match can never shadow a later exact one.
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

	// Phase 3: weak claims against the unclaimed remainder. Every entry sees
	// the same post-phase-2 snapshot, and two weak claims resolving to the
	// same prev entry are rejected, so this phase is order-independent too.
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
			// Multiple candidates on an axis, or Name and BaseURL pointing at
			// different stored servers.
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
