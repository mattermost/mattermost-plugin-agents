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
// prefix is NOT duplicated. Tool-name validation (length <=128, allowed
// characters) happens inside the wrapped go-sdk AddTool and logs via the
// server's logger on failure; the sanitizer below ensures the prefix portion
// is always valid, so validation failures are caused only by the caller's
// tool-name suffix.
//
// AddTool is a free function (not a method on *Server) because Go does not
// allow methods to declare type parameters that don't appear on the receiver.
// The go-sdk's own mcp.AddTool has the same signature shape for the same
// reason.
//
// This function delegates the actual schema generation to go-sdk's generic
// mcp.AddTool, which internally calls jsonschema.For[In](nil) — the same call
// the Agents plugin uses internally at mcpserver/tools/provider.go:233. We do
// not pre-compute the schema in mcphelper because it would be overwritten.
func AddTool[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	prefix := sanitizeForToolName(s.config.PluginID) + "__"
	if !strings.HasPrefix(tool.Name, prefix) {
		tool.Name = prefix + tool.Name
	}
	mcp.AddTool[In, Out](s.server, tool, handler)
}

// sanitizeForToolName returns pluginID with any rune outside the MCP
// tool-name allowed charset [A-Za-z0-9_\-.] replaced with '_'. This is used
// ONLY for the tool-name prefix emitted to go-sdk's validator; every other
// use of PluginID (PluginHTTP routing path, Mattermost-Plugin-ID header,
// registry keys, filterToolsByConfig origin keys, wire-serialized JSON)
// keeps the RAW pluginID.
//
// The charset matches validToolNameRune in
// github.com/modelcontextprotocol/go-sdk@v1.4.1/mcp/tool.go:134-140. '.' IS
// allowed, so real Mattermost plugin IDs like "com.mattermost.plugin-foo"
// pass through unchanged. This function is defense-in-depth for future
// plugin IDs that might include spaces, '@', ':', '/', or non-ASCII runes.
//
// The function is idempotent: sanitizeForToolName(sanitizeForToolName(x)) ==
// sanitizeForToolName(x) for all x.
func sanitizeForToolName(pluginID string) string {
	var b strings.Builder
	b.Grow(len(pluginID))
	for _, r := range pluginID {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
