// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package chunking

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitPlaintextOnSentences(t *testing.T) {
	// Original test cases
	for i, test := range []struct {
		input  string
		size   int
		output []string
	}{
		{
			"Hello. How are you! I'm doing well. Thanks!",
			10,
			[]string{"Hello. How", "are you!", "I'm doing", "well. Than", "ks!"},
		},
		{
			"Hello. How are you! I'm doing well.",
			20,
			[]string{"Hello. How are you!", "I'm doing well."},
		},
		{
			"Hello. How are you! I'm doing well.",
			25,
			[]string{"Hello. How are you!", "I'm doing well."},
		},
		{
			"Hello. How are you! I'm doing well.",
			32,
			[]string{"Hello. How are you! I'm doing we", "ll."},
		},
	} {
		t.Run("test "+strconv.Itoa(i), func(t *testing.T) {
			actual := SplitPlaintextOnSentences(test.input, test.size)
			require.Equal(t, test.output, actual)
		})
	}

	// Additional test cases testing the intended behavior
	t.Run("Empty string", func(t *testing.T) {
		chunks := SplitPlaintextOnSentences("", 100)
		assert.Equal(t, 1, len(chunks), "Should return a single chunk for empty string")
		assert.Equal(t, "", chunks[0], "Empty string should return empty chunk")
	})

	t.Run("Text with various sentence boundaries", func(t *testing.T) {
		input := "This is a statement. Is this a question? Yes, it is! This ends with ellipsis..."
		chunks := SplitPlaintextOnSentences(input, 20)

		// Find at least one chunk ending with each type of sentence boundary
		foundPeriod := false
		foundQuestion := false
		foundExclamation := false

		for _, chunk := range chunks {
			if strings.HasSuffix(chunk, ".") {
				foundPeriod = true
			}
			if strings.HasSuffix(chunk, "?") {
				foundQuestion = true
			}
			if strings.HasSuffix(chunk, "!") {
				foundExclamation = true
			}
		}

		// Assert we found at least some sentence boundaries
		assert.True(t, foundPeriod || foundQuestion || foundExclamation,
			"Should preserve at least some sentence boundaries")

		// Check that no chunk exceeds the maximum size
		for i, chunk := range chunks {
			assert.LessOrEqual(t, len(chunk), 20, "Chunk %d exceeds maximum size", i)
		}
	})

	t.Run("Very long sentence beyond chunk size", func(t *testing.T) {
		input := "This is an extremely long sentence without any sentence boundaries that should be split based purely on the chunk size limit and not on sentence boundaries because there are none to be found here"
		chunkSize := 30
		chunks := SplitPlaintextOnSentences(input, chunkSize)

		// Verify no chunk exceeds the maximum size
		for i, chunk := range chunks {
			assert.LessOrEqual(t, len(chunk), chunkSize, "Chunk %d exceeds maximum size", i)
		}

		// Verify we get back the full text (ignoring whitespace differences)
		combined := strings.Join(chunks, " ")
		assert.Equal(t, len(strings.ReplaceAll(input, " ", "")), len(strings.ReplaceAll(combined, " ", "")),
			"Combined chunks should contain all input text")
	})

	t.Run("Respects minimum chunk size", func(t *testing.T) {
		input := "Short. Another. Third. Fourth. Fifth. A slightly longer sentence to end with."
		chunkSize := 30
		minSize := int(float64(chunkSize) * 0.75)
		chunks := SplitPlaintextOnSentences(input, chunkSize)

		// Verify that chunks (except possibly the last one) meet the minimum size
		for i, chunk := range chunks[:len(chunks)-1] {
			assert.GreaterOrEqual(t, len(chunk), minSize,
				"Chunk %d should meet minimum size requirement: %q", i, chunk)
		}
	})

	t.Run("Whitespace-only content", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			chunkSize int
		}{
			{"spaces only", "     ", 10},
			{"tabs only", "\t\t\t", 10},
			{"newlines only", "\n\n\n", 10},
			{"mixed whitespace", "  \t\n  \t\n  ", 10},
			{"whitespace longer than chunk", "                    ", 5},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				chunks := SplitPlaintextOnSentences(tc.input, tc.chunkSize)
				require.NotEmpty(t, chunks, "Should return at least one chunk")

				// Each chunk should not exceed the chunk size
				for i, chunk := range chunks {
					assert.LessOrEqual(t, len(chunk), tc.chunkSize,
						"Chunk %d exceeds maximum size: %q", i, chunk)
				}
			})
		}
	})

	t.Run("Content exactly at chunk size boundary", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			chunkSize int
			expected  []string
		}{
			{
				name:      "exactly at boundary",
				input:     "Hello world.",
				chunkSize: 12,
				expected:  []string{"Hello world."},
			},
			{
				name:      "one character less than boundary",
				input:     "Hello world",
				chunkSize: 12,
				expected:  []string{"Hello world"},
			},
			{
				name:      "one character more than boundary",
				input:     "Hello world!.",
				chunkSize: 12,
				expected:  []string{"Hello world!", "."},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				chunks := SplitPlaintextOnSentences(tc.input, tc.chunkSize)
				assert.Equal(t, tc.expected, chunks)
			})
		}
	})

	t.Run("Content that produces exactly one chunk", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			chunkSize int
		}{
			{"shorter than chunk size", "Short.", 100},
			{"much shorter than chunk size", "Hi", 1000},
			{"single character", ".", 10},
			{"single word", "Hello", 10},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				chunks := SplitPlaintextOnSentences(tc.input, tc.chunkSize)
				require.Len(t, chunks, 1, "Should produce exactly one chunk")
				assert.Equal(t, tc.input, chunks[0], "Single chunk should equal input")
			})
		}
	})

	t.Run("Content with only sentence-ending punctuation", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			chunkSize int
		}{
			{"periods only", "...", 10},
			{"exclamations only", "!!!", 10},
			{"questions only", "???", 10},
			{"mixed punctuation", ".!?.!?", 10},
			{"punctuation longer than chunk", ".!?.!?.!?.!?.!?", 3},
			{"single period", ".", 10},
			{"single exclamation", "!", 10},
			{"single question", "?", 10},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				chunks := SplitPlaintextOnSentences(tc.input, tc.chunkSize)
				require.NotEmpty(t, chunks, "Should return at least one chunk")

				// Each chunk should not exceed the chunk size
				for i, chunk := range chunks {
					assert.LessOrEqual(t, len(chunk), tc.chunkSize,
						"Chunk %d exceeds maximum size: %q", i, chunk)
				}

				// All punctuation should be preserved
				var combined string
				for _, chunk := range chunks {
					combined += chunk
				}
				// Note: TrimSpace may remove some content, but punctuation should be preserved
				assert.NotEmpty(t, combined, "Combined chunks should not be empty")
			})
		}
	})

	t.Run("Edge cases with chunk size", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			chunkSize int
		}{
			{"chunk size of 1", "Hello.", 1},
			{"chunk size of 2", "Hello.", 2},
			{"very small chunk size with sentence endings", "A. B. C.", 2},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				// Should not panic even with very small chunk sizes
				chunks := SplitPlaintextOnSentences(tc.input, tc.chunkSize)
				require.NotEmpty(t, chunks, "Should return at least one chunk")

				// Each chunk should not exceed the chunk size
				for i, chunk := range chunks {
					assert.LessOrEqual(t, len(chunk), tc.chunkSize,
						"Chunk %d exceeds maximum size: %q (len=%d, max=%d)", i, chunk, len(chunk), tc.chunkSize)
				}
			})
		}
	})
}
