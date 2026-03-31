// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "testing"

func TestUTF16CodeUnitCount(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "ascii",
			input:    "A B",
			expected: 3,
		},
		{
			name:     "bmp unicode",
			input:    "A 你好 B",
			expected: 6,
		},
		{
			name:     "single emoji",
			input:    "A 🎉 B",
			expected: 6,
		},
		{
			name:     "emoji zwj sequence",
			input:    "A 👨‍👩‍👧‍👦 B",
			expected: 15,
		},
		{
			name:     "bullet markdown",
			input:    "• **React**",
			expected: 11,
		},
		{
			name:     "multiple emoji",
			input:    "🙂🙂text",
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UTF16CodeUnitCount(tt.input); got != tt.expected {
				t.Fatalf("UTF16CodeUnitCount(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
