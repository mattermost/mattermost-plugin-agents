// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost-plugin-ai/config"
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
// Post-migration, config updates flow exclusively through PUT /admin/config -> DB -> cluster event.
// OnConfigurationChange is only triggered by Mattermost when config.json changes, which no longer
// happens after the one-time migration. This method is kept as a no-op (removed entirely in Phase 4).
func (p *Plugin) OnConfigurationChange() error {
	return nil
}
