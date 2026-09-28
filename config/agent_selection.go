// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// AgentInactiveReason explains why an agent is not running. The empty value
// means the agent is active.
type AgentInactiveReason string

const (
	AgentActive                     AgentInactiveReason = ""
	AgentInactiveInvalidConfig      AgentInactiveReason = "invalid_config"
	AgentInactiveServiceUnavailable AgentInactiveReason = "service_unavailable"
	AgentInactiveServiceNotLicensed AgentInactiveReason = "service_not_licensed"
	AgentInactiveAgentLimit         AgentInactiveReason = "agent_limit"
)

// AgentPool returns configuration-file bots followed by user-created agents
// ordered by creation time (then ID), which is the order agents take slots in
// the agent cap. dbAgents is not modified; nil entries are skipped.
func AgentPool(fileBots []llm.BotConfig, dbAgents []*llm.BotConfig) []llm.BotConfig {
	sorted := slices.Clone(dbAgents)
	sorted = slices.DeleteFunc(sorted, func(a *llm.BotConfig) bool { return a == nil })
	slices.SortStableFunc(sorted, func(a, b *llm.BotConfig) int {
		if a.CreateAt != b.CreateAt {
			if a.CreateAt < b.CreateAt {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})

	pool := make([]llm.BotConfig, 0, len(fileBots)+len(sorted))
	pool = append(pool, fileBots...)
	for _, agent := range sorted {
		pool = append(pool, *agent)
	}
	return pool
}

// AgentInactiveReasons classifies each agent in pool (ordered as by AgentPool)
// at level. Agents with a missing or incomplete LLM service, or one that is
// not active at level, never occupy a slot; the remaining agents take slots in
// pool order until the agent cap is reached.
func AgentInactiveReasons(services []llm.ServiceConfig, pool []llm.BotConfig, level enterprise.Level) []AgentInactiveReason {
	cfg := &Config{Services: services}
	byID := llm.ServiceLookup(services)
	activeServices := ActiveServiceIDs(cfg, level)
	limit, capped := enterprise.AgentLimitFor(level)

	reasons := make([]AgentInactiveReason, len(pool))
	active := 0
	for i, bot := range pool {
		if !bot.IsValid() {
			reasons[i] = AgentInactiveInvalidConfig
			continue
		}
		if svc, ok := byID(bot.ServiceID); !ok || !llm.IsValidService(svc) {
			reasons[i] = AgentInactiveServiceUnavailable
			continue
		}
		if !slices.Contains(activeServices, bot.ServiceID) {
			reasons[i] = AgentInactiveServiceNotLicensed
			continue
		}
		if capped && active >= limit {
			reasons[i] = AgentInactiveAgentLimit
			continue
		}
		active++
	}
	return reasons
}
