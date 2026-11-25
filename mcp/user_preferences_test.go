// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
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

type hookAtomicKV struct {
	*atomicMockKVService
	beforeWrite func()
}

func (h *hookAtomicKV) SetAtomicWithRetries(key string, valueFunc func(oldValue []byte) (any, error)) error {
	if h.beforeWrite != nil {
		h.beforeWrite()
	}
	return h.atomicMockKVService.SetAtomicWithRetries(key, valueFunc)
}

func TestRemapUserPreferencesConvergingConcurrentEdits(t *testing.T) {
	const (
		serverID = "atl-id"
		urlA     = "https://mcp.atlassian.com/v2/mcp"
		urlB     = "https://mcp.atlassian.com/v2/mcp?tools=basic"
		urlC     = "https://mcp.atlassian.com/v2/mcp?tools=all"
		urlOther = "https://other.atlassian.com/v2/mcp"
	)

	tests := []struct {
		name string
		run  func(t *testing.T, kv *atomicMockKVService)
		want []string
	}{
		{
			name: "out-of-order sweeps: B->C runs before A->B and converges to C",
			run: func(t *testing.T, kv *atomicMockKVService) {
				latest := []ServerConfig{{ID: serverID, Name: "Atlassian", BaseURL: urlC}}
				load := func() ([]ServerConfig, error) { return latest, nil }

				// B->C sweeps first while KV still has A: no-op.
				updatedBC, err := RemapUserPreferencesConverging(context.Background(), kv,
					[]config.MCPServerURLRemap{{ServerID: serverID, ServerName: "Atlassian", OldURL: urlB, NewURL: urlC}}, load)
				require.NoError(t, err)
				require.Equal(t, 0, updatedBC)

				// A->B runs afterwards, sees latest saved URL is C, and remaps A directly to C.
				updatedAB, err := RemapUserPreferencesConverging(context.Background(), kv,
					[]config.MCPServerURLRemap{{ServerID: serverID, ServerName: "Atlassian", OldURL: urlA, NewURL: urlB}}, load)
				require.NoError(t, err)
				require.Equal(t, 1, updatedAB)
			},
			want: []string{urlC},
		},
		{
			name: "mid-flight advance: B->C commits and sweeps after A->B resolved B but before A->B writes B",
			run: func(t *testing.T, kv *atomicMockKVService) {
				var mu sync.Mutex
				currentURL := urlB
				load := func() ([]ServerConfig, error) {
					mu.Lock()
					defer mu.Unlock()
					return []ServerConfig{{ID: serverID, Name: "Atlassian", BaseURL: currentURL}}, nil
				}

				advancedOnce := false
				hookedKV := &hookAtomicKV{
					atomicMockKVService: kv,
					beforeWrite: func() {
						mu.Lock()
						if advancedOnce {
							mu.Unlock()
							return
						}
						advancedOnce = true
						// Simulate concurrent B->C committing C and running its sweep
						// while A->B has already resolved B and is about to write B.
						currentURL = urlC
						mu.Unlock()

						updatedBC, err := RemapUserPreferencesConverging(context.Background(), kv,
							[]config.MCPServerURLRemap{{ServerID: serverID, ServerName: "Atlassian", OldURL: urlB, NewURL: urlC}}, load)
						require.NoError(t, err)
						require.Equal(t, 0, updatedBC)
					},
				}

				updatedAB, err := RemapUserPreferencesConverging(context.Background(), hookedKV,
					[]config.MCPServerURLRemap{{ServerID: serverID, ServerName: "Atlassian", OldURL: urlA, NewURL: urlB}}, load)
				require.NoError(t, err)
				// Pass 1 writes B, post-write check observes C != B, pass 2 writes C.
				require.Equal(t, 2, updatedAB)
			},
			want: []string{urlC},
		},
		{
			name: "overlapping origin across two servers: X(A->B->D) does not collide with settled Y(B->C)",
			run: func(t *testing.T, kv *atomicMockKVService) {
				const urlD = "https://mcp.atlassian.com/v2/mcp?tools=custom"
				var mu sync.Mutex
				servers := []ServerConfig{
					{ID: "srv-x", Name: "X", BaseURL: urlB},
					{ID: "srv-y", Name: "Y", BaseURL: urlC},
				}
				load := func() ([]ServerConfig, error) {
					mu.Lock()
					defer mu.Unlock()
					return append([]ServerConfig(nil), servers...), nil
				}

				advancedOnce := false
				hookedKV := &hookAtomicKV{
					atomicMockKVService: kv,
					beforeWrite: func() {
						mu.Lock()
						if advancedOnce {
							mu.Unlock()
							return
						}
						advancedOnce = true
						servers[0].BaseURL = urlD
						mu.Unlock()
					},
				}

				_, err := RemapUserPreferencesConverging(context.Background(), hookedKV, []config.MCPServerURLRemap{
					{ServerID: "srv-x", ServerName: "X", OldURL: urlA, NewURL: urlB},
					{ServerID: "srv-y", ServerName: "Y", OldURL: urlB, NewURL: urlC},
				}, load)
				require.NoError(t, err)
			},
			want: []string{"https://mcp.atlassian.com/v2/mcp?tools=custom"},
		},
		{
			name: "concurrent cross-origin host move stops remapping",
			run: func(t *testing.T, kv *atomicMockKVService) {
				load := func() ([]ServerConfig, error) {
					return []ServerConfig{{ID: serverID, Name: "Atlassian", BaseURL: urlOther}}, nil
				}
				updated, err := RemapUserPreferencesConverging(context.Background(), kv,
					[]config.MCPServerURLRemap{{ServerID: serverID, ServerName: "Atlassian", OldURL: urlA, NewURL: urlB}}, load)
				require.NoError(t, err)
				require.Equal(t, 0, updated)
			},
			want: []string{urlA},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := &atomicMockKVService{mockKVService: newMockKVService()}
			_, err := kv.Set(userPreferencesKVKey("bob"), UserToolProviderPreferences{DisabledServers: []string{urlA}})
			require.NoError(t, err)

			tt.run(t, kv)

			raw := kv.store[userPreferencesKVKey("bob")]
			var got UserToolProviderPreferences
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, tt.want, got.DisabledServers)
		})
	}
}
