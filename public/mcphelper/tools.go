// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddTool registers a typed tool on the server. The In and Out type parameters
// drive JSON-schema generation via github.com/google/jsonschema-go: struct
// fields are introspected and `jsonschema:"..."` tags are honored as property
// descriptions. The caller populates tool.Name, tool.Description, and any
// other *mcp.Tool metadata; mcphelper rewrites tool.Name in place to prepend
// the sanitized plugin-ID namespace.
//
// If tool.Name already starts with the sanitized "<PluginID>__" prefix, the
// prefix is not duplicated. AddTool is a free function because Go does not
// allow generic methods on *Server.
func AddTool[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	prefix := sanitizeForToolName(s.config.PluginID) + "__"
	if !strings.HasPrefix(tool.Name, prefix) {
		tool.Name = prefix + tool.Name
	}
	mcp.AddTool[In, Out](s.server, tool, handler)
}

// sanitizeForToolName returns pluginID with any rune outside [A-Za-z0-9_-]
// replaced with '_', matching LLM-provider tool-name constraints.
func sanitizeForToolName(pluginID string) string {
	var b strings.Builder
	b.Grow(len(pluginID))
	for _, r := range pluginID {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
