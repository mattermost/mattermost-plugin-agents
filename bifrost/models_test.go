// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertBifrostModels(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	strPtr := func(s string) *string { return &s }

	input := []schemas.Model{
		{
			ID:              "anthropic/claude-sonnet-4-5",
			Name:            strPtr("Claude Sonnet 4.5"),
			MaxInputTokens:  intPtr(200000),
			MaxOutputTokens: intPtr(8192),
			ContextLength:   intPtr(200000),
		},
		{
			// Cohere / Mistral / Groq publish ContextLength only; the converter
			// must use it as the InputTokenLimit so the UI can auto-fill.
			ID:            "cohere/command-r",
			ContextLength: intPtr(128000),
		},
		{
			// Provider gave us nothing — pointers stay nil.
			ID: "custom-model",
		},
	}

	got := convertBifrostModels(input)
	require.Len(t, got, 3)

	assert.Equal(t, "claude-sonnet-4-5", got[0].ID)
	assert.Equal(t, "Claude Sonnet 4.5", got[0].DisplayName)
	require.NotNil(t, got[0].InputTokenLimit)
	assert.Equal(t, 200000, *got[0].InputTokenLimit)
	require.NotNil(t, got[0].OutputTokenLimit)
	assert.Equal(t, 8192, *got[0].OutputTokenLimit)
	require.NotNil(t, got[0].ContextLength)
	assert.Equal(t, 200000, *got[0].ContextLength)

	assert.Equal(t, "command-r", got[1].ID)
	assert.Equal(t, "command-r", got[1].DisplayName)
	require.NotNil(t, got[1].InputTokenLimit, "InputTokenLimit must fall back to ContextLength")
	assert.Equal(t, 128000, *got[1].InputTokenLimit)
	assert.Nil(t, got[1].OutputTokenLimit, "MaxOutputTokens not provided → nil")
	require.NotNil(t, got[1].ContextLength)
	assert.Equal(t, 128000, *got[1].ContextLength)

	assert.Equal(t, "custom-model", got[2].ID)
	assert.Nil(t, got[2].InputTokenLimit)
	assert.Nil(t, got[2].OutputTokenLimit)
	assert.Nil(t, got[2].ContextLength)
}
