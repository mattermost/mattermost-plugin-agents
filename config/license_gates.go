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
// controls while prev (nil = create / empty defaults) carried none. Editing
// the lists of an agent that already has access controls, and opening them
// back up, are not new restrictions.
func AccessControlsNewlyRestricted(prev *llm.BotConfig, next llm.BotConfig) bool {
	if !BotHasAccessControls(next) {
		return false
	}
	return prev == nil || !BotHasAccessControls(*prev)
}

func check(level enterprise.Level, capability enterprise.Capability) error {
	if level >= enterprise.RequiredLevel(capability) {
		return nil
	}
	return enterprise.NewLicenseError(capability, level)
}

func emptyIfNil(prev *Config) Config {
	if prev == nil {
		return Config{}
	}
	return *prev
}

func botByID(bots []llm.BotConfig) map[string]llm.BotConfig {
	out := make(map[string]llm.BotConfig, len(bots))
	for _, bot := range bots {
		if bot.ID != "" {
			out[bot.ID] = bot
		}
	}
	return out
}

func serviceByID(services []llm.ServiceConfig) map[string]llm.ServiceConfig {
	out := make(map[string]llm.ServiceConfig, len(services))
	for _, svc := range services {
		if svc.ID != "" {
			out[svc.ID] = svc
		}
	}
	return out
}

