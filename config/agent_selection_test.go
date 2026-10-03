// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestAgentPoolOrder(t *testing.T) {
	fileBots := []llm.BotConfig{{Name: "file"}}
	dbAgents := []*llm.BotConfig{
		{ID: "b", Name: "newer", CreateAt: 20},
		nil,
		{ID: "c", Name: "tie-c", CreateAt: 10},
		{ID: "a", Name: "tie-a", CreateAt: 10},
	}

	pool := AgentPool(fileBots, dbAgents)

	names := make([]string, 0, len(pool))
	for _, bot := range pool {
		names = append(names, bot.Name)
	}
	require.Equal(t, []string{"file", "tie-a", "tie-c", "newer"}, names)
	require.Equal(t, "b", dbAgents[0].ID, "input slice must not be reordered")
}

func TestAgentInactiveReasons(t *testing.T) {
	services := []llm.ServiceConfig{
		{ID: "first", Type: llm.ServiceTypeOpenAI, APIKey: "key"},
		{ID: "second", Type: llm.ServiceTypeAnthropic, APIKey: "key"},
		{ID: "no-key", Type: llm.ServiceTypeOpenAI},
	}
	agent := func(name, serviceID string) llm.BotConfig {
		return llm.BotConfig{Name: name, DisplayName: name, ServiceID: serviceID}
	}

	tests := []struct {
		name  string
		level enterprise.Level
		pool  []llm.BotConfig
		want  []AgentInactiveReason
	}{
		{
			name:  "unlicensed keeps the first agent on the first service",
			level: enterprise.LevelUnlicensed,
			pool:  []llm.BotConfig{agent("a", "first"), agent("b", "first")},
			want:  []AgentInactiveReason{AgentActive, AgentInactiveAgentLimit},
		},
		{
			name:  "unlicensed agent on a second service takes no slot",
			level: enterprise.LevelUnlicensed,
			pool:  []llm.BotConfig{agent("a", "second"), agent("b", "first")},
			want:  []AgentInactiveReason{AgentInactiveServiceNotLicensed, AgentActive},
		},
		{
			name:  "unlicensed with every agent on a second service",
			level: enterprise.LevelUnlicensed,
			pool:  []llm.BotConfig{agent("a", "second"), agent("b", "second")},
			want:  []AgentInactiveReason{AgentInactiveServiceNotLicensed, AgentInactiveServiceNotLicensed},
		},
		{
			name:  "professional caps at three",
			level: enterprise.LevelProfessional,
			pool:  []llm.BotConfig{agent("a", "first"), agent("b", "first"), agent("c", "first"), agent("d", "first")},
			want:  []AgentInactiveReason{AgentActive, AgentActive, AgentActive, AgentInactiveAgentLimit},
		},
		{
			name:  "enterprise runs agents on every service",
			level: enterprise.LevelEnterprise,
			pool:  []llm.BotConfig{agent("a", "first"), agent("b", "second")},
			want:  []AgentInactiveReason{AgentActive, AgentActive},
		},
		{
			name:  "deleted service at enterprise",
			level: enterprise.LevelEnterprise,
			pool:  []llm.BotConfig{agent("a", "deleted"), agent("b", "first")},
			want:  []AgentInactiveReason{AgentInactiveServiceUnavailable, AgentActive},
		},
		{
			name:  "deleted service below enterprise takes no slot",
			level: enterprise.LevelUnlicensed,
			pool:  []llm.BotConfig{agent("a", "deleted"), agent("b", "first")},
			want:  []AgentInactiveReason{AgentInactiveServiceUnavailable, AgentActive},
		},
		{
			name:  "service without credentials",
			level: enterprise.LevelEnterpriseAdvanced,
			pool:  []llm.BotConfig{agent("a", "no-key")},
			want:  []AgentInactiveReason{AgentInactiveServiceUnavailable},
		},
		{
			name:  "invalid agent configuration",
			level: enterprise.LevelEnterpriseAdvanced,
			pool:  []llm.BotConfig{{Name: "a", ServiceID: "first"}},
			want:  []AgentInactiveReason{AgentInactiveInvalidConfig},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, AgentInactiveReasons(services, tc.pool, tc.level))
		})
	}
}
