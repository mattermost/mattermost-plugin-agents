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

func TestSearchPostsArgsChannelIDs(t *testing.T) {
	tests := []struct {
		name               string
		jsonInput          string
		expectedQuery      string
		expectedChannelIDs []string
		expectedTeamID     string
		expectedLimit      int
	}{
		{
			name:               "single channel ID",
			jsonInput:          `{"query": "bug fix", "channel_ids": ["abcdefghijklmnopqrstuvwxyz"]}`,
			expectedQuery:      "bug fix",
			expectedChannelIDs: []string{"abcdefghijklmnopqrstuvwxyz"},
			expectedLimit:      0,
		},
		{
			name:               "multiple channel IDs",
			jsonInput:          `{"query": "deployment", "channel_ids": ["abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba"]}`,
			expectedQuery:      "deployment",
			expectedChannelIDs: []string{"abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba"},
			expectedLimit:      0,
		},
		{
			name:               "empty channel IDs array",
			jsonInput:          `{"query": "test", "channel_ids": []}`,
			expectedQuery:      "test",
			expectedChannelIDs: []string{},
			expectedLimit:      0,
		},
		{
			name:               "no channel IDs field",
			jsonInput:          `{"query": "search term"}`,
			expectedQuery:      "search term",
			expectedChannelIDs: nil,
			expectedLimit:      0,
		},
		{
			name:               "channel IDs with team ID and limit",
			jsonInput:          `{"query": "release notes", "team_id": "abcdefghijklmnopqrstuvwxyz", "channel_ids": ["zyxwvutsrqponmlkjihgfedcba"], "limit": 50}`,
			expectedQuery:      "release notes",
			expectedChannelIDs: []string{"zyxwvutsrqponmlkjihgfedcba"},
			expectedTeamID:     "abcdefghijklmnopqrstuvwxyz",
			expectedLimit:      50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args SearchPostsArgs
			err := json.Unmarshal([]byte(tt.jsonInput), &args)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedQuery, args.Query)
			assert.Equal(t, tt.expectedChannelIDs, args.ChannelIDs)
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
			name:     "channel_ids property exists",
			field:    "channel_ids",
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

	t.Run("channel_ids is array type", func(t *testing.T) {
		channelIDsProp, exists := schema.Properties["channel_ids"]
		require.True(t, exists)
		assert.Equal(t, "array", channelIDsProp.Type)
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
