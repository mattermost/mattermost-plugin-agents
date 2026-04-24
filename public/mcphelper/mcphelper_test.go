// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUserID_RoundTrip verifies withUserID/GetUserID are inverses.
func TestGetUserID_RoundTrip(t *testing.T) {
	ctx := withUserID(context.Background(), "user123")
	require.Equal(t, "user123", GetUserID(ctx))
}

// TestGetUserID_Missing verifies GetUserID returns "" when no user ID was set.
func TestGetUserID_Missing(t *testing.T) {
	require.Equal(t, "", GetUserID(context.Background()))
}

// TestGetUserID_EmptyValue verifies that an explicitly-stored empty string
// round-trips as empty (it's still a valid stored value).
func TestGetUserID_EmptyValue(t *testing.T) {
	ctx := withUserID(context.Background(), "")
	require.Equal(t, "", GetUserID(ctx))
}

// TestSanitizeForToolName is a table-driven unit test on the helper. The MCP
// tool-name validator at go-sdk@v1.4.1/mcp/tool.go:134-140 allows [A-Za-z0-9_\-.],
// so real Mattermost plugin IDs pass through unchanged; the helper is
// defense-in-depth for future plugin IDs containing whitespace, '@', '/', etc.
func TestSanitizeForToolName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"dotted_plugin_id_noop", "com.mattermost.plugin-foo", "com.mattermost.plugin-foo"},
		{"hyphenated_plugin_id_noop", "mattermost-ai", "mattermost-ai"},
		{"simple_plugin_id_noop", "playbooks", "playbooks"},
		{"alphanumerics_underscores_noop", "ABC_123", "ABC_123"},
		{"space_replaced", "a b", "a_b"},
		{"slash_replaced", "x/y/z", "x_y_z"},
		{"colon_replaced", "com:mattermost", "com_mattermost"},
		{"at_sign_replaced", "com@plugin", "com_plugin"},
		{"mixed_invalid_runes", "com mattermost/@evil", "com_mattermost__evil"},
		{"non_ascii_replaced", "café", "caf_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeForToolName(tc.in)
			require.Equal(t, tc.want, got)
			// Idempotency: sanitize(sanitize(x)) == sanitize(x).
			assert.Equal(t, got, sanitizeForToolName(got), "sanitize should be idempotent")
		})
	}
}
