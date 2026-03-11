// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSearchTermWithChannel(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		channelName string
		expected    string
	}{
		{
			name:        "simple query with channel name",
			query:       "bug fix",
			channelName: "town-square",
			expected:    "in:town-square bug fix",
		},
		{
			name:        "channel name with hyphens",
			query:       "release notes",
			channelName: "release-announcements-2024",
			expected:    "in:release-announcements-2024 release notes",
		},
		{
			name:        "query already containing in: modifier",
			query:       "in:other-channel error",
			channelName: "dev",
			expected:    "in:dev in:other-channel error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSearchTermWithChannel(tt.query, tt.channelName)
			if got != tt.expected {
				t.Errorf("buildSearchTermWithChannel(%q, %q) = %q, want %q", tt.query, tt.channelName, got, tt.expected)
			}
		})
	}
}

func TestSearchPostsArgsChannelID(t *testing.T) {
	tests := []struct {
		name              string
		jsonInput         string
		expectedQuery     string
		expectedChannelID string
		expectedTeamID    string
		expectedLimit     int
	}{
		{
			name:              "with channel ID",
			jsonInput:         `{"query": "bug fix", "channel_id": "abcdefghijklmnopqrstuvwxyz"}`,
			expectedQuery:     "bug fix",
			expectedChannelID: "abcdefghijklmnopqrstuvwxyz",
			expectedLimit:     0,
		},
		{
			name:              "no channel ID field",
			jsonInput:         `{"query": "search term"}`,
			expectedQuery:     "search term",
			expectedChannelID: "",
			expectedLimit:     0,
		},
		{
			name:              "channel ID with team ID and limit",
			jsonInput:         `{"query": "release notes", "team_id": "abcdefghijklmnopqrstuvwxyz", "channel_id": "zyxwvutsrqponmlkjihgfedcba", "limit": 50}`,
			expectedQuery:     "release notes",
			expectedChannelID: "zyxwvutsrqponmlkjihgfedcba",
			expectedTeamID:    "abcdefghijklmnopqrstuvwxyz",
			expectedLimit:     50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args SearchPostsArgs
			err := json.Unmarshal([]byte(tt.jsonInput), &args)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedQuery, args.Query)
			assert.Equal(t, tt.expectedChannelID, args.ChannelID)
			assert.Equal(t, tt.expectedTeamID, args.TeamID)
			assert.Equal(t, tt.expectedLimit, args.Limit)
		})
	}
}

func TestSearchPostsArgsSchema(t *testing.T) {
	schema := llm.NewJSONSchemaFromStruct[SearchPostsArgs]()

	require.NotNil(t, schema)
	require.NotNil(t, schema.Properties)

	tests := []struct {
		name     string
		field    string
		expected bool
	}{
		{
			name:     "query property exists",
			field:    "query",
			expected: true,
		},
		{
			name:     "channel_id property exists",
			field:    "channel_id",
			expected: true,
		},
		{
			name:     "team_id property exists",
			field:    "team_id",
			expected: true,
		},
		{
			name:     "limit property exists",
			field:    "limit",
			expected: true,
		},
		{
			name:     "nonexistent property absent",
			field:    "nonexistent",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, exists := schema.Properties[tt.field]
			assert.Equal(t, tt.expected, exists)
		})
	}

	t.Run("channel_id is string type", func(t *testing.T) {
		channelIDProp, exists := schema.Properties["channel_id"]
		require.True(t, exists)
		assert.Equal(t, "string", channelIDProp.Type)
	})

	t.Run("schema has exactly four properties", func(t *testing.T) {
		assert.Len(t, schema.Properties, 4)
	})
}

func TestSearchPostsChannelIDValidation(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected bool
	}{
		{
			name:     "valid ID from NewId",
			id:       model.NewId(),
			expected: true,
		},
		{
			name:     "valid 26 char lowercase alphanumeric",
			id:       "abcdefghijklmnopqrstuvwxyz",
			expected: true,
		},
		{
			name:     "empty string is invalid",
			id:       "",
			expected: false,
		},
		{
			name:     "too short ID",
			id:       "abc",
			expected: false,
		},
		{
			name:     "25 char string is invalid",
			id:       "abcdefghijklmnopqrstuvwxy",
			expected: false,
		},
		{
			name:     "27 char string is invalid",
			id:       "abcdefghijklmnopqrstuvwxyza",
			expected: false,
		},
		{
			name:     "special characters are invalid",
			id:       "abcdefghijklmnopqrstuvwxy!",
			expected: false,
		},
		{
			name:     "spaces are invalid",
			id:       "abcdefghijklmnopqrst uvwxy",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := model.IsValidId(tt.id)
			assert.Equal(t, tt.expected, result,
				"model.IsValidId(%q) = %v, want %v", tt.id, result, tt.expected)
		})
	}
}
