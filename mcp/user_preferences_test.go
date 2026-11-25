// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateUserPreferencesNormalized(t *testing.T) {
	t.Run("allows empty list", func(t *testing.T) {
		p := &UserToolProviderPreferences{DisabledServers: []string{}}
		require.NoError(t, ValidateUserPreferencesNormalized(p))
	})

	t.Run("rejects too many entries", func(t *testing.T) {
		servers := make([]string, UserPreferencesMaxDisabledServers+1)
		for i := range servers {
			servers[i] = "s"
		}
		p := &UserToolProviderPreferences{DisabledServers: servers}
		err := ValidateUserPreferencesNormalized(p)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUserPreferencesInvalid)
	})

	t.Run("rejects entry too long", func(t *testing.T) {
		long := strings.Repeat("x", UserPreferencesMaxServerEntryLen+1)
		p := &UserToolProviderPreferences{DisabledServers: []string{long}}
		err := ValidateUserPreferencesNormalized(p)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUserPreferencesInvalid)
	})
}

type atomicMockKVService struct {
	*mockKVService
}

func (a *atomicMockKVService) SetAtomicWithRetries(key string, valueFunc func(oldValue []byte) (any, error)) error {
	a.mu.Lock()
	old := append([]byte(nil), a.store[key]...)
	a.mu.Unlock()

	newVal, err := valueFunc(old)
	if err != nil {
		return err
	}
	_, err = a.Set(key, newVal)
	return err
}

func TestRemapUserPreferencesOrigins(t *testing.T) {
	const (
		oldURL   = "https://mcp.atlassian.com/v2/mcp"
		newURL   = "https://mcp.atlassian.com/v2/mcp?tools=all"
		otherURL = "https://github.example.com/mcp"
	)

	tests := []struct {
		name        string
		initial     map[string]UserToolProviderPreferences
		remaps      map[string]string
		wantUpdated int
		wantAfter   map[string][]string
	}{
		{
			name: "remaps matching user preferences and leaves unrelated users untouched",
			initial: map[string]UserToolProviderPreferences{
				"alice": {DisabledServers: []string{oldURL, otherURL}},
				"bob":   {DisabledServers: []string{otherURL}},
				"carol": {DisabledServers: []string{oldURL + "/"}},
			},
			remaps:      map[string]string{oldURL: newURL},
			wantUpdated: 2,
			wantAfter: map[string][]string{
				"alice": {otherURL, newURL},
				"bob":   {otherURL},
				"carol": {newURL},
			},
		},
		{
			name: "deduplicates when both old and new URLs were present",
			initial: map[string]UserToolProviderPreferences{
				"alice": {DisabledServers: []string{oldURL, newURL}},
			},
			remaps:      map[string]string{oldURL: newURL},
			wantUpdated: 1,
			wantAfter: map[string][]string{
				"alice": {newURL},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := &atomicMockKVService{mockKVService: newMockKVService()}
			for userID, prefs := range tt.initial {
				_, err := kv.Set(userPreferencesKVKey(userID), prefs)
				require.NoError(t, err)
			}
			// Unrelated key must be ignored.
			_, err := kv.Set("mcp_oauth_token_v1_alice_Atlassian", "token")
			require.NoError(t, err)

			updated, err := RemapUserPreferencesOrigins(context.Background(), kv, tt.remaps)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdated, updated)

			for userID, wantDisabled := range tt.wantAfter {
				raw := kv.store[userPreferencesKVKey(userID)]
				var got UserToolProviderPreferences
				require.NoError(t, json.Unmarshal(raw, &got))
				require.Equal(t, wantDisabled, got.DisabledServers)
			}
		})
	}
}
