// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalWebSearchContext(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
	}{
		{
			// A user-controlled post prop can carry the literal JSON string
			// "null", which unmarshals into a nil map without error. The
			// function must still return a usable, non-nil map rather than
			// panicking on the tracking-key writes.
			name:    "null literal returns usable map without panicking",
			input:   "null",
			wantNil: false,
		},
		{
			name:    "valid object returns map with reset tracking keys",
			input:   `{"some_existing_key":"some_value"}`,
			wantNil: false,
		},
		{
			name:    "malformed json returns nil",
			input:   `{not valid json`,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()

			c := &Conversations{mmClient: mmClient}

			var result map[string]interface{}
			require.NotPanics(t, func() {
				result = c.unmarshalWebSearchContext(tc.input, "post-id")
			}, "unmarshalWebSearchContext must not panic on user-controlled input")

			if tc.wantNil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result, "expected a usable, non-nil map")

			count, hasCount := result[mmtools.WebSearchCountKey]
			require.True(t, hasCount, "expected reset search count key to be present")
			assert.Equal(t, 0, count)

			queries, hasQueries := result[mmtools.WebSearchExecutedQueriesKey]
			require.True(t, hasQueries, "expected reset executed queries key to be present")
			assert.Equal(t, []string{}, queries)
		})
	}
}
