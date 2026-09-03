// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package chunking

import (
	"strings"

	"github.com/tmc/langchaingo/textsplitter"
)

// ChunkInfo contains metadata about a chunk's position within a document
type ChunkInfo struct {
	IsChunk     bool
	ChunkIndex  int
	TotalChunks int
}

// Chunk represents a piece of text with its chunk metadata
type Chunk struct {
	Content string
	ChunkInfo
}

// Options defines options for chunking documents
type Options struct {
	ChunkSize        int    `json:"chunkSize"`        // Maximum size of each chunk in characters
	ChunkOverlap     int    `json:"chunkOverlap"`     // Number of characters to overlap between chunks
	ChunkingStrategy string `json:"chunkingStrategy"` // Strategy: sentences, paragraphs, or fixed
}

// DefaultOptions returns the default chunking options
func DefaultOptions() Options {
	return Options{
		ChunkSize:        1000,
		ChunkOverlap:     200,
		ChunkingStrategy: "sentences",
	}
}

// unchunked returns the content as a single non-chunk.
func unchunked(content string) []Chunk {
	return []Chunk{{
		Content: content,
		ChunkInfo: ChunkInfo{
			IsChunk:     false,
			ChunkIndex:  0,
			TotalChunks: 1,
		},
	}}
}

// strategySeparators maps a chunking strategy to the separators passed to the
// recursive-character splitter; unknown strategies fall back to sentences.
var strategySeparators = map[string][]string{
	"paragraphs": {"\n\n", "\n", " ", ""},
	"fixed":      {" ", ""},
	"sentences":  {".", "!", "?", "\n", " ", ""},
}

// ChunkText splits text into chunks based on the provided options
func ChunkText(content string, opts Options) []Chunk {
	// If content is empty, return a single non-chunk
	if strings.TrimSpace(content) == "" {
		return unchunked(content)
	}

	// If chunk size is zero or negative, return the original as non-chunk
	if opts.ChunkSize <= 0 {
		return unchunked(content)
	}

	separators, ok := strategySeparators[opts.ChunkingStrategy]
	if !ok {
		separators = strategySeparators["sentences"]
	}
	splitter := textsplitter.NewRecursiveCharacter(
		textsplitter.WithChunkSize(opts.ChunkSize),
		textsplitter.WithChunkOverlap(opts.ChunkOverlap),
		textsplitter.WithSeparators(separators),
	)
	textChunks, err := splitter.SplitText(content)
	if err != nil || (len(textChunks) == 1 && textChunks[0] == content) {
		return unchunked(content)
	}

	// Create chunks with metadata
	result := make([]Chunk, len(textChunks))
	for i, chunk := range textChunks {
		result[i] = Chunk{
			Content: chunk,
			ChunkInfo: ChunkInfo{
				IsChunk:     true,
				ChunkIndex:  i,
				TotalChunks: len(textChunks),
			},
		}
	}

	return result
}
