// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildHookCallbackURL_AcceptsScopedPath(t *testing.T) {
	got, err := buildHookCallbackURL("com.example.plugin", "/hooks/before")
	require.NoError(t, err)
	assert.Equal(t, "/plugins/com.example.plugin/hooks/before", got)
}

func TestBuildHookCallbackURL_RejectsBadCallbackPaths(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		errContains string
	}{
		{"missing leading slash", "hooks/before", "must start with /"},
		{"empty", "", "must start with /"},
		{"parent traversal", "/hooks/../../api/v4/users/me", "escapes plugin namespace"},
		{"parent traversal at root", "/..", "escapes plugin namespace"},
		{"parent traversal in middle", "/foo/../../bar", "escapes plugin namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildHookCallbackURL("com.example.plugin", tc.path)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestBuildHookCallbackURL_RejectsEmptyPluginID(t *testing.T) {
	_, err := buildHookCallbackURL(" ", "/hooks/before")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing hook plugin id")
}

func TestBeforeHookStore_IssueStoresToolBoundEntry(t *testing.T) {
	kv := newMockKVService()
	store := NewBeforeHookStore(kv)

	key, err := store.Issue("user-1", "search_posts", " com.example.plugin ", "/hooks/before")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key, beforeHookKeyPrefix))
	require.Len(t, key, len(beforeHookKeyPrefix)+beforeHookSecretLength)

	var entry BeforeHookEntry
	require.NoError(t, kv.Get(key, &entry))
	require.Equal(t, BeforeHookEntry{
		UserID:      "user-1",
		ToolName:    "search_posts",
		CallbackURL: "/plugins/com.example.plugin/hooks/before",
	}, entry)
}

func TestBeforeHookStore_ResolveRequiresMatchingTool(t *testing.T) {
	kv := newMockKVService()
	store := NewBeforeHookStore(kv)

	key, err := store.Issue("user-1", "search_posts", "com.example.plugin", "/hooks/before")
	require.NoError(t, err)

	_, err = store.Resolve("user-1", "create_post", key)
	require.ErrorIs(t, err, ErrBeforeHookKeyNotFound)

	var entry BeforeHookEntry
	require.NoError(t, kv.Get(key, &entry))
	require.Equal(t, "search_posts", entry.ToolName, "mismatched tool should not consume the key")
}

func TestBeforeHookStore_ResolveKeepsKeyUntilTTL(t *testing.T) {
	kv := newMockKVService()
	store := NewBeforeHookStore(kv)

	key, err := store.Issue("user-1", "search_posts", "com.example.plugin", "/hooks/before")
	require.NoError(t, err)

	entry, err := store.Resolve("user-1", "search_posts", key)
	require.NoError(t, err)
	require.Equal(t, "/plugins/com.example.plugin/hooks/before", entry.CallbackURL)

	entry, err = store.Resolve("user-1", "search_posts", key)
	require.NoError(t, err)
	require.Equal(t, "/plugins/com.example.plugin/hooks/before", entry.CallbackURL)
}
