// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
)

// ErrUserPreferencesInvalid indicates normalized preferences violate size or count limits.
var ErrUserPreferencesInvalid = errors.New("invalid user preferences")

// Limits for persisted user MCP provider preferences (stored in the plugin KV store).
// Mattermost's PluginKeyValue.IsValid does not cap value size; these bounds keep
// requests and stored JSON small and predictable (see model.PluginKeyValue in mattermost/server/public).
const (
	UserPreferencesMaxRequestBodyBytes = 256 << 10 // 256 KiB HTTP body cap for PUT /mcp/user-preferences
	UserPreferencesMaxDisabledServers  = 256
	UserPreferencesMaxServerEntryLen   = 512 // max runes per disabled server identifier
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
		if mmapi.IsKVNotFound(err) {
			return &UserToolProviderPreferences{DisabledServers: []string{}}, nil
		}
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
	if err := ValidateUserPreferencesNormalized(prefs); err != nil {
		return nil, err
	}
	if err := pluginAPI.KVSet(userPreferencesKVKey(userID), prefs); err != nil {
		return nil, fmt.Errorf("failed to save user preferences: %w", err)
	}
	return prefs, nil
}

// ValidateUserPreferencesNormalized returns an error if normalized preferences exceed storage limits.
func ValidateUserPreferencesNormalized(prefs *UserToolProviderPreferences) error {
	if prefs == nil {
		return nil
	}
	if len(prefs.DisabledServers) > UserPreferencesMaxDisabledServers {
		return fmt.Errorf("%w: too many disabled_servers (max %d)", ErrUserPreferencesInvalid, UserPreferencesMaxDisabledServers)
	}
	for _, s := range prefs.DisabledServers {
		if len([]rune(s)) > UserPreferencesMaxServerEntryLen {
			return fmt.Errorf("%w: disabled_servers entry exceeds max length (%d runes)", ErrUserPreferencesInvalid, UserPreferencesMaxServerEntryLen)
		}
	}
	return nil
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
