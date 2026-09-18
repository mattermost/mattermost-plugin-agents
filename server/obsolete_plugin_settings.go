// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// obsoleteCredentialSettingKeys lists the plugin setting keys that manifests up
// to v0.3.2 declared for provider account credentials. The whole settings schema
// was replaced by the single Config setting in v0.4.0 and the plugin has read
// none of these since. The server keeps a stored setting under its original key
// after a later manifest stops declaring it, so an installation configured while
// one of those releases was active still carries a value under each of them.
var obsoleteCredentialSettingKeys = []string{
	"OpenAIAPIKey",
	"OpenAICompatibleKey",
	"AnthropicAPIKey",
	"AskSagePassword",
	"MattermostAISecret",
}

// withoutObsoleteCredentialSettings returns a copy of the stored plugin settings
// with every key in obsoleteCredentialSettingKeys removed, and reports whether
// the copy differs from what was passed in. Keys are matched without regard to
// case: the System Console lowercases the keys it writes, while a config.json
// written by an older release holds the casing the manifest declared.
func withoutObsoleteCredentialSettings(stored map[string]any) (map[string]any, bool) {
	obsolete := make(map[string]struct{}, len(obsoleteCredentialSettingKeys))
	for _, key := range obsoleteCredentialSettingKeys {
		obsolete[strings.ToLower(key)] = struct{}{}
	}

	cleaned := make(map[string]any, len(stored))
	changed := false
	for key, value := range stored {
		if _, ok := obsolete[strings.ToLower(key)]; ok {
			changed = true
			continue
		}
		cleaned[key] = value
	}

	return cleaned, changed
}

// removeObsoleteCredentialSettings drops the setting keys the plugin no longer
// reads from the stored plugin configuration.
//
// It reads the unsanitized server configuration, so the settings it writes back
// are the stored values rather than the placeholders the sanitized read returns
// for a setting the manifest marks secret. It writes only when one of those keys
// is actually present, which makes every activation after the first, and every
// activation on a node that lost the race for the migration lock, a no-op. A
// failure to persist is logged and does not stop activation, so an installation
// whose configuration source is read-only keeps working.
func removeObsoleteCredentialSettings(pluginAPI *pluginapi.Client, pluginID string) {
	serverConfig := pluginAPI.Configuration.GetUnsanitizedConfig()
	if serverConfig == nil {
		return
	}

	stored, ok := serverConfig.PluginSettings.Plugins[pluginID]
	if !ok {
		return
	}

	cleaned, changed := withoutObsoleteCredentialSettings(stored)
	if !changed {
		return
	}

	if err := pluginAPI.Configuration.SavePluginConfig(cleaned); err != nil {
		pluginAPI.Log.Warn("Failed to remove keys the plugin no longer reads from the stored plugin configuration", "error", err)
		return
	}

	pluginAPI.Log.Info("Removed keys the plugin no longer reads from the stored plugin configuration")
}
