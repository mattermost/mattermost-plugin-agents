// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
)

// pluginServerOriginPrefix mirrors the wire format produced by
// mcp.pluginServerOriginKey and the synthetic origin keys assembled by
// filterToolsByConfig and mcpserver plugin handlers. Kept as a package-private
// constant in the server package because the mcp helper that constructs it is
// unexported.
const pluginServerOriginPrefix = "plugin://"

// lookupToolPolicy is the pure decision function backing the
// mcp.ToolPolicyChecker installed in OnActivate. It is extracted from the
// inline closure at server/main.go so it can be unit-tested without
// standing up a Plugin instance.
//
// Resolution order — first matching branch wins:
//
//  1. mcp.EmbeddedClientKey               → embedded server policy (with
//     vetted-seed fallback when no
//     persisted ToolConfigs).
//  2. exact-match on cfg.Servers[i].BaseURL → remote MCP server policy.
//  3. plugin://<pluginID> prefix          → cfg.PluginServers[i].PluginID
//     lookup.
//  4. fallthrough                         → ("ask", false). Means "do not
//     auto-execute"; tool either was
//     never surfaced (filter dropped
//     it) or the origin is unknown.
//
// Synthetic *mcp.ServerConfig for plugin entries hardcodes Enabled=true and
// relies on the upstream `if !ps.Enabled { continue }` filter. Propagating
// ps.Enabled here would cause MCPServerConfig.GetToolPolicy to short-circuit to
// ("ask", false) before consulting ToolConfigs.
func lookupToolPolicy(mcpCfg config.MCPConfig, serverBaseURL, toolName string) (string, bool) {
	if serverBaseURL == mcp.EmbeddedClientKey {
		toolConfigs := mcpCfg.EmbeddedServer.ToolConfigs
		if len(toolConfigs) == 0 {
			toolConfigs = mcp.SeedVettedToolConfigs(mcp.EmbeddedClientKey)
		}
		embeddedCfg := &mcp.ServerConfig{Enabled: true, ToolConfigs: toolConfigs}
		return embeddedCfg.GetToolPolicy(toolName)
	}
	for i := range mcpCfg.Servers {
		if mcpCfg.Servers[i].BaseURL == serverBaseURL {
			return mcpCfg.Servers[i].GetToolPolicy(toolName)
		}
	}
	if strings.HasPrefix(serverBaseURL, pluginServerOriginPrefix) {
		pluginID := strings.TrimPrefix(serverBaseURL, pluginServerOriginPrefix)
		for i := range mcpCfg.PluginServers {
			ps := &mcpCfg.PluginServers[i]
			if ps.PluginID != pluginID {
				continue
			}
			if !ps.Enabled {
				// Disabled plugin: tool should already have been dropped by
				// filterToolsByConfig. Defensive fallthrough — never
				// auto-execute on behalf of a disabled plugin.
				break
			}
			synthetic := &mcp.ServerConfig{
				Enabled:     true,
				ToolConfigs: ps.ToolConfigs,
			}
			return synthetic.GetToolPolicy(toolName)
		}
	}
	return mcp.ToolPolicyAsk, false
}
