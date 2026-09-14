// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateStructuredOutputPolicies(t *testing.T) {
	const (
		svcNative = "svcnativesvcnativesvcnativ"
		svcLegacy = "svclegacysvclegacysvclegac"
		svcPlain  = "svcplainsvcplainsvcplainsv"
	)
	services := func(policyOnLegacy llm.StructuredOutputPolicy) []llm.ServiceConfig {
		return []llm.ServiceConfig{
			{ID: svcNative, Name: "native"},
			{ID: svcLegacy, Name: "legacy", StructuredOutputPolicy: policyOnLegacy},
			{ID: svcPlain, Name: "plain"},
		}
	}
	agent := func(name, serviceID string, structuredOutput bool) *llm.BotConfig {
		return &llm.BotConfig{
			Name:                    name,
			DisplayName:             name,
			BotUserID:               "bot-" + name,
			ServiceID:               serviceID,
			StructuredOutputEnabled: structuredOutput, //nolint:staticcheck // the deprecated field is the migration's input
		}
	}
	policies := func(t *testing.T, s *Store) map[string]llm.StructuredOutputPolicy {
		t.Helper()
		cfg, err := s.GetConfig()
		require.NoError(t, err)
		got := make(map[string]llm.StructuredOutputPolicy, len(cfg.Services))
		for _, svc := range cfg.Services {
			got[svc.ID] = svc.StructuredOutputPolicy
		}
		return got
	}

	tests := []struct {
		name          string
		seed          func(t *testing.T, s *Store)
		wantMigrated  []string
		wantPolicies  map[string]llm.StructuredOutputPolicy
		wantNewConfig bool
	}{
		{
			name: "stored agent with the deprecated flag pins its service",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{Services: services("")}, true)
				require.NoError(t, s.CreateAgent(agent("a1", svcNative, true)))
				require.NoError(t, s.CreateAgent(agent("a2", svcPlain, false)))
			},
			wantMigrated:  []string{svcNative},
			wantPolicies:  map[string]llm.StructuredOutputPolicy{svcNative: llm.StructuredOutputPolicyNative, svcLegacy: "", svcPlain: ""},
			wantNewConfig: true,
		},
		{
			name: "legacy config bot not yet copied to the agents table is honoured",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: services(""),
					Bots:     []llm.BotConfig{*agent("legacy-bot", svcLegacy, true)},
				}, true)
			},
			wantMigrated:  []string{svcLegacy},
			wantPolicies:  map[string]llm.StructuredOutputPolicy{svcNative: "", svcLegacy: llm.StructuredOutputPolicyNative, svcPlain: ""},
			wantNewConfig: true,
		},
		{
			name: "explicit administrator choice is never overwritten",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: services(llm.StructuredOutputPolicyPromptFallback),
					Bots:     []llm.BotConfig{*agent("legacy-bot", svcLegacy, true)},
				}, true)
			},
			wantMigrated:  nil,
			wantPolicies:  map[string]llm.StructuredOutputPolicy{svcNative: "", svcLegacy: llm.StructuredOutputPolicyPromptFallback, svcPlain: ""},
			wantNewConfig: false,
		},
		{
			name: "no flagged agents writes nothing",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{Services: services("")}, true)
				require.NoError(t, s.CreateAgent(agent("a1", svcNative, false)))
			},
			wantMigrated:  nil,
			wantPolicies:  map[string]llm.StructuredOutputPolicy{svcNative: "", svcLegacy: "", svcPlain: ""},
			wantNewConfig: false,
		},
		{
			name: "no active config is a no-op",
			seed: func(t *testing.T, s *Store) {
				require.NoError(t, s.CreateAgent(agent("a1", svcNative, true)))
			},
			wantMigrated:  nil,
			wantPolicies:  nil,
			wantNewConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			tt.seed(t, s)
			rowsBefore := configHistoryCount(t, s)

			saved, migrated, err := s.MigrateStructuredOutputPolicies()
			require.NoError(t, err)
			assert.Equal(t, tt.wantMigrated, migrated)

			if tt.wantNewConfig {
				assert.Equal(t, rowsBefore+1, configHistoryCount(t, s), "a new active config row must be written")
				stored, getErr := s.GetConfig()
				require.NoError(t, getErr)
				assert.Equal(t, *stored, saved, "returned config must be what was persisted")
			} else {
				assert.Equal(t, rowsBefore, configHistoryCount(t, s), "nothing to migrate must not write a config row")
			}
			if tt.wantPolicies != nil {
				assert.Equal(t, tt.wantPolicies, policies(t, s))
			}

			// A second run — the same node on its next activation, or an HA
			// follower that lost the mutex race — finds nothing to do and
			// leaves the winner's policies in place.
			_, again, err := s.MigrateStructuredOutputPolicies()
			require.NoError(t, err)
			assert.Empty(t, again)
			if tt.wantPolicies != nil {
				assert.Equal(t, tt.wantPolicies, policies(t, s))
			}
		})
	}
}
