// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost/server/public/model"
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

	p.configuration.Update(&configuration.Config)

	return nil
}

// ConfigurationWillBeSaved is invoked before the configuration is saved to the database.
func (p *Plugin) ConfigurationWillBeSaved(newCfg *model.Config) (*model.Config, error) {
	if newCfg == nil || newCfg.PluginSettings.Plugins == nil {
		return newCfg, nil
	}

	pluginSettings, ok := newCfg.PluginSettings.Plugins["mattermost-ai"]
	if !ok {
		return newCfg, nil
	}

	migratedSettings, changed, err := MigratePluginConfig(pluginSettings)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate plugin configuration: %w", err)
	}

	if changed {
		newCfg.PluginSettings.Plugins["mattermost-ai"] = migratedSettings
		p.API.LogInfo("Configuration migrated in ConfigurationWillBeSaved")
	}

	return newCfg, nil
}