func headersNonEmpty(h map[string]string) bool {
	for k, v := range h {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

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

// DefaultToolPolicyLookup returns the product-default policy for a tool on a
// server (empty when the product has no default beyond "ask"). It lets the
// gate treat the vetted defaults the System Console seeds into the saved
// configuration as baseline rather than as an admin-authored policy.
type DefaultToolPolicyLookup func(serverBaseURL, toolName string) string

// toolPoliciesNewlyAuto reports whether any tool in next auto-runs more
// widely than it did in prev (or than its seeded default, for a tool prev
// does not store). Narrowing or removing a policy is never reported.
func toolPoliciesNewlyAuto(prev, next []MCPToolConfig, defaultFor func(toolName string) string) bool {
	prevReach := make(map[string]int, len(prev))
	for _, tc := range prev {
		if tc.Name != "" {
			prevReach[tc.Name] = toolPolicyReach(tc.Policy)
		}
	}
	for _, tc := range next {
		if tc.Name == "" {
			continue
		}
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

func indexMCPServers(servers []MCPServerConfig) (byID, byName map[string]MCPServerConfig) {
	byID = make(map[string]MCPServerConfig, len(servers))
	byName = make(map[string]MCPServerConfig, len(servers))
	for _, s := range servers {
		if s.ID != "" {
			byID[s.ID] = s
		}
		if s.Name != "" {
			byName[s.Name] = s
		}
	}
	return byID, byName
}

func prevMCPServer(s MCPServerConfig, byID, byName map[string]MCPServerConfig) (MCPServerConfig, bool) {
	if s.ID != "" {
		if prev, ok := byID[s.ID]; ok {
			return prev, true
		}
	}
	if s.Name != "" {
		if prev, ok := byName[s.Name]; ok {
			return prev, true
		}
	}
	return MCPServerConfig{}, false
}

// ValidateLicenseTransition returns a *enterprise.LicenseError (or agent-limit
// error) for the first gated item that next newly enables or increases relative
// to prev. prev may be nil, which is treated as empty. Transitions to off /
// fewer / cleared are never denied.
func ValidateLicenseTransition(prev *Config, next Config, checker levelProvider, defaultPolicy DefaultToolPolicyLookup) error {
	level := enterprise.LevelUnlicensed
	if checker != nil {
		level = checker.Level()
	}
	from := emptyIfNil(prev)

	if err := validateServiceTransition(from, next, level); err != nil {
		return err
	}
	if err := validateBotCountTransition(from, next, level); err != nil {
		return err
	}
	if err := validateConfigBotFields(from, next, level); err != nil {
		return err
	}
	if next.EnableTokenUsageLogging && !from.EnableTokenUsageLogging {
		if err := check(level, enterprise.CapTokenAccounting); err != nil {
			return err
		}
	}
	if next.AllowNativeWebSearchInChannels && !from.AllowNativeWebSearchInChannels {
		if err := check(level, enterprise.CapProviderWebSearch); err != nil {
			return err
		}
	}
	if next.WebSearch.Enabled && !from.WebSearch.Enabled {
		if err := check(level, enterprise.CapSovereignWebSearch); err != nil {
			return err
		}
	}
	if next.EmbeddingSearchConfig.Type != "" && from.EmbeddingSearchConfig.Type == "" {
		if err := check(level, enterprise.CapSemanticSearch); err != nil {
			return err
		}
	}
	if (next.EnableCallSummary && !from.EnableCallSummary) ||
		(next.TranscriptGenerator != "" && from.TranscriptGenerator == "") {
		if err := check(level, enterprise.CapMeetings); err != nil {
			return err
		}
	}
	if err := validateMCPTransition(from, next, level, defaultPolicy); err != nil {
		return err
	}
	return nil
}

func validateServiceTransition(from, next Config, level enterprise.Level) error {
	if limit, ok := enterprise.ServiceLimitFor(level); ok {
		if len(next.Services) > limit && len(next.Services) > len(from.Services) {
			return check(level, enterprise.CapMultipleLLMServices)
		}
	}

	prevSvcs := serviceByID(from.Services)
	for _, svc := range next.Services {
		if svc.FallbackServiceID == "" {
			continue
		}
		prev, found := prevSvcs[svc.ID]
		if found && prev.FallbackServiceID == svc.FallbackServiceID {
			continue
		}
		if err := check(level, enterprise.CapModelFallback); err != nil {
			return err
		}
	}
	return nil
}

func validateBotCountTransition(from, next Config, level enterprise.Level) error {
	limit, ok := enterprise.AgentLimitFor(level)
	if !ok {
		return nil
	}
	if len(next.Bots) > limit && len(next.Bots) > len(from.Bots) {
		return enterprise.AgentLimitError(level)
	}
	return nil
}

func validateConfigBotFields(from, next Config, level enterprise.Level) error {
	prevBots := botByID(from.Bots)
	for _, bot := range next.Bots {
		var prev *llm.BotConfig
		if p, ok := prevBots[bot.ID]; ok {
			copied := p
			prev = &copied
		}

		if bot.UserAccessLevel == llm.UserAccessLevelAttributeBased &&
			(prev == nil || prev.UserAccessLevel != llm.UserAccessLevelAttributeBased) {
			if err := check(level, enterprise.CapAttributeBasedAccess); err != nil {
				return err
			}
		}
		if AccessControlsNewlyRestricted(prev, bot) {
			if err := check(level, enterprise.CapAgentAccessControls); err != nil {
				return err
			}
		}
		if bot.UseServiceAccountAuth && (prev == nil || !prev.UseServiceAccountAuth) {
			if err := check(level, enterprise.CapMCPServiceAccount); err != nil {
				return err
			}
		}
		if BotHasProviderWebSearch(bot) && (prev == nil || !BotHasProviderWebSearch(*prev)) {
			if err := check(level, enterprise.CapProviderWebSearch); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMCPTransition(from, next Config, level enterprise.Level, defaultPolicy DefaultToolPolicyLookup) error {
	defaultFor := func(serverBaseURL string) func(string) string {
		if defaultPolicy == nil {
			return nil
		}
		return func(toolName string) string { return defaultPolicy(serverBaseURL, toolName) }
	}
	if next.MCP.EnablePluginServer && !from.MCP.EnablePluginServer {
		if err := check(level, enterprise.CapRemoteMCP); err != nil {
			return err
		}
	}

	prevByID, prevByName := indexMCPServers(from.MCP.Servers)
	for _, srv := range next.MCP.Servers {
		prev, found := prevMCPServer(srv, prevByID, prevByName)
		if !found || (srv.Enabled && !prev.Enabled) {
			if err := check(level, enterprise.CapRemoteMCP); err != nil {
				return err
			}
		}
		if headersNonEmpty(srv.ServiceAccountHeaders) && (!found || !headersNonEmpty(prev.ServiceAccountHeaders)) {
			if err := check(level, enterprise.CapMCPServiceAccount); err != nil {
				return err
			}
		}
		var prevTools []MCPToolConfig
		if found {
			prevTools = prev.ToolConfigs
		}
		if toolPoliciesNewlyAuto(prevTools, srv.ToolConfigs, defaultFor(srv.BaseURL)) {
			if err := check(level, enterprise.CapToolApprovalPolicies); err != nil {
				return err
			}
		}
	}

	if toolPoliciesNewlyAuto(from.MCP.EmbeddedServer.ToolConfigs, next.MCP.EmbeddedServer.ToolConfigs, defaultFor(MCPEmbeddedServerOrigin)) {
		if err := check(level, enterprise.CapToolApprovalPolicies); err != nil {
			return err
		}
	}

	prevPluginByID := make(map[string]PluginServerConfig, len(from.MCP.PluginServers))
	for _, ps := range from.MCP.PluginServers {
		if ps.ID != "" {
			prevPluginByID[ps.ID] = ps
		}
	}
	for _, ps := range next.MCP.PluginServers {
		var prevTools []MCPToolConfig
		if prev, ok := prevPluginByID[ps.ID]; ok {
			prevTools = prev.ToolConfigs
		}
		if toolPoliciesNewlyAuto(prevTools, ps.ToolConfigs, defaultFor(PluginServerOrigin(ps.PluginID))) {
			if err := check(level, enterprise.CapToolApprovalPolicies); err != nil {
				return err
			}
		}
	}
	return nil
}
