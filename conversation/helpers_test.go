// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalBlocks(t *testing.T) {
	tests := []struct {
		name           string
		raw            json.RawMessage
		expectedBlocks []ContentBlock
		expectErr      bool
	}{
		{
			name:           "nil RawMessage returns nil",
			raw:            nil,
			expectedBlocks: nil,
			expectErr:      false,
		},
		{
			name:           "empty RawMessage returns nil",
			raw:            json.RawMessage{},
			expectedBlocks: nil,
			expectErr:      false,
		},
		{
			name:           "empty JSON array returns empty slice",
			raw:            json.RawMessage(`[]`),
			expectedBlocks: []ContentBlock{},
			expectErr:      false,
		},
		{
			name: "valid blocks JSON",
			raw:  json.RawMessage(`[{"type":"text","text":"hello"}]`),
			expectedBlocks: []ContentBlock{
				{Type: BlockTypeText, Text: "hello"},
			},
			expectErr: false,
		},
		{
			name:      "invalid JSON returns error",
			raw:       json.RawMessage(`{not json`),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, err := unmarshalBlocks(tt.raw)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedBlocks, blocks)
		})
	}
}
