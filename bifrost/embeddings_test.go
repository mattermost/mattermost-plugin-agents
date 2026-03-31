// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestEmbeddingDimensions(t *testing.T) {
	tests := []struct {
		name       string
		dimensions int
		expectSet  bool
	}{
		{
			name:       "dimensions > 0 sets Params",
			dimensions: 1536,
			expectSet:  true,
		},
		{
			name:       "dimensions == 0 does not set Params",
			dimensions: 0,
			expectSet:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build params the same way CreateEmbedding does
			var params *schemas.EmbeddingParameters
			if tt.dimensions > 0 {
				params = &schemas.EmbeddingParameters{
					Dimensions: Ptr(tt.dimensions),
				}
			}

			if tt.expectSet {
				assert.NotNil(t, params)
				assert.NotNil(t, params.Dimensions)
				assert.Equal(t, tt.dimensions, *params.Dimensions)
			} else {
				assert.Nil(t, params)
			}
		})
	}
}
