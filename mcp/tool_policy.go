// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// ToolPolicyChecker looks up the per-tool policy for a given MCP server/tool.
type ToolPolicyChecker interface {
	GetToolPolicy(serverBaseURL string, toolName string) (policy string, enabled bool)
}

// ToolPolicyFunc is a function adapter that implements ToolPolicyChecker.
type ToolPolicyFunc func(serverBaseURL string, toolName string) (string, bool)

// GetToolPolicy implements ToolPolicyChecker.
func (f ToolPolicyFunc) GetToolPolicy(serverBaseURL string, toolName string) (string, bool) {
	return f(serverBaseURL, toolName)
}

// ToolPolicyLookupName returns the configured name to use for a runtime tool name.
// Runtime MCP tools may be namespaced while persisted policy config is usually
// stored by the server's bare tool name. An exact configured name still wins.
func ToolPolicyLookupName(sc *ServerConfig, toolName string) string {
	if sc == nil || toolName == "" || llm.IsBareMCPToolName(toolName) {
		return toolName
	}
	for _, toolConfig := range sc.ToolConfigs {
		if toolConfig.Name == toolName {
			return toolName
		}
	}
	return llm.BareMCPToolName(toolName)
}

// LookupToolPolicy resolves a tool's policy for embedded, remote, and plugin
// origins. Unknown or disabled origins never auto-execute.
func LookupToolPolicy(cfg Config, serverBaseURL, toolName string) (string, bool) {
	if serverBaseURL == EmbeddedClientKey {
		// Backfill the vetted seed for embedded tools the admin hasn't stored a
		// config for, so tools added after an install first saved its configs
		// still get their default policy. Stored entries win.
		toolConfigs := mergeSeedConfigs(cfg.EmbeddedServer.ToolConfigs, SeedVettedToolConfigs(EmbeddedClientKey))
		embeddedCfg := &ServerConfig{
			Name:        EmbeddedServerName,
			Enabled:     true,
			BaseURL:     EmbeddedClientKey,
			ToolConfigs: toolConfigs,
		}
		return embeddedCfg.GetToolPolicy(ToolPolicyLookupName(embeddedCfg, toolName))
	}

	for i := range cfg.Servers {
		if cfg.Servers[i].BaseURL == serverBaseURL {
			return cfg.Servers[i].GetToolPolicy(ToolPolicyLookupName(&cfg.Servers[i], toolName))
		}
	}

	pluginID, isPluginOrigin := strings.CutPrefix(serverBaseURL, pluginServerOriginKey(""))
	if !isPluginOrigin {
		return ToolPolicyAsk, false
	}

	for i := range cfg.PluginServers {
		ps := &cfg.PluginServers[i]
		if ps.PluginID != pluginID {
			continue
		}
		if !ps.Enabled {
			return ToolPolicyAsk, false
		}
		synthetic := &ServerConfig{
			Name:        ps.Name,
			Enabled:     true,
			BaseURL:     serverBaseURL,
			ToolConfigs: ps.ToolConfigs,
		}
		return synthetic.GetToolPolicy(ToolPolicyLookupName(synthetic, toolName))
	}

	return ToolPolicyAsk, false
}
