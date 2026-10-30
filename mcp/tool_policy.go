// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ToolPolicyChecker looks up the per-tool policy for a given MCP server/tool.
// readOnlyHint is the tool's MCP readOnlyHint annotation as its server reported
// it to the requesting user; it only changes the default for unconfigured tools
// (see DefaultToolPolicy).
type ToolPolicyChecker interface {
	GetToolPolicy(serverBaseURL string, toolName string, readOnlyHint bool) (policy string, enabled bool)
}

// ToolPolicyFunc is a function adapter that implements ToolPolicyChecker.
type ToolPolicyFunc func(serverBaseURL string, toolName string, readOnlyHint bool) (string, bool)

// GetToolPolicy implements ToolPolicyChecker.
func (f ToolPolicyFunc) GetToolPolicy(serverBaseURL string, toolName string, readOnlyHint bool) (string, bool) {
	return f(serverBaseURL, toolName, readOnlyHint)
}

// NewConfigToolPolicyChecker resolves effective policies against the current
// config and license on every call. Callers pass the bare tool name (runtime
// server namespace removed); it must not be stripped again, because MCP tool
// names may themselves contain the separator (pluginmcp prefixes every plugin
// tool with "{pluginID}__").
func NewConfigToolPolicyChecker(getConfig func() Config, policiesLicensed func() bool) ToolPolicyChecker {
	return ToolPolicyFunc(func(serverBaseURL string, toolName string, readOnlyHint bool) (string, bool) {
		return LookupEffectiveToolPolicy(getConfig(), serverBaseURL, toolName, readOnlyHint, policiesLicensed())
	})
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

// DefaultToolPolicy returns the policy for a tool that has no stored config:
// the server's vetted seed policy when it has one, otherwise auto_run_in_dm
// for a tool its server annotates as read-only, otherwise ask. The embedded
// server ignores the hint: its vetted list is curated and deliberately leaves
// some read-only tools (get_file_link, webhook listings) at ask.
//
// readOnlyHint is reported by the server, so a server that mislabels a tool
// can get it auto-run; this is bounded to DMs, where only the requesting user
// sees the result, and an admin's stored config always wins.
func DefaultToolPolicy(serverBaseURL, toolName string, readOnlyHint bool) string {
	for _, seed := range SeedVettedToolConfigs(serverBaseURL) {
		if seed.Name == toolName {
			return seed.Policy
		}
	}
	if readOnlyHint && serverBaseURL != EmbeddedClientKey {
		return ToolPolicyAutoRunInDM
	}
	return ToolPolicyAsk
}

// EffectiveDefaultToolPolicy is the default LookupEffectiveToolPolicy applies
// to a tool with no stored config: DefaultToolPolicy when tool-approval
// policies are licensed, otherwise the product default (see
// LookupEffectiveToolPolicy).
func EffectiveDefaultToolPolicy(serverBaseURL, toolName string, readOnlyHint, policiesLicensed bool) string {
	if policiesLicensed {
		return DefaultToolPolicy(serverBaseURL, toolName, readOnlyHint)
	}
	return unlicensedDefaultToolPolicy(serverBaseURL, toolName)
}

// LookupToolPolicy resolves a tool's policy for embedded, remote, and plugin
// origins. Unknown or disabled origins never auto-execute. Tools without a
// stored config get DefaultToolPolicy.
func LookupToolPolicy(cfg Config, serverBaseURL, toolName string, readOnlyHint bool) (string, bool) {
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
		lookupName := ToolPolicyLookupName(embeddedCfg, toolName)
		return embeddedCfg.GetToolPolicyOrDefault(lookupName, DefaultToolPolicy(EmbeddedClientKey, lookupName, false))
	}

	for i := range cfg.Servers {
		if cfg.Servers[i].BaseURL == serverBaseURL {
			lookupName := ToolPolicyLookupName(&cfg.Servers[i], toolName)
			return cfg.Servers[i].GetToolPolicyOrDefault(lookupName, DefaultToolPolicy(serverBaseURL, lookupName, readOnlyHint))
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
		lookupName := ToolPolicyLookupName(synthetic, toolName)
		return synthetic.GetToolPolicyOrDefault(lookupName, DefaultToolPolicy(serverBaseURL, lookupName, readOnlyHint))
	}

	return ToolPolicyAsk, false
}

// LookupEffectiveToolPolicy is LookupToolPolicy limited to the product-default
// policy when tool-approval policies are not available: the stored policy
// applies only where it auto-runs no more widely than the default, so an
// admin's narrower choice (such as ask) always holds. Enabled still comes from
// the stored config, so a disabled tool stays disabled at every level. The
// default is the vetted seed policy for EmbeddedClientKey tools (ask if the
// tool is not in the seed) and ask for every other origin, so a server's
// readOnlyHint does not auto-run its tools either.
func LookupEffectiveToolPolicy(cfg Config, serverBaseURL, toolName string, readOnlyHint, policiesLicensed bool) (string, bool) {
	policy, enabled := LookupToolPolicy(cfg, serverBaseURL, toolName, readOnlyHint)
	if policiesLicensed {
		return policy, enabled
	}

	defaultPolicy := unlicensedDefaultToolPolicy(serverBaseURL, toolName)
	if config.ToolPolicyReach(policy) < config.ToolPolicyReach(defaultPolicy) {
		return policy, enabled
	}
	return defaultPolicy, enabled
}

// unlicensedDefaultToolPolicy is the product-default policy that applies when
// tool-approval policies are not licensed.
func unlicensedDefaultToolPolicy(serverBaseURL, toolName string) string {
	defaultPolicy := ToolPolicyAsk
	if serverBaseURL == EmbeddedClientKey {
		lookupName := ToolPolicyLookupName(&ServerConfig{
			Name:        EmbeddedServerName,
			Enabled:     true,
			BaseURL:     EmbeddedClientKey,
			ToolConfigs: SeedVettedToolConfigs(EmbeddedClientKey),
		}, toolName)
		for _, seed := range SeedVettedToolConfigs(EmbeddedClientKey) {
			if seed.Name == lookupName || seed.Name == toolName {
				defaultPolicy = seed.Policy
				break
			}
		}
	}
	return defaultPolicy
}
