// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package genui

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The webapp splitter runs the same cases (split_message.test.ts), so block
// indexes cannot drift between the client that sends them and this check.
func TestClosedBlocks(t *testing.T) {
	raw, err := os.ReadFile("testdata/split_cases.json")
	require.NoError(t, err)
	var cases []struct {
		Name    string   `json:"name"`
		Message string   `json:"message"`
		Blocks  []string `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			blocks := ClosedBlocks(tc.Message)
			if len(tc.Blocks) == 0 {
				assert.Empty(t, blocks)
				return
			}
			assert.Equal(t, tc.Blocks, blocks)
		})
	}
}
