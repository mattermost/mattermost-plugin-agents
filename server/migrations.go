// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/config_migrations"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// runAllMigrations executes all migrations under a single mutex to prevent race conditions
// in multi-instance deployments. Persists the updated configuration and marks migrations as
// complete only after successful save. Returns the final configuration and any errors encountered.
func runAllMigrations(mutexAPI cluster.MutexPluginAPI, pluginAPI *pluginapi.Client, cfg config.Config) (config.Config, bool, error) {
	// Create config saver that wraps the config in the expected structure
	configSaver := config_migrations.DefaultConfigSaver(func(cfg config.Config) any {
		return configuration{Config: cfg}
	})

	runner := config_migrations.NewRunner(
		mutexAPI,
		pluginAPI,
		configSaver,
		&config_migrations.ServicesToBotsMigration{},
		&config_migrations.SeparateServicesFromBotsMigration{},
	)

	return runner.RunAll(cfg)
}
