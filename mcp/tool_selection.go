// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ToolSelection narrows which MCP servers an operation is allowed to contact.
// It is always intersected with the admin-enabled servers, so the zero value
// means "every admin-enabled server" — the intent management and catalog
// surfaces need, since they must show and configure servers the requesting
// agent has not allowlisted.
//
// Runtime tool construction passes a narrowed selection instead: an ineligible
// server must never be contacted, not merely have its tools filtered out
// afterwards.
type ToolSelection struct {
	// AllowedOrigins, when non-nil, restricts the selection to these server
	// origins. A non-nil empty slice selects nothing.
	AllowedOrigins []string

	// DeniedOrigins removes server origins from the selection. Applied after
	// AllowedOrigins.
	DeniedOrigins []string

	// ExcludeRemoteServers drops everything but the embedded Mattermost
	// server. Remote and plugin MCP servers are the licensed "MCP Support"
	// feature, so without a license they are never contacted.
	ExcludeRemoteServers bool
}

// Allows reports whether origin is part of the selection.
func (s ToolSelection) Allows(origin string) bool {
	return s.compile().allows(origin)
}

// originFilter is a ToolSelection compiled for repeated lookups.
type originFilter struct {
	// allowed is nil when the selection has no allowlist.
	allowed      map[string]bool
	denied       map[string]bool
	embeddedOnly bool
}

func (s ToolSelection) compile() originFilter {
	filter := originFilter{embeddedOnly: s.ExcludeRemoteServers}

	if s.AllowedOrigins != nil {
		filter.allowed = originSet(s.AllowedOrigins)
	}
	if len(s.DeniedOrigins) > 0 {
		filter.denied = originSet(s.DeniedOrigins)
	}

	return filter
}

func (f originFilter) allows(origin string) bool {
	normalized := llm.NormalizeMCPServerOrigin(origin)
	if normalized == "" {
		return false
	}
	if f.embeddedOnly && IsRemoteServerOrigin(normalized) {
		return false
	}
	if f.denied[normalized] {
		return false
	}
	if f.allowed == nil {
		return true
	}
	return f.allowed[normalized]
}

func originSet(origins []string) map[string]bool {
	set := make(map[string]bool, len(origins))
	for _, origin := range llm.NormalizeMCPServerOrigins(origins) {
		set[origin] = true
	}
	return set
}
