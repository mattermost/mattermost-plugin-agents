// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOAuthStateToClear(t *testing.T) {
	jira := ServerConfig{ID: "jira-id", Name: "Jira", BaseURL: "https://jira.example.com/mcp"}
	docs := ServerConfig{ID: "docs-id", Name: "Docs", BaseURL: "https://docs.example.com/mcp"}
	with := func(s ServerConfig, change func(*ServerConfig)) ServerConfig {
		change(&s)
		return s
	}

	tests := []struct {
		name string
		prev []ServerConfig
		next []ServerConfig
		want []OAuthStateClear
	}{
		{
			name: "unchanged servers keep their grants",
			prev: []ServerConfig{jira, docs},
			next: []ServerConfig{jira, with(docs, func(s *ServerConfig) { s.Description = "edited"; s.ClientSecret = "rotated" })},
		},
		{
			name: "a deleted server's grants are cleared",
			prev: []ServerConfig{jira, docs},
			next: []ServerConfig{jira},
			want: []OAuthStateClear{{ServerID: "docs-id", Name: "Docs"}},
		},
		{
			name: "a renamed server clears its old name and anything left under its new one",
			prev: []ServerConfig{jira},
			next: []ServerConfig{with(jira, func(s *ServerConfig) { s.Name = "Jira Cloud" })},
			want: []OAuthStateClear{{ServerID: "jira-id", Name: "Jira"}, {ServerID: "jira-id", Name: "Jira Cloud"}},
		},
		{
			name: "moving to another host clears grants",
			prev: []ServerConfig{jira},
			next: []ServerConfig{with(jira, func(s *ServerConfig) { s.BaseURL = "https://evil.example.com/mcp" })},
			want: []OAuthStateClear{{ServerID: "jira-id", Name: "Jira"}},
		},
		{
			name: "changing the port clears grants",
			prev: []ServerConfig{jira},
			next: []ServerConfig{with(jira, func(s *ServerConfig) { s.BaseURL = "https://jira.example.com:8443/mcp" })},
			want: []OAuthStateClear{{ServerID: "jira-id", Name: "Jira"}},
		},
		{
			name: "changing the scheme clears grants",
			prev: []ServerConfig{jira},
			next: []ServerConfig{with(jira, func(s *ServerConfig) { s.BaseURL = "http://jira.example.com/mcp" })},
			want: []OAuthStateClear{{ServerID: "jira-id", Name: "Jira"}},
		},
		{
			name: "a path-only change keeps grants",
			prev: []ServerConfig{jira},
			next: []ServerConfig{with(jira, func(s *ServerConfig) { s.BaseURL = "https://JIRA.example.com:443/v2/mcp/" })},
		},
		{
			name: "a new server clears state left under its name",
			prev: []ServerConfig{jira},
			next: []ServerConfig{jira, docs},
			want: []OAuthStateClear{{ServerID: "docs-id", Name: "Docs"}},
		},
		{
			name: "a server added in place of a deleted one with its name clears that name once",
			prev: []ServerConfig{docs},
			next: []ServerConfig{{ID: "new-id", Name: "Docs", BaseURL: "https://other.example.com/mcp"}},
			want: []OAuthStateClear{{ServerID: "docs-id", Name: "Docs"}},
		},
		{
			name: "a server saved before IDs existed keeps its grants when its ID is minted",
			prev: []ServerConfig{with(jira, func(s *ServerConfig) { s.ID = "" })},
			next: []ServerConfig{jira},
		},
		{
			name: "a server saved before IDs existed and then deleted is cleared",
			prev: []ServerConfig{with(jira, func(s *ServerConfig) { s.ID = "" })},
			next: nil,
			want: []OAuthStateClear{{Name: "Jira"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, OAuthStateToClear(tt.prev, tt.next))
		})
	}
}

func TestServerNameFromOAuthKey(t *testing.T) {
	const userID = "abcdefghijklmnopqrstuvwxyz"

	tests := []struct {
		name     string
		key      string
		wantName string
		wantOK   bool
	}{
		{"token key", "mcp_oauth_token_v1_" + userID + "_Jira", "Jira", true},
		{"sign-in needed key", "mcp_oauth_needed_v1_" + userID + "_Jira", "Jira", true},
		{"name with underscores", "mcp_oauth_token_v1_" + userID + "_my_jira_v2", "my_jira_v2", true},
		{"name with spaces", "mcp_oauth_token_v1_" + userID + "_Jira Cloud", "Jira Cloud", true},
		{"refresh lease is not cleared", "mcp_oauth_refresh_lease_v1_" + userID + "_Jira", "", false},
		{"client registration is not per server name", "mcp_oauth_client_v2_0123abcd", "", false},
		{"malformed user ID", "mcp_oauth_token_v1_not-a-user-id-at-all_Jira", "", false},
		{"missing name separator", "mcp_oauth_token_v1_" + userID + "Jira", "", false},
		{"unrelated key", "mcp_tools_cache_v1_Jira", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, ok := serverNameFromOAuthKey(tt.key)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantName, name)
		})
	}
}

