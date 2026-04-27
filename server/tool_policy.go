// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
)

// pluginServerOriginPrefix mirrors the unexported mcp plugin-server origin key.
const pluginServerOriginPrefix = "plugin://"

// lookupToolPolicy resolves a tool's policy for embedded, remote, and plugin
// server origins. Unknown or disabled origins never auto-execute.
//
// Plugin entries use Enabled=true in the synthetic ServerConfig after checking
// ps.Enabled, so GetToolPolicy can still read disabled per-tool entries.
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
