// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalWebSearchContext(t *testing.T) {
	cases := []struct {
		name          string
		json          string
		wantNil       bool
		wantResetKeys bool
	}{
		{
			// A user (or buggy client) can set the web_search_context post prop
			// to the literal JSON "null". json.Unmarshal succeeds and leaves the
			// target map nil, so the unconditional writes that follow used to
			// panic with "assignment to entry in nil map" and crash the plugin.
			name:          "literal null does not panic",
			json:          "null",
			wantResetKeys: true,
		},
		{
			name:          "empty object resets tracking",
			json:          "{}",
			wantResetKeys: true,
		},
		{
			name:          "valid context is parsed and tracking reset",
			json:          `{"mm_web_search_allowed_urls":["https://example.com"]}`,
			wantResetKeys: true,
		},
		{
			name:    "invalid json returns nil",
			json:    "{not-json",
			wantNil: true,
		},
		{
			name:    "non-object json returns nil",
			json:    "123",
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mocks.NewMockClient(t)
			mmClient.EXPECT().LogDebug(mock.Anything, mock.Anything).Maybe()
			mmClient.EXPECT().LogError(mock.Anything, mock.Anything).Maybe()

			c := &Conversations{mmClient: mmClient}

			params := c.unmarshalWebSearchContext(tc.json, "post-id")

			if tc.wantNil {
				require.Nil(t, params)
				return
			}

			require.NotNil(t, params)
			if tc.wantResetKeys {
				require.Equal(t, 0, params[mmtools.WebSearchCountKey])
				require.Equal(t, []string{}, params[mmtools.WebSearchExecutedQueriesKey])
			}
		})
	}
}
