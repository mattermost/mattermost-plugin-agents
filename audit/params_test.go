// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncateID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short values pass through unchanged",
			input:    "abcdefghijklmnopqrstuvwxyz",
			expected: "abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:     "value at the limit passes through unchanged",
			input:    strings.Repeat("a", 128),
			expected: strings.Repeat("a", 128),
		},
		{
			name:     "oversized values are clamped with a marker",
			input:    strings.Repeat("a", 129),
			expected: strings.Repeat("a", 128) + "…(truncated)",
		},
		{
			name:     "empty string passes through",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TruncateID(tt.input))
		})
	}
}

func TestTruncateDescription(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "typical error chains pass through unchanged",
			input:    "failed to save config: kv exploded",
			expected: "failed to save config: kv exploded",
		},
		{
			name:     "oversized descriptions are clamped with a marker",
			input:    strings.Repeat("x", 501),
			expected: strings.Repeat("x", 500) + "…(truncated)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TruncateDescription(tt.input))
		})
	}
}
