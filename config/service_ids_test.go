// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestReconcileServiceIDs(t *testing.T) {
	tests := []struct {
		name      string
		next      []llm.ServiceConfig
		prev      []llm.ServiceConfig
		expectIDs []string
		expectErr bool
	}{
		{
			name: "round-trip payload keeps existing ID",
			next: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "ID-less payload reclaims existing service by name",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "ID-less reclaim survives unrelated field edits",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "anthropic", APIURL: "https://new.example.com"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{"prev-id"},
		},
		{
			name: "genuinely new service stays ID-less",
			next: []llm.ServiceConfig{
				{Name: "brand-new", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{""},
		},
		{
			name: "explicit ID beats a name claim for the same prev entry",
			next: []llm.ServiceConfig{
				{ID: "prev-id", Name: "renamed", Type: "openai"},
				{Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{"prev-id", ""},
		},
		{
			name: "unknown ID colliding with a stored service name errors",
			next: []llm.ServiceConfig{
				{ID: "made-up", Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectErr: true,
		},
		{
			name: "unknown ID with a unique name is kept (API automation)",
			next: []llm.ServiceConfig{
				{ID: "seeded-by-automation", Name: "new-svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectIDs: []string{"seeded-by-automation"},
		},
		{
			name: "caller-chosen IDs on first write are kept",
			next: []llm.ServiceConfig{
				{ID: "seeded-by-automation", Name: "svc", Type: "openai"},
			},
			prev:      nil,
			expectIDs: []string{"seeded-by-automation"},
		},
		{
			name: "duplicate incoming IDs error",
			next: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
				{ID: "prev-id", Name: "copy", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectErr: true,
		},
		{
			name: "duplicate stored IDs error",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "dup", Name: "svc", Type: "openai"},
				{ID: "dup", Name: "other", Type: "openai"},
			},
			expectErr: true,
		},
		{
			name: "ambiguous name match errors",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-a", Name: "svc", Type: "openai"},
				{ID: "prev-b", Name: "svc", Type: "anthropic"},
			},
			expectErr: true,
		},
		{
			name: "two ID-less entries claiming one prev service error",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
				{Name: "svc", Type: "anthropic"},
			},
			prev: []llm.ServiceConfig{
				{ID: "prev-id", Name: "svc", Type: "openai"},
			},
			expectErr: true,
		},
		{
			name: "empty previous list leaves new entries ID-less",
			next: []llm.ServiceConfig{
				{Name: "svc", Type: "openai"},
			},
			prev:      nil,
			expectIDs: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReconcileServiceIDs(tt.next, tt.prev)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrServiceIDConflict)
				return
			}
			require.NoError(t, err)
			ids := make([]string, len(got))
			for i := range got {
				ids[i] = got[i].ID
			}
			require.Equal(t, tt.expectIDs, ids)
		})
	}
}