func TestClearServerOAuthState(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const bob = "bbbbbbbbbbbbbbbbbbbbbbbbbb"

	kv := newMockKVService()
	keep := []string{
		buildTokenKey(alice, "Jira Cloud"), // a name that extends a cleared one
		buildTokenKey(bob, "Ji"),           // a name that is a prefix of a cleared one
		buildTokenKey(alice, "my_jira"),
		buildRefreshLeaseKey(alice, "Jira"),
		"mcp_tools_cache_v1_Jira",
	}
	cleared := []string{
		buildTokenKey(alice, "Jira"),
		buildTokenKey(bob, "Jira"),
		buildAuthNeededKey(bob, "Jira"),
		buildTokenKey(alice, "my_jira_v2"),
	}
	for _, key := range append(slices.Clone(keep), cleared...) {
		_, err := kv.Set(key, "value")
		require.NoError(t, err)
	}

	deleted, err := ClearServerOAuthState(context.Background(), kv, []string{"Jira", "my_jira_v2"})

	require.NoError(t, err)
	require.Equal(t, len(cleared), deleted)
	remaining, err := kv.ListKeys(0, 100)
	require.NoError(t, err)
	require.ElementsMatch(t, keep, remaining)
}

// failingDeleteKV fails deletes of one key.
type failingDeleteKV struct {
	*mockKVService
	failKey string
}

func (f *failingDeleteKV) Delete(key string) error {
	if key == f.failKey {
		return errors.New("kv unavailable")
	}
	return f.mockKVService.Delete(key)
}

func (f *failingDeleteKV) ListKeys(page, count int, options ...pluginapi.ListKeysOption) ([]string, error) {
	return f.mockKVService.ListKeys(page, count, options...)
}

func TestClearServerOAuthStateReportsDeleteErrors(t *testing.T) {
	const alice = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	kv := &failingDeleteKV{mockKVService: newMockKVService(), failKey: buildTokenKey(alice, "Docs")}
	for _, key := range []string{buildTokenKey(alice, "Docs"), buildTokenKey(alice, "Jira")} {
		_, err := kv.Set(key, "value")
		require.NoError(t, err)
	}

	deleted, err := ClearServerOAuthState(context.Background(), kv, []string{"Docs", "Jira"})

	require.Error(t, err)
	require.Equal(t, 1, deleted, "a failed delete does not stop the others")
	remaining, listErr := kv.ListKeys(0, 100)
	require.NoError(t, listErr)
	require.Equal(t, []string{buildTokenKey(alice, "Docs")}, remaining)
}

// A sign-in started before its server changed must not store a grant under
// the server's name, where it would be sent to whatever server holds it now.
func TestProcessCallbackRejectsChangedServer(t *testing.T) {
	const userID = "user123"
	const state = "test-state"
	session := &OAuthSession{
		UserID:        userID,
		ServerID:      "Jira",
		ServerURL:     "https://jira.example.com/mcp",
		CodeVerifier:  "verifier",
		State:         state,
		TokenEndpoint: "https://auth.example.com/token",
		ClientID:      "client",
		CreatedAt:     time.Now(),
	}

	tests := []struct {
		name    string
		servers []ServerConfig
	}{
		{name: "server deleted or renamed", servers: nil},
		{name: "server moved to another origin", servers: []ServerConfig{{Name: "Jira", BaseURL: "https://evil.example.com/mcp", Enabled: true}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(name string) (ServerConfig, bool) {
				for _, s := range tt.servers {
					if s.Name == name {
						return s, true
					}
				}
				return ServerConfig{}, false
			}
			manager, mockClient := setupTestOAuthManagerWithLookup(t, lookup)
			mockClient.On("KVGet", mock.AnythingOfType("string"), mock.AnythingOfType("*mcp.OAuthSession")).Run(func(args mock.Arguments) {
				*args.Get(1).(*OAuthSession) = *session
			}).Return(nil).Once()
			mockClient.On("KVDelete", buildSessionKey(userID, state)).Return(nil).Once()

			result, err := manager.ProcessCallback(context.Background(), userID, state, "auth-code", "")

			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), "configuration changed during sign-in")
			mockClient.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
		})
	}
}
