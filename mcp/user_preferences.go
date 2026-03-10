// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/mmapi"
)

// UserToolProviderPreferences stores per-user provider toggle state.
type UserToolProviderPreferences struct {
	DisabledServers []string `json:"disabled_servers"`
}

func userPreferencesKVKey(userID string) string {
	return fmt.Sprintf("user_tool_providers_%s", userID)
}

// LoadUserPreferences loads the user's tool provider preferences from KV.
// Returns a default (empty disabled list) when no entry exists.
func LoadUserPreferences(pluginAPI mmapi.Client, userID string) (*UserToolProviderPreferences, error) {
	var prefs UserToolProviderPreferences
	if err := pluginAPI.KVGet(userPreferencesKVKey(userID), &prefs); err != nil {
		return nil, fmt.Errorf("failed to load user preferences: %w", err)
	}
	if prefs.DisabledServers == nil {
		prefs.DisabledServers = []string{}
	}
	return &prefs, nil
}

// SaveUserPreferences normalizes and persists the user's tool provider preferences.
func SaveUserPreferences(pluginAPI mmapi.Client, userID string, prefs *UserToolProviderPreferences) (*UserToolProviderPreferences, error) {
	normalizePreferences(prefs)
	if err := pluginAPI.KVSet(userPreferencesKVKey(userID), prefs); err != nil {
		return nil, fmt.Errorf("failed to save user preferences: %w", err)
	}
	return prefs, nil
}

// normalizePreferences trims blanks, removes empty strings, deduplicates, and
// sorts the disabled servers list for stable persistence and tests.
func normalizePreferences(prefs *UserToolProviderPreferences) {
	if prefs == nil {
		return
	}

	seen := make(map[string]bool, len(prefs.DisabledServers))
	var cleaned []string
	for _, s := range prefs.DisabledServers {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		cleaned = append(cleaned, s)
	}

	sort.Strings(cleaned)

	if cleaned == nil {
		cleaned = []string{}
	}
	prefs.DisabledServers = cleaned
}
