// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// clearMigratedPluginSettings empties the plugin's entry in the server
// configuration. Once the configuration lives in the database that entry is a
// stale copy nothing reads, yet it still holds the credentials it was migrated
// with. Must only run after the database migration has succeeded.
func clearMigratedPluginSettings(pluginAPI *pluginapi.Client) {
	if len(pluginAPI.Configuration.GetPluginConfig()) == 0 {
		return
	}

	if err := pluginAPI.Configuration.SavePluginConfig(map[string]any{}); err != nil {
		pluginAPI.Log.Warn("Failed to clear migrated plugin settings from the server configuration", "error", err)
		return
	}

	pluginAPI.Log.Info("Cleared migrated plugin settings from the server configuration")
}
