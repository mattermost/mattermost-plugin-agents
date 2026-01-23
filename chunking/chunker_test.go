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
}
