// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// levelProvider is the subset of LicenseChecker used by config gates.
type levelProvider interface {
	Level() enterprise.Level
}

// LicensedServices returns the LLM services that are active at level, in
// configuration order. Below Enterprise that is the first service; at
// Enterprise and above every configured service is active.
func LicensedServices(cfg *Config, level enterprise.Level) []llm.ServiceConfig {
	if cfg == nil {
		return nil
	}
	limit, capped := enterprise.ServiceLimitFor(level)
	if !capped || len(cfg.Services) <= limit {
		return cfg.Services
	}
	return cfg.Services[:limit]
}

// ActiveServiceIDs returns the IDs of LicensedServices, skipping entries with
// an empty ID.
func ActiveServiceIDs(cfg *Config, level enterprise.Level) []string {
	svcs := LicensedServices(cfg, level)
	ids := make([]string, 0, len(svcs))
	for _, svc := range svcs {
		if svc.ID != "" {
			ids = append(ids, svc.ID)
		}
	}
	return ids
}

// BotHasProviderWebSearch reports whether cfg enables provider-native web search.
func BotHasProviderWebSearch(cfg llm.BotConfig) bool {
	return slices.Contains(cfg.EnabledNativeTools, llm.NativeToolWebSearch)
}

// BotHasAccessControls reports whether cfg restricts the agent to named
// users/teams/channels or a block list. Attribute-based access is a separate
// capability and is not included here.
func BotHasAccessControls(cfg llm.BotConfig) bool {
	if cfg.ChannelAccessLevel != llm.ChannelAccessLevelAll {
		return true
	}
	if len(cfg.ChannelIDs) > 0 || len(cfg.UserIDs) > 0 || len(cfg.TeamIDs) > 0 {
		return true
	}
	switch cfg.UserAccessLevel {
	case llm.UserAccessLevelAllow, llm.UserAccessLevelBlock, llm.UserAccessLevelNone:
		return true
	}
	return false
}

// AccessControlsNewlyRestricted reports whether next carries named access
// controls while prev (the zero value for a new agent) carried none. Editing
// the lists of an agent that already has access controls, and opening them
// back up, are not new restrictions.
func AccessControlsNewlyRestricted(prev, next llm.BotConfig) bool {
	return BotHasAccessControls(next) && !BotHasAccessControls(prev)
}

// DefaultToolPolicyLookup returns the product-default policy for a tool on a
// server (empty when the product has no default beyond "ask"). It lets the
// gate treat the vetted defaults the System Console seeds into the saved
// configuration as baseline rather than as an admin-authored policy.
type DefaultToolPolicyLookup func(serverBaseURL, toolName string) string

// toolPolicyReach orders policies by how much they auto-run: ask < auto-run
// in DMs < auto-run everywhere. Unknown values behave as ask.
func toolPolicyReach(policy string) int {
	switch policy {
	case MCPToolPolicyAutoRunEverywhere:
		return 2
	case MCPToolPolicyAutoRunInDM:
		return 1
	default:
		return 0
	}
}

// toolPoliciesNewlyAuto reports whether any tool in next auto-runs more
// widely than it did in prev (or than its seeded default, for a tool prev
// does not store). Narrowing or removing a policy is never reported.
func toolPoliciesNewlyAuto(prev, next []MCPToolConfig, defaultFor func(toolName string) string) bool {
	prevReach := make(map[string]int, len(prev))
	for _, tc := range prev {
		prevReach[tc.Name] = toolPolicyReach(tc.Policy)
	}
	for _, tc := range next {
		baseline, stored := prevReach[tc.Name]
		if !stored && defaultFor != nil {
			baseline = toolPolicyReach(defaultFor(tc.Name))
		}
		if toolPolicyReach(tc.Policy) > baseline {
			return true
		}
	}
	return false
}

