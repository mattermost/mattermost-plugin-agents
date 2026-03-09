// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientUsesPluginTransport(t *testing.T) {
	client := NewClient(&fakePluginAPI{})
	require.NotNil(t, client)

	transport, ok := client.httpClient.Transport.(*pluginAPIRoundTripper)
	require.True(t, ok)
	require.NotNil(t, transport.api)
}

func TestNewClientFromAppUsesAppTransportAndUserID(t *testing.T) {
	client := NewClientFromApp(&fakeAppAPI{}, "abcdefghijklmnopqrstuvwxyz")
	require.NotNil(t, client)

	transport, ok := client.httpClient.Transport.(*appAPIRoundTripper)
	require.True(t, ok)
	require.NotNil(t, transport.api)
	require.Equal(t, "abcdefghijklmnopqrstuvwxyz", transport.userID)
}

func TestParamConstraintUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedValues    []string
		expectedBindings  int
		expectedBindTool  string
		expectedBindField string
	}{
		{
			name:           "shorthand string array",
			input:          `["val1", "val2"]`,
			expectedValues: []string{"val1", "val2"},
		},
		{
			name:           "full struct with allowed_values only",
			input:          `{"allowed_values": ["a", "b"]}`,
			expectedValues: []string{"a", "b"},
		},
		{
			name:              "full struct with from_tool_output only",
			input:             `{"from_tool_output": [{"tool": "create_channel", "field": "channel_id"}]}`,
			expectedBindings:  1,
			expectedBindTool:  "create_channel",
			expectedBindField: "channel_id",
		},
		{
			name:              "full struct with both",
			input:             `{"allowed_values": ["existing"], "from_tool_output": [{"tool": "src", "field": "id"}]}`,
			expectedValues:    []string{"existing"},
			expectedBindings:  1,
			expectedBindTool:  "src",
			expectedBindField: "id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pc ParamConstraint
			err := json.Unmarshal([]byte(tt.input), &pc)
			require.NoError(t, err)

			if tt.expectedValues != nil {
				assert.Equal(t, tt.expectedValues, pc.AllowedValues)
			}
			assert.Len(t, pc.FromToolOutput, tt.expectedBindings)
			if tt.expectedBindings > 0 {
				assert.Equal(t, tt.expectedBindTool, pc.FromToolOutput[0].Tool)
				assert.Equal(t, tt.expectedBindField, pc.FromToolOutput[0].Field)
			}
		})
	}
}

func TestToolConstraintsUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "shorthand form (backwards compatible)",
			input: `{"search_posts": {"channel_ids": ["chan1", "chan2"], "team_id": ["team1"]}}`,
		},
		{
			name:  "dynamic form",
			input: `{"create_post": {"channel_id": {"allowed_values": ["existing_abc"], "from_tool_output": [{"tool": "create_channel", "field": "channel_id"}]}}}`,
		},
		{
			name:  "dynamic-only form",
			input: `{"create_post": {"channel_id": {"from_tool_output": [{"tool": "create_channel", "field": "channel_id"}]}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tc ToolConstraints
			err := json.Unmarshal([]byte(tt.input), &tc)
			require.NoError(t, err)
			require.NotEmpty(t, tc)
		})
	}
}
