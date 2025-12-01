// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config_migrations

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Migration defines the interface for a configuration migration.
type Migration interface {
	// Key returns the unique KV store key for this migration
	Key() string
	// Run executes the migration, returning whether changes were made
	Run(pluginAPI *pluginapi.Client, cfg config.Config) (changed bool, newCfg config.Config, err error)
}

// ConfigSaver is a function that saves the configuration.
// This abstraction allows the Runner to work with different config wrapper types.
type ConfigSaver func(pluginAPI *pluginapi.Client, cfg config.Config) error

// Runner executes migrations in order with HA-safe locking and rollback.
type Runner struct {
	migrations  []Migration
	mutexAPI    cluster.MutexPluginAPI
	pluginAPI   *pluginapi.Client
	configSaver ConfigSaver
}

// NewRunner creates a new migration runner with the given migrations.
func NewRunner(mutexAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, configSaver ConfigSaver, migrations ...Migration) *Runner {
	return &Runner{
		migrations:  migrations,
		mutexAPI:    mutexAPI,
		pluginAPI:   pluginAPI,
		configSaver: configSaver,
	}
}

// RunAll executes all migrations under a single mutex to prevent race conditions
// in multi-instance deployments. Persists the updated configuration and marks migrations as
// complete only after successful save. Returns the final configuration and any errors encountered.
func (r *Runner) RunAll(cfg config.Config) (config.Config, bool, error) {
	mtx, err := cluster.NewMutex(r.mutexAPI, "ai_all_migrations")
	if err != nil {
		return config.Config{}, false, fmt.Errorf("failed to create migrations mutex: %w", err)
	}
	mtx.Lock()

	changed := false
	completedMigrations := make([]Migration, 0)

	var kvVal []byte

	// Run each migration in order
	for _, migration := range r.migrations {
		if err := r.pluginAPI.KV.Get(migration.Key(), &kvVal); err != nil || kvVal == nil {
			didMigrate, newCfg, migrateErr := migration.Run(r.pluginAPI, cfg)
			if migrateErr != nil {
				mtx.Unlock()
				return cfg, false, fmt.Errorf("failed to run migration %s: %w", migration.Key(), migrateErr)
			}
			if didMigrate {
				changed = true
				completedMigrations = append(completedMigrations, migration)
				cfg = newCfg
				r.pluginAPI.Log.Info("Migration completed", "key", migration.Key())
			}
		}
	}

	if changed {
		// Mark migrations as completed in KV store BEFORE unlocking to prevent race conditions.
		// If the save fails later, we will revert these keys.
		for _, migration := range completedMigrations {
			if _, err := r.pluginAPI.KV.Set(migration.Key(), []byte("true")); err != nil {
				r.pluginAPI.Log.Warn("Failed to mark migration as completed in KV store", "key", migration.Key(), "error", err)
			}
		}

		// Release mutex before saving config to avoid deadlock when SavePluginConfig
		// triggers OnConfigurationChange which tries to acquire the same mutex
		mtx.Unlock()

		if saveErr := r.configSaver(r.pluginAPI, cfg); saveErr != nil {
			// Revert KV keys if save failed, so we retry next time
			mtx.Lock()
			for _, migration := range completedMigrations {
				if err := r.pluginAPI.KV.Delete(migration.Key()); err != nil {
					r.pluginAPI.Log.Warn("Failed to revert migration KV key", "key", migration.Key(), "error", err)
				}
			}
			mtx.Unlock()

			return cfg, false, fmt.Errorf("failed to save migrated configuration: %w", saveErr)
		}

		r.pluginAPI.Log.Info("Configuration persisted after migrations")
	} else {
		mtx.Unlock()
	}

	return cfg, changed, nil
}

// DefaultConfigSaver creates the default config saver that wraps config in a struct with "config" field.
// This matches the plugin's expected configuration structure.
func DefaultConfigSaver(wrapperFactory func(cfg config.Config) any) ConfigSaver {
	return func(pluginAPI *pluginapi.Client, cfg config.Config) error {
		wrapped := wrapperFactory(cfg)

		out := map[string]any{}
		marshalBytes, marshalErr := json.Marshal(wrapped)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal migrated configuration: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(marshalBytes, &out); unmarshalErr != nil {
			return fmt.Errorf("failed to unmarshal migrated configuration: %w", unmarshalErr)
		}

		return pluginAPI.Configuration.SavePluginConfig(out)
	}
}