func headersNonEmpty(h map[string]string) bool {
	for k, v := range h {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// byKey indexes items by key, skipping empty keys.
func byKey[T any](items []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(items))
	for _, item := range items {
		if k := key(item); k != "" {
			out[k] = item
		}
	}
	return out
}

// gate pairs a transition in the configuration with the capability it needs.
type gate struct {
	newlyOn    bool
	capability enterprise.Capability
}

// ValidateLicenseTransition returns a *enterprise.LicenseError (or agent-limit
// error) for a gated item that next newly enables or increases relative to
// prev. prev may be nil, which is treated as empty. Transitions to off /
// fewer / cleared are never denied.
func ValidateLicenseTransition(prev *Config, next Config, checker levelProvider, defaultPolicy DefaultToolPolicyLookup) error {
	level := enterprise.LevelUnlicensed
	if checker != nil {
		level = checker.Level()
	}
	var from Config
	if prev != nil {
		from = *prev
	}

	if limit, capped := enterprise.ServiceLimitFor(level); capped && len(next.Services) > limit && len(next.Services) > len(from.Services) {
		return enterprise.NewLicenseError(enterprise.CapMultipleLLMServices, level)
	}
	if limit, capped := enterprise.AgentLimitFor(level); capped && len(next.Bots) > limit && len(next.Bots) > len(from.Bots) {
		return enterprise.AgentLimitError(level)
	}

	defaultFor := func(serverBaseURL string) func(string) string {
		if defaultPolicy == nil {
			return nil
		}
		return func(toolName string) string { return defaultPolicy(serverBaseURL, toolName) }
	}

	gates := []gate{
		{next.EnableTokenUsageLogging && !from.EnableTokenUsageLogging, enterprise.CapTokenAccounting},
		{next.AllowNativeWebSearchInChannels && !from.AllowNativeWebSearchInChannels, enterprise.CapProviderWebSearch},
		{next.WebSearch.Enabled && !from.WebSearch.Enabled, enterprise.CapSovereignWebSearch},
		{next.EmbeddingSearchConfig.Type != "" && from.EmbeddingSearchConfig.Type == "", enterprise.CapSemanticSearch},
		{(next.EnableCallSummary && !from.EnableCallSummary) || (next.TranscriptGenerator != "" && from.TranscriptGenerator == ""), enterprise.CapMeetings},
		{next.MCP.EnablePluginServer && !from.MCP.EnablePluginServer, enterprise.CapRemoteMCP},
		{toolPoliciesNewlyAuto(from.MCP.EmbeddedServer.ToolConfigs, next.MCP.EmbeddedServer.ToolConfigs, defaultFor(MCPEmbeddedServerOrigin)), enterprise.CapToolApprovalPolicies},
	}

	prevServices := byKey(from.Services, func(s llm.ServiceConfig) string { return s.ID })
	for _, svc := range next.Services {
		prevFallback := prevServices[svc.ID].FallbackServiceID
		gates = append(gates, gate{svc.FallbackServiceID != "" && svc.FallbackServiceID != prevFallback, enterprise.CapModelFallback})
	}

	prevBots := byKey(from.Bots, func(b llm.BotConfig) string { return b.ID })
	for _, bot := range next.Bots {
		gates = append(gates, agentGates(prevBots[bot.ID], bot)...)
	}

	// New servers carry freshly minted IDs by the time this runs, so an ID
	// missing from prev is a newly added server.
	prevServers := byKey(from.MCP.Servers, func(s MCPServerConfig) string { return s.ID })
	for _, srv := range next.MCP.Servers {
		prevSrv, found := prevServers[srv.ID]
		gates = append(gates,
			gate{!found || (srv.Enabled && !prevSrv.Enabled), enterprise.CapRemoteMCP},
			gate{headersNonEmpty(srv.ServiceAccountHeaders) && !headersNonEmpty(prevSrv.ServiceAccountHeaders), enterprise.CapMCPServiceAccount},
			gate{toolPoliciesNewlyAuto(prevSrv.ToolConfigs, srv.ToolConfigs, defaultFor(srv.BaseURL)), enterprise.CapToolApprovalPolicies},
		)
	}

	prevPlugins := byKey(from.MCP.PluginServers, func(p PluginServerConfig) string { return p.PluginID })
	for _, ps := range next.MCP.PluginServers {
		prevPS, found := prevPlugins[ps.PluginID]
		gates = append(gates,
			gate{found && ps.Enabled && !prevPS.Enabled, enterprise.CapRemoteMCP},
			gate{toolPoliciesNewlyAuto(prevPS.ToolConfigs, ps.ToolConfigs, defaultFor(PluginServerOrigin(ps.PluginID))), enterprise.CapToolApprovalPolicies},
		)
	}

	return firstDenied(gates, level)
}

// ValidateAgentTransition returns a *enterprise.LicenseError when next newly
// enables an agent setting unavailable at level. prev is the stored agent, or
// the zero value for a new one.
func ValidateAgentTransition(prev, next llm.BotConfig, level enterprise.Level) error {
	return firstDenied(agentGates(prev, next), level)
}

func agentGates(prev, next llm.BotConfig) []gate {
	return []gate{
		{next.UserAccessLevel == llm.UserAccessLevelAttributeBased && prev.UserAccessLevel != llm.UserAccessLevelAttributeBased, enterprise.CapAttributeBasedAccess},
		{AccessControlsNewlyRestricted(prev, next), enterprise.CapAgentAccessControls},
		{next.UseServiceAccountAuth && !prev.UseServiceAccountAuth, enterprise.CapMCPServiceAccount},
		{BotHasProviderWebSearch(next) && !BotHasProviderWebSearch(prev), enterprise.CapProviderWebSearch},
	}
}

func firstDenied(gates []gate, level enterprise.Level) error {
	for _, g := range gates {
		if g.newlyOn && level < enterprise.RequiredLevel(g.capability) {
			return enterprise.NewLicenseError(g.capability, level)
		}
	}
	return nil
}
