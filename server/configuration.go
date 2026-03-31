// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost-plugin-ai/config"
)

// configuration captures the plugin's external configuration structure.
// It is used during the one-time config data migration (config.json -> DB)
// via LoadPluginConfiguration.
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
