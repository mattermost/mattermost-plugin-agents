// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
)

func TestMigrateServiceStructuredOutputPolicies(t *testing.T) {
	service := func(id string, policy llm.StructuredOutputPolicy) llm.ServiceConfig {
		return llm.ServiceConfig{
			ID:                     id,
			Type:                   llm.ServiceTypeAnthropic,
			APIKey:                 "key",
			DefaultModel:           "claude-sonnet-4-5",
			StructuredOutputPolicy: policy,
		}
	}
	agent := func(serviceID string, structuredOutput bool) *llm.BotConfig {
		return &llm.BotConfig{
			Name:                    "agent-" + serviceID,
			ServiceID:               serviceID,
			StructuredOutputEnabled: structuredOutput, //nolint:staticcheck // the deprecated field is the migration's input
		}
	}

	tests := []struct {
		name           string
		services       []llm.ServiceConfig
		agents         []*llm.BotConfig
		wantMigratedID []string
		wantPolicies   map[string]llm.StructuredOutputPolicy
	}{
		{
			name:           "no agents leaves every policy unset",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			wantMigratedID: nil,
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": ""},
		},
		{
			name:           "an agent with the deprecated flag pins its service to native",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			agents:         []*llm.BotConfig{agent("svc-a", true)},
			wantMigratedID: []string{"svc-a"},
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": llm.StructuredOutputPolicyNative},
		},
		{
			name:           "agents with the flag off change nothing",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			agents:         []*llm.BotConfig{agent("svc-a", false), agent("svc-a", false)},
			wantMigratedID: nil,
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": ""},
		},
		{
			name:           "one agent with the flag is enough for a shared service",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			agents:         []*llm.BotConfig{agent("svc-a", false), agent("svc-a", true)},
			wantMigratedID: []string{"svc-a"},
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": llm.StructuredOutputPolicyNative},
		},
		{
			name:           "only the services those agents use are migrated",
			services:       []llm.ServiceConfig{service("svc-a", ""), service("svc-b", "")},
			agents:         []*llm.BotConfig{agent("svc-a", true), agent("svc-b", false)},
			wantMigratedID: []string{"svc-a"},
			wantPolicies: map[string]llm.StructuredOutputPolicy{
				"svc-a": llm.StructuredOutputPolicyNative,
				"svc-b": "",
			},
		},
		{
			name:           "an explicit administrator choice is never overwritten",
			services:       []llm.ServiceConfig{service("svc-a", llm.StructuredOutputPolicyPromptFallback)},
			agents:         []*llm.BotConfig{agent("svc-a", true)},
			wantMigratedID: nil,
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": llm.StructuredOutputPolicyPromptFallback},
		},
		{
			name:           "an agent referencing an unknown service is ignored",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			agents:         []*llm.BotConfig{agent("svc-missing", true)},
			wantMigratedID: nil,
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": ""},
		},
		{
			name:           "a nil agent entry is skipped",
			services:       []llm.ServiceConfig{service("svc-a", "")},
			agents:         []*llm.BotConfig{nil, agent("svc-a", true)},
			wantMigratedID: []string{"svc-a"},
			wantPolicies:   map[string]llm.StructuredOutputPolicy{"svc-a": llm.StructuredOutputPolicyNative},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Services: tt.services}

			migrated := MigrateServiceStructuredOutputPolicies(cfg, tt.agents)
			assert.Equal(t, tt.wantMigratedID, migrated)

			policies := make(map[string]llm.StructuredOutputPolicy, len(cfg.Services))
			for _, svc := range cfg.Services {
				policies[svc.ID] = svc.StructuredOutputPolicy
			}
			assert.Equal(t, tt.wantPolicies, policies)

			// Running again on the migrated configuration must be a no-op.
			assert.Empty(t, MigrateServiceStructuredOutputPolicies(cfg, tt.agents), "the migration must be idempotent")
			for _, svc := range cfg.Services {
				assert.Equal(t, tt.wantPolicies[svc.ID], svc.StructuredOutputPolicy)
			}
		})
	}
}
