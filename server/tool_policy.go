// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
)

// pluginServerOriginPrefix mirrors the wire format produced by
// mcp.pluginServerOriginKey (mcp/user_clients.go:227) and the synthetic
// origin keys assembled by filterToolsByConfig (mcp/client_manager.go:493)
// and the Phase 3 external-endpoint synthetic config
// (mcpserver/plugin_handlers.go:215). Kept as a package-private constant in
// the server package because the mcp helper that constructs it is unexported.
const pluginServerOriginPrefix = "plugin://"

// lookupToolPolicy is the pure decision function backing the
// mcp.ToolPolicyChecker installed in OnActivate. It is extracted from the
// inline closure at server/main.go so it can be unit-tested without
// standing up a Plugin instance.
//
// Resolution order — first matching branch wins:
//
//  1. mcp.EmbeddedClientKey               → embedded server policy (with
//                                            vetted-seed fallback when no
//                                            persisted ToolConfigs).
//  2. exact-match on cfg.Servers[i].BaseURL → remote MCP server policy.
//  3. plugin://<pluginID> prefix          → cfg.PluginServers[i].PluginID
//                                            lookup (M2 Phase 5 remediation;
//                                            Phase 1 added PluginServers but
//                                            did not wire the policy checker).
//  4. fallthrough                         → ("ask", false). Means "do not
//                                            auto-execute"; tool either was
//                                            never surfaced (filter dropped
//                                            it) or the origin is unknown.
//
// Synthetic *mcp.ServerConfig for plugin entries hardcodes Enabled=true and
// relies on the upstream `if !ps.Enabled { continue }` filter — mirrors
// Phase 3's external-endpoint pattern at mcpserver/plugin_handlers.go:212-217
// and filterToolsByConfig at mcp/client_manager.go:489-501. Propagating
// ps.Enabled here would cause MCPServerConfig.GetToolPolicy to short-circuit
// to ("ask", false) before consulting ToolConfigs, which would silently hide
// every tool of a re-enabled plugin during a stale-cache window.
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
