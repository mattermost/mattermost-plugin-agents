// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import "github.com/mattermost/mattermost-plugin-agents/v2/llm"

// MigrateServiceStructuredOutputPolicies preserves the intent of the removed
// per-agent structured output toggle. An agent that had it enabled used to send
// native JSON schemas; with the toggle gone, the decision belongs to the service
// its agents run on, and an unset policy means automatic detection — which
// answers "not capable" for every provider except direct OpenAI and Gemini. So a
// service used by at least one agent that still carries the deprecated flag is
// pinned to the native policy.
//
// Only an unset policy is filled in, so an explicit administrator choice is
// never overwritten. That also makes the migration idempotent: after it has run,
// every affected service has a policy and a second run changes nothing, which is
// what makes it safe on every activation and on every node of an HA cluster.
//
// Ordering matters for reading the deprecated flag: this runs at activation,
// before any agent can be saved from the UI. The current webapp omits
// structuredOutputEnabled from its payload, and the field is a plain bool, so
// the first UI save of an agent clears the stored value — after that the intent
// is gone. Running at activation is what makes the read reliable.
//
// The opposite direction is deliberately not migrated. A service whose agents
// all had the flag off keeps its unset policy and takes part in automatic
// detection, rather than being pinned off it for the lifetime of the install.
//
// Returns the IDs of the services it changed, in configuration order.
func MigrateServiceStructuredOutputPolicies(cfg *Config, agents []*llm.BotConfig) []string {
	if cfg == nil {
		return nil
	}

	wantsNative := make(map[string]struct{})
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		if !agent.StructuredOutputEnabled { //nolint:staticcheck // the deprecated field is what this migration reads
			continue
		}
		wantsNative[agent.ServiceID] = struct{}{}
	}
	if len(wantsNative) == 0 {
		return nil
	}

	var migrated []string
	for i := range cfg.Services {
		svc := &cfg.Services[i]
		if svc.StructuredOutputPolicy != "" {
			continue
		}
		if _, ok := wantsNative[svc.ID]; !ok {
			continue
		}
		svc.StructuredOutputPolicy = llm.StructuredOutputPolicyNative
		migrated = append(migrated, svc.ID)
	}
	return migrated
}
