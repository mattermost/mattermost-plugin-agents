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

func TestTruncateIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "small lists pass through with entries clamped",
			input:    []string{"a", strings.Repeat("b", 129)},
			expected: []string{"a", strings.Repeat("b", 128) + "…(truncated)"},
		},
		{
			name: "oversized lists are capped with a marker entry",
			input: func() []string {
				vals := make([]string, 70)
				for i := range vals {
					vals[i] = "id"
				}
				return vals
			}(),
			expected: func() []string {
				vals := make([]string, 64, 65)
				for i := range vals {
					vals[i] = "id"
				}
				return append(vals, "…(truncated)")
			}(),
		},
		{
			name:     "empty list stays empty",
			input:    []string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TruncateIDs(tt.input))
		})
	}
}
