// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestValidateServiceIDUniqueness(t *testing.T) {
	tests := []struct {
		name      string
		services  []llm.ServiceConfig
		expectErr bool
	}{
		{
			name: "unique IDs",
			services: []llm.ServiceConfig{
				{ID: "a", Name: "svc", Type: "openai"},
				{ID: "b", Name: "other", Type: "anthropic"},
			},
		},
		{
			name: "empty IDs ignored",
			services: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
				{Name: "other", Type: "anthropic"},
			},
		},
		{
			name: "duplicate non-empty IDs error",
			services: []llm.ServiceConfig{
				{ID: "dup", Name: "svc", Type: "openai"},
				{ID: "dup", Name: "copy", Type: "openai"},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceIDUniqueness(tt.services)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrServiceIDConflict)
				return
			}
			require.NoError(t, err)
		})
	}
}
