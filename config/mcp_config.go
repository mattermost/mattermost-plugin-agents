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

// ErrMCPServerIDConflict is the base error for every identity problem
// ReconcileMCPConfigIDs / kind-specific reconcilers can detect; callers map
// it to a client error (stale or corrupt admin console payload).
var ErrMCPServerIDConflict = errors.New("MCP server identity conflict")

// ReconcileMCPConfigIDs is the single entry point for MCP identity on config
// save. It:
//
//  1. Treats PluginServers as server-owned: prev is carried forward
//     unconditionally. Full config save never accepts client-provided plugin
//     rows (bridge register/unregister and PUT /admin/mcp/plugin-servers/:id
//     are the only writers).
//  2. Runs kind-specific ID carry-forward for remote and embedded servers.
//  3. Rejects any ID shared by two MCP resources of any kind.
func ReconcileMCPConfigIDs(next, prev MCPConfig) (MCPConfig, error) {
	next.PluginServers = append([]PluginServerConfig(nil), prev.PluginServers...)

	servers, err := ReconcileMCPServerIDs(next.Servers, prev.Servers)
	if err != nil {
		return MCPConfig{}, err
	}
	next.Servers = servers

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

// OccupiedMCPServerIDs returns every non-empty MCP resource ID across remote,
// embedded, and plugin entries. Used by mint paths to avoid cross-kind collisions.
func OccupiedMCPServerIDs(cfg MCPConfig) map[string]struct{} {
	out := make(map[string]struct{}, len(cfg.Servers)+len(cfg.PluginServers)+1)
	for i := range cfg.Servers {
		if id := cfg.Servers[i].ID; id != "" {
			out[id] = struct{}{}
		}
	}
	if id := cfg.EmbeddedServer.ID; id != "" {
		out[id] = struct{}{}
	}
	for i := range cfg.PluginServers {
		if id := cfg.PluginServers[i].ID; id != "" {
			out[id] = struct{}{}
		}
	}
	return out
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

// ReconcileMCPServerIDs carries stable IDs forward from prev onto ID-less
// entries in next (e.g. from webapp bundles predating the ID field). Server
// IDs are ABAC policy identities, so identity mistakes are never papered
// over: minting a fresh ID for an existing server detaches its policy (which
// fails open). Matching runs in global phases over the whole payload so the
// outcome cannot depend on payload order; anything suspicious errors:
//
//  1. Explicit-ID claims: an ID found in prev claims that entry. An ID
//     unknown to prev is accepted as a caller-chosen ID for a genuinely new
//     server (admin-API automation seeds its own IDs) — unless the entry's
//     Name or BaseURL matches a stored server, which means a stale or
//     corrupt client is trying to swap an existing server's policy identity.
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
		if j, ok := prevByID[id]; ok {
			claimed[j] = true
			continue
		}
		// Unknown ID: legitimate for a genuinely new server seeded by API
		// automation. A Name or BaseURL collision with a stored
		// (ID-carrying) server instead means a stale client is swapping
		// that server's policy identity, which would detach its policy.
		for k := range prev {
			if prev[k].ID == "" {
				continue
			}
			if prev[k].Name == next[i].Name || prev[k].BaseURL == next[i].BaseURL {
				return nil, fmt.Errorf("%w: server %q carries ID %q that the stored configuration never issued", ErrMCPServerIDConflict, next[i].Name, id)
			}
		}
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

// ReconcileEmbeddedMCPServerID carries the embedded server's stable ID forward
// from prev onto an ID-less next. A non-empty next ID that differs from a
// non-empty prev ID is a conflict (same ErrMCPServerIDConflict contract as
// remote servers). Caller-chosen IDs on a previously ID-less embedded server
// are kept; both empty leaves next ID-less for the caller to mint.
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
