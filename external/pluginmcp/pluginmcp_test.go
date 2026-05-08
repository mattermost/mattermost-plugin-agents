// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bifrostToolNameRe is the regex Bifrost / the Anthropic API apply to every
// tool's `custom.name`. Names that fail it cause an instant "bifrost error"
// before any LLM call is made.
var bifrostToolNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func TestGetUserID_RoundTrip(t *testing.T) {
	ctx := withUserID(context.Background(), "user123")
	require.Equal(t, "user123", GetUserID(ctx))
}

func TestGetUserID_Missing(t *testing.T) {
	require.Equal(t, "", GetUserID(context.Background()))
}

func TestGetUserID_EmptyValue(t *testing.T) {
	ctx := withUserID(context.Background(), "")
	require.Equal(t, "", GetUserID(ctx))
}

func TestSanitizeForToolName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"dotted_plugin_id_replaced", "com.mattermost.plugin-foo", "com_mattermost_plugin-foo"},
		{"hyphenated_plugin_id_noop", "mattermost-ai", "mattermost-ai"},
		{"simple_plugin_id_noop", "playbooks", "playbooks"},
		{"alphanumerics_underscores_noop", "ABC_123", "ABC_123"},
		{"space_replaced", "a b", "a_b"},
		{"slash_replaced", "x/y/z", "x_y_z"},
		{"colon_replaced", "com:mattermost", "com_mattermost"},
		{"at_sign_replaced", "com@plugin", "com_plugin"},
		{"mixed_invalid_runes", "com mattermost/@evil", "com_mattermost__evil"},
		{"non_ascii_replaced", "café", "caf_"},
		{"bifrost_regex_compliance", "com.mattermost.plugin-mcp-demo", "com_mattermost_plugin-mcp-demo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeForToolName(tc.in)
			require.Equal(t, tc.want, got)
			assert.Equal(t, got, sanitizeForToolName(got), "sanitize should be idempotent")
		})
	}
}

// TestSanitizedPrefixIsBifrostCompliant ensures sanitized prefixes plus a
// realistic suffix satisfy Bifrost's tool-name regex.
func TestSanitizedPrefixIsBifrostCompliant(t *testing.T) {
	pluginIDs := []string{
		"com.mattermost.plugin-foo",
		"com.mattermost.plugin-mcp-demo",
		"mattermost-ai",
		"playbooks",
		"ABC_123",
		"com mattermost/@evil",
	}
	suffixes := []string{"echo", "add_two_numbers", "get_user_display_name"}
	for _, pid := range pluginIDs {
		for _, suffix := range suffixes {
			fullName := sanitizeForToolName(pid) + "__" + suffix
			assert.Regexp(t, bifrostToolNameRe, fullName,
				"prefix for %q must produce a Bifrost-compliant tool name (got %q)", pid, fullName)
		}
	}
}
