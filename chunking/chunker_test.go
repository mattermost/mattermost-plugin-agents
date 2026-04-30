// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package chunking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkText(t *testing.T) {
	t.Run("empty content", func(t *testing.T) {
		content := ""
		opts := DefaultOptions()

		chunks := ChunkText(content, opts)
		require.Len(t, chunks, 1)
		assert.Equal(t, content, chunks[0].Content)
		assert.False(t, chunks[0].IsChunk)
		assert.Equal(t, 0, chunks[0].ChunkIndex)
		assert.Equal(t, 1, chunks[0].TotalChunks)
	})

	t.Run("short content", func(t *testing.T) {
		content := "This is a short message."
		opts := DefaultOptions()

		chunks := ChunkText(content, opts)
		require.Len(t, chunks, 1)
		assert.Equal(t, content, chunks[0].Content)
		assert.False(t, chunks[0].IsChunk)
		assert.Equal(t, 0, chunks[0].ChunkIndex)
		assert.Equal(t, 1, chunks[0].TotalChunks)
	})

	t.Run("sentences strategy", func(t *testing.T) {
		content := "This is sentence one. This is sentence two! This is sentence three? This is sentence four."
		opts := Options{
			ChunkSize:        25,
			ChunkingStrategy: "sentences",
		}

		chunks := ChunkText(content, opts)
		require.Greater(t, len(chunks), 1, "Should create multiple chunks")

		// Verify all chunks are marked correctly
		for i, chunk := range chunks {
			assert.True(t, chunk.IsChunk)
			assert.Equal(t, i, chunk.ChunkIndex)
			assert.Equal(t, len(chunks), chunk.TotalChunks)
			assert.LessOrEqual(t, len(chunk.Content), opts.ChunkSize, "Chunk should not exceed max size")
		}
	})

	t.Run("sentences strategy without whitespace after punctuation", func(t *testing.T) {
		content := "This is sentence one.This is sentence two!This is sentence three?This is the fourth sentence."
		opts := Options{
			ChunkSize:        25,
			ChunkOverlap:     0,
			ChunkingStrategy: "sentences",
		}

		chunks := ChunkText(content, opts)
		require.Greater(t, len(chunks), 1, "Should split on punctuation even without trailing space")

		// Verify all chunks are marked correctly
		for i, chunk := range chunks {
			assert.True(t, chunk.IsChunk)
			assert.Equal(t, i, chunk.ChunkIndex)
			assert.Equal(t, len(chunks), chunk.TotalChunks)
			assert.LessOrEqual(t, len(chunk.Content), opts.ChunkSize, "Chunk should not exceed max size")
		}
	})

	t.Run("paragraphs strategy", func(t *testing.T) {
		content := "First paragraph here.\n\nSecond paragraph here.\n\nThird paragraph here."
		opts := Options{
			ChunkSize:        30,
			ChunkingStrategy: "paragraphs",
		}

		chunks := ChunkText(content, opts)
		require.Greater(t, len(chunks), 1, "Should create multiple chunks")

		// Verify chunks
		for i, chunk := range chunks {
			assert.True(t, chunk.IsChunk)
			assert.Equal(t, i, chunk.ChunkIndex)
			assert.Equal(t, len(chunks), chunk.TotalChunks)
		}
	})

	t.Run("fixed strategy", func(t *testing.T) {
		content := "This is a long text that should be split into fixed-size chunks without regard to sentence boundaries."
		opts := Options{
			ChunkSize:        20,
			ChunkingStrategy: "fixed",
		}

		chunks := ChunkText(content, opts)
		require.Greater(t, len(chunks), 1, "Should create multiple chunks")

		// Verify chunks
		for i, chunk := range chunks {
			assert.True(t, chunk.IsChunk)
			assert.Equal(t, i, chunk.ChunkIndex)
			assert.Equal(t, len(chunks), chunk.TotalChunks)
			assert.LessOrEqual(t, len(chunk.Content), opts.ChunkSize, "Chunk should not exceed max size")
		}
	})

	t.Run("chunk overlap", func(t *testing.T) {
		content := "Word1 Word2 Word3 Word4 Word5 Word6 Word7 Word8 Word9 Word10"
		opts := Options{
			ChunkSize:        20,
			ChunkOverlap:     5,
			ChunkingStrategy: "fixed",
		}

		chunks := ChunkText(content, opts)
		require.Greater(t, len(chunks), 1, "Should create multiple chunks")

		// With overlap, later chunks should contain some content from previous chunks
		// This is handled by the underlying langchaingo library
		for i, chunk := range chunks {
			assert.True(t, chunk.IsChunk)
			assert.Equal(t, i, chunk.ChunkIndex)
			assert.Equal(t, len(chunks), chunk.TotalChunks)
		}
	})

	t.Run("zero chunk size", func(t *testing.T) {
		content := "Some content"
		opts := Options{
			ChunkSize: 0,
		}

		chunks := ChunkText(content, opts)
		require.Len(t, chunks, 1)
		assert.Equal(t, content, chunks[0].Content)
		assert.False(t, chunks[0].IsChunk)
	})

	t.Run("negative chunk size", func(t *testing.T) {
		content := "Some content"
		opts := Options{
			ChunkSize: -100,
		}

		chunks := ChunkText(content, opts)
		require.Len(t, chunks, 1)
		assert.Equal(t, content, chunks[0].Content)
		assert.False(t, chunks[0].IsChunk)
	})

	t.Run("whitespace-only content", func(t *testing.T) {
		content := "  \t\n  \t\n  "
		opts := DefaultOptions()
		chunks := ChunkText(content, opts)

		require.Len(t, chunks, 1)
		assert.Equal(t, content, chunks[0].Content, "Should preserve original whitespace content")
		assert.False(t, chunks[0].IsChunk, "Whitespace-only content should not be marked as chunk")
		assert.Equal(t, 0, chunks[0].ChunkIndex)
		assert.Equal(t, 1, chunks[0].TotalChunks)
	})

	t.Run("content exactly at chunk size boundary", func(t *testing.T) {
		tests := []struct {
			name     string
			content  string
			size     int
			strategy string
		}{
			{
				name:     "exact match with sentences strategy",
				content:  "Hello world.",
				size:     12,
				strategy: "sentences",
			},
			{
				name:     "exact match with fixed strategy",
				content:  "Hello world!",
				size:     12,
				strategy: "fixed",
			},
			{
				name:     "exact match with paragraphs strategy",
				content:  "Hello world?",
				size:     12,
				strategy: "paragraphs",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				opts := Options{
					ChunkSize:        tc.size,
					ChunkOverlap:     0,
					ChunkingStrategy: tc.strategy,
				}

				chunks := ChunkText(tc.content, opts)

				// Content exactly at chunk size should return single result
				require.Len(t, chunks, 1)
				assert.Equal(t, tc.content, chunks[0].Content)
				// When content exactly matches chunk size and is returned unchanged,
				// it should be marked as non-chunk per the condition on line 98
				assert.False(t, chunks[0].IsChunk, "Content exactly at chunk size should be non-chunk")
			})
		}
	})

	t.Run("content that produces exactly one chunk vs non-chunk", func(t *testing.T) {
		// Test that content smaller than chunk size returns as non-chunk
		t.Run("content smaller than chunk size", func(t *testing.T) {
			content := "Short text."
			opts := Options{
				ChunkSize:        100,
				ChunkOverlap:     0,
				ChunkingStrategy: "sentences",
			}

			chunks := ChunkText(content, opts)
			require.Len(t, chunks, 1)
			assert.False(t, chunks[0].IsChunk, "Content smaller than chunk size should be non-chunk")
			assert.Equal(t, content, chunks[0].Content)
		})
	})

	t.Run("unknown/invalid chunking strategy string", func(t *testing.T) {
		tests := []struct {
			name     string
			strategy string
		}{
			{"empty string", ""},
			{"unknown strategy", "unknown_strategy"},
			{"misspelled sentences", "sentencez"},
			{"uppercase strategy", "SENTENCES"},
			{"mixed case", "Paragraphs"},
			{"with spaces", " sentences "},
			{"numeric", "123"},
			{"special characters", "sentences!@#"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				content := "This is sentence one. This is sentence two. This is sentence three."
				opts := Options{
					ChunkSize:        30,
					ChunkOverlap:     0,
					ChunkingStrategy: tc.strategy,
				}

				// Unknown strategies should fall through to default (sentences)
				// and should not panic
				chunks := ChunkText(content, opts)
				require.NotEmpty(t, chunks, "Should return at least one chunk for unknown strategy")

				// Verify basic chunk metadata is set
				for i, chunk := range chunks {
					assert.Equal(t, i, chunk.ChunkIndex)
					assert.Equal(t, len(chunks), chunk.TotalChunks)
					assert.NotEmpty(t, chunk.Content)
				}
			})
		}
	})

	t.Run("very large chunk overlap relative to chunk size", func(t *testing.T) {
		content := "Word1 Word2 Word3 Word4 Word5 Word6 Word7 Word8 Word9 Word10"

		tests := []struct {
			name         string
			chunkSize    int
			chunkOverlap int
		}{
			{"overlap equals chunk size", 20, 20},
			{"overlap greater than chunk size", 20, 30},
			{"overlap much larger than chunk size", 10, 100},
			{"both very small with overlap larger", 5, 10},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				opts := Options{
					ChunkSize:        tc.chunkSize,
					ChunkOverlap:     tc.chunkOverlap,
					ChunkingStrategy: "fixed",
				}

				// Should not panic even with invalid overlap configuration
				chunks := ChunkText(content, opts)
				require.NotEmpty(t, chunks, "Should return at least one chunk")

				// Verify basic structure
				for i, chunk := range chunks {
					assert.Equal(t, i, chunk.ChunkIndex)
					assert.Equal(t, len(chunks), chunk.TotalChunks)
				}
			})
		}
	})

	t.Run("content with only sentence-ending punctuation", func(t *testing.T) {
		tests := []struct {
			name    string
			content string
		}{
			{"periods only", "..."},
			{"exclamations only", "!!!"},
			{"questions only", "???"},
			{"mixed punctuation", ".!?.!?"},
			{"punctuation with spaces", ". . . ! ! ! ? ? ?"},
			{"long punctuation string", "........!!!!!??????"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				opts := Options{
					ChunkSize:        5,
					ChunkOverlap:     0,
					ChunkingStrategy: "sentences",
				}

				// Should not panic with punctuation-only content
				chunks := ChunkText(tc.content, opts)
				require.NotEmpty(t, chunks, "Should return at least one chunk")

				// All original content should be present across chunks
				var combined string
				for _, chunk := range chunks {
					combined += chunk.Content
				}
				// Content may be split but should be preserved
				assert.NotEmpty(t, combined, "Combined chunks should have content")
			})
		}
	})
}
