// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripMarkdownCodeFencing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no fencing",
			input:    `{"highlights": ["point 1"], "action_items": []}`,
			expected: `{"highlights": ["point 1"], "action_items": []}`,
		},
		{
			name:     "json fencing multiline",
			input:    "```json\n{\"highlights\": [\"point 1\"], \"action_items\": []}\n```",
			expected: `{"highlights": ["point 1"], "action_items": []}`,
		},
		{
			name:     "plain fencing multiline",
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "fencing with surrounding whitespace",
			input:    "  ```json\n{\"key\": \"value\"}\n```  ",
			expected: `{"key": "value"}`,
		},
		{
			name:     "no closing fence",
			input:    "```json\n{\"key\": \"value\"}",
			expected: `{"key": "value"}`,
		},
		{
			name:     "single-line json fenced",
			input:    "```json {\"a\":1}```",
			expected: `{"a":1}`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "just backticks",
			input:    "```\n```",
			expected: "",
		},
		{
			name:     "regular text not fenced",
			input:    "This is a normal response with ``` in the middle",
			expected: "This is a normal response with ``` in the middle",
		},
		{
			name:     "multiline json with extra whitespace",
			input:    "```json\n{\n  \"highlights\": [\n    \"point 1\"\n  ]\n}\n```",
			expected: "{\n  \"highlights\": [\n    \"point 1\"\n  ]\n}",
		},
		{
			name:     "single-line javascript fenced",
			input:    "```javascript {\"a\":1}```",
			expected: `{"a":1}`,
		},
		{
			name:     "single-line with unknown language tag",
			input:    "```xml <root/>```",
			expected: `<root/>`,
		},
		{
			name:     "multiline with non-json language tag",
			input:    "```typescript\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripMarkdownCodeFencing(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
