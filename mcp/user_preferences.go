// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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

const userPreferencesKVPrefix = "user_tool_providers_"

func userPreferencesKVKey(userID string) string {
	return userPreferencesKVPrefix + userID
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

	slices.Sort(cleaned)

	if cleaned == nil {
		cleaned = []string{}
	}
	prefs.DisabledServers = cleaned
}

var errUserPreferencesUnchanged = errors.New("user preferences unchanged")

// UserPreferencesKVStore is the subset of pluginapi.KVService needed to remap
// persisted per-user provider preferences when a server URL changes.
type UserPreferencesKVStore interface {
	ListKeys(page, count int, options ...pluginapi.ListKeysOption) ([]string, error)
	SetAtomicWithRetries(key string, valueFunc func(oldValue []byte) (newValue any, err error)) error
}

func remapDisabledServers(disabled []string, remaps map[string]string) ([]string, bool) {
	if len(disabled) == 0 || len(remaps) == 0 {
		return disabled, false
	}

	matched := false
	for _, s := range disabled {
		if nextOrigin, ok := remaps[llm.NormalizeMCPServerOrigin(s)]; ok && s != nextOrigin {
			matched = true
			break
		}
	}
	if !matched {
		return disabled, false
	}

	out := make([]string, len(disabled))
	for i, s := range disabled {
		if nextOrigin, ok := remaps[llm.NormalizeMCPServerOrigin(s)]; ok && s != nextOrigin {
			out[i] = nextOrigin
		} else {
			out[i] = s
		}
	}
	return out, true
}

// RemapUserPreferencesOrigins updates every user's stored disabled-server
// preference that matches an old normalized origin in remaps to the server's
// new URL, using compare-and-set so concurrent user toggles are preserved.
func RemapUserPreferencesOrigins(ctx context.Context, kv UserPreferencesKVStore, remaps map[string]string) (int, error) {
	if len(remaps) == 0 || kv == nil {
		return 0, nil
	}

	var keys []string
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		pageKeys, err := kv.ListKeys(page, listKeysPageSize)
		if err != nil {
			return 0, fmt.Errorf("failed to list user preference KV keys: %w", err)
		}
		for _, key := range pageKeys {
			if strings.HasPrefix(key, userPreferencesKVPrefix) && len(key) > len(userPreferencesKVPrefix) {
				keys = append(keys, key)
			}
		}
		if len(pageKeys) < listKeysPageSize {
			break
		}
	}

	updated := 0
	var errs []error
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return updated, errors.Join(append(errs, err)...)
		}
		err := kv.SetAtomicWithRetries(key, func(oldValue []byte) (any, error) {
			if len(oldValue) == 0 {
				return nil, errUserPreferencesUnchanged
			}
			var prefs UserToolProviderPreferences
			if unmarshalErr := json.Unmarshal(oldValue, &prefs); unmarshalErr != nil {
				return nil, fmt.Errorf("failed to decode user preferences: %w", unmarshalErr)
			}
			remapped, changed := remapDisabledServers(prefs.DisabledServers, remaps)
			if !changed {
				return nil, errUserPreferencesUnchanged
			}
			prefs.DisabledServers = remapped
			normalizePreferences(&prefs)
			return &prefs, nil
		})
		if errors.Is(err, errUserPreferencesUnchanged) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to remap user preferences: %w", err))
			continue
		}
		updated++
	}
	return updated, errors.Join(errs...)
}

const maxUserPreferencesConvergePasses = 5

// RemapUserPreferencesConverging updates persisted per-user disabled-server
// preferences for initialRemaps while converging against the latest saved
// server URLs returned by loadServers. If a concurrent config save edits one
// of the same servers again (for example A->B racing with B->C) before or
// during a pass, the intermediate target is folded into the tracked old URLs
// and another pass runs against the newest URL so preferences never stall at
// an intermediate value.
func RemapUserPreferencesConverging(
	ctx context.Context,
	kv UserPreferencesKVStore,
	initialRemaps []config.MCPServerURLRemap,
	loadServers func() ([]ServerConfig, error),
) (int, error) {
	if len(initialRemaps) == 0 || kv == nil {
		return 0, nil
	}

	type trackedServer struct {
		serverID   string
		serverName string
		originRef  string
		oldNorms   map[string]struct{}
		targetURL  string
	}

	tracked := make([]*trackedServer, 0, len(initialRemaps))
	for _, r := range initialRemaps {
		oldNorm := llm.NormalizeMCPServerOrigin(r.OldURL)
		if oldNorm == "" || r.NewURL == "" {
			continue
		}
		tracked = append(tracked, &trackedServer{
			serverID:   r.ServerID,
			serverName: r.ServerName,
			originRef:  r.OldURL,
			oldNorms:   map[string]struct{}{oldNorm: {}},
			targetURL:  r.NewURL,
		})
	}
	if len(tracked) == 0 {
		return 0, nil
	}

	resolveLatest := func() (bool, error) {
		if loadServers == nil {
			return false, nil
		}
		servers, err := loadServers()
		if err != nil {
			return false, fmt.Errorf("failed to load current MCP servers for preference remap: %w", err)
		}
		byID := make(map[string]ServerConfig, len(servers))
		byName := make(map[string]ServerConfig, len(servers))
		for _, s := range servers {
			if s.ID != "" {
				byID[s.ID] = s
			}
			byName[s.Name] = s
		}

		advanced := false
		for _, t := range tracked {
			if t.targetURL == "" {
				continue
			}
			var (
				cur ServerConfig
				ok  bool
			)
			if t.serverID != "" {
				cur, ok = byID[t.serverID]
			} else {
				cur, ok = byName[t.serverName]
			}
			if !ok || cur.BaseURL == "" || originChanged(t.originRef, cur.BaseURL) {
				t.targetURL = ""
				continue
			}
			if cur.BaseURL != t.targetURL {
				if prevNorm := llm.NormalizeMCPServerOrigin(t.targetURL); prevNorm != "" {
					t.oldNorms[prevNorm] = struct{}{}
				}
				t.targetURL = cur.BaseURL
				advanced = true
			}
		}
		return advanced, nil
	}

	if _, err := resolveLatest(); err != nil {
		return 0, err
	}

	totalUpdated := 0
	for range maxUserPreferencesConvergePasses {
		active := make(map[string]string)
		for _, t := range tracked {
			if t.targetURL == "" {
				continue
			}
			for oldNorm := range t.oldNorms {
				active[oldNorm] = t.targetURL
			}
		}
		if len(active) == 0 {
			break
		}

		n, err := RemapUserPreferencesOrigins(ctx, kv, active)
		totalUpdated += n
		if err != nil {
			return totalUpdated, err
		}

		if loadServers == nil {
			break
		}
		advanced, err := resolveLatest()
		if err != nil {
			return totalUpdated, err
		}
		if !advanced {
			break
		}
	}

	return totalUpdated, nil
}
