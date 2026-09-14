// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ToolSelection narrows which MCP servers an operation is allowed to contact.
// It is always intersected with the admin-enabled servers, so the zero value
// means "every admin-enabled server": what management and catalog surfaces
// need, since they must show and configure servers the requesting agent has
// not allowlisted.
//
// Runtime tool construction passes a narrowed selection instead, because an
// ineligible server must never be contacted, not merely have its tools
// filtered out afterwards.
type ToolSelection struct {
	// AllowedOrigins, when non-nil, restricts the selection to these server
	// origins. A non-nil empty slice selects nothing.
	AllowedOrigins []string

	// DeniedOrigins removes server origins from the selection, including ones
	// AllowedOrigins lists.
	DeniedOrigins []string

	// ExcludeRemoteServers drops everything but the embedded Mattermost
	// server. Remote and plugin MCP servers are the licensed "MCP Support"
	// feature, so without a license they are never contacted.
	ExcludeRemoteServers bool
}

// Allows reports whether origin is part of the selection.
func (s ToolSelection) Allows(origin string) bool {
	normalized := llm.NormalizeMCPServerOrigin(origin)
	if normalized == "" {
		return false
	}
	if s.ExcludeRemoteServers && IsRemoteServerOrigin(normalized) {
		return false
	}
	if slices.Contains(llm.NormalizeMCPServerOrigins(s.DeniedOrigins), normalized) {
		return false
	}
	if s.AllowedOrigins == nil {
		return true
	}
	return slices.Contains(llm.NormalizeMCPServerOrigins(s.AllowedOrigins), normalized)
}
