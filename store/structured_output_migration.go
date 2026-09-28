// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

var errNoStructuredOutputMigration = errors.New("no service needs a structured output policy")

// MigrateStructuredOutputPolicies pins services to the native structured
// output policy when an agent that uses them still carries the deprecated
// per-agent toggle (see config.MigrateServiceStructuredOutputPolicies). Both
// stored agents and legacy config.Bots that the bot migration has not yet
// copied into the agents table are considered, so the intent survives
// whichever order those migrations run in.
//
// The read-modify-write happens in one UpdateConfig transaction, so a
// concurrent admin save cannot be overwritten. Nothing is written when no
// service changes. Returns the saved config and the IDs of the services it
// changed; the caller must reload runtime config from the store even when
// migrated is empty, because another node may have written the policies first.
func (s *Store) MigrateStructuredOutputPolicies() (config.Config, []string, error) {
	agents, err := s.ListAgents()
	if err != nil {
		return config.Config{}, nil, err
	}

	var migrated []string
	saved, err := s.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		if prev == nil || len(prev.Services) == 0 {
			return config.Config{}, errNoStructuredOutputMigration
		}
		next, copyErr := config.DeepCopyJSON(*prev)
		if copyErr != nil {
			return config.Config{}, copyErr
		}

		candidates := make([]*llm.BotConfig, 0, len(agents)+len(prev.Bots))
		candidates = append(candidates, agents...)
		for i := range prev.Bots {
			candidates = append(candidates, &prev.Bots[i])
		}

		migrated = config.MigrateServiceStructuredOutputPolicies(&next, candidates)
		if len(migrated) == 0 {
			return config.Config{}, errNoStructuredOutputMigration
		}
		return next, nil
	})
	if errors.Is(err, errNoStructuredOutputMigration) {
		return config.Config{}, nil, nil
	}
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("failed to migrate structured output policies: %w", err)
	}
	return saved, migrated, nil
}
