// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddTool registers a typed tool, prepending the sanitized plugin-ID namespace
// to tool.Name when not already present.
func AddTool[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	prefix := sanitizeForToolName(s.config.PluginID) + "__"
	if !strings.HasPrefix(tool.Name, prefix) {
		tool.Name = prefix + tool.Name
	}
	mcp.AddTool[In, Out](s.server, tool, handler)
}

// sanitizeForToolName replaces characters outside [A-Za-z0-9_-] with '_'.
// Applies only to the LLM-facing tool-name prefix; routing and registry keys
// keep the raw plugin ID. Stricter than go-sdk because Bifrost / Anthropic
// enforce ^[a-zA-Z0-9_-]{1,128}$ on tool names.
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
