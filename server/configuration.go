// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	config.Config `json:"config"`
}

// Clone deep copies the configuration to handle reference types properly.
func (c *configuration) Clone() *configuration {
	if c == nil {
		return nil
	}

	return &configuration{
		Config: *c.Config.Clone(),
	}
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)
	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return fmt.Errorf("failed to load plugin configuration: %w", err)
	}

	// Run migrations on the newly loaded configuration
	pluginAPI := pluginapi.NewClient(p.API, p.Driver)
	potentiallyUpdatedConfig, wasUpdated, err := runAllMigrations(p.API, pluginAPI, configuration.Config)
	if err != nil {
		pluginAPI.Log.Error("Failed to run migrations on configuration change", "error", err)
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Update in-memory representation with the final (potentially migrated) config
	// The save to disk is already handled by runAllMigrations if changes were made
	finalConfig := configuration.Config
	if wasUpdated {
		finalConfig = potentiallyUpdatedConfig
		pluginAPI.Log.Info("Configuration migrated in OnConfigurationChange")
	}
	p.configuration.Update(&finalConfig)

	return nil
}
