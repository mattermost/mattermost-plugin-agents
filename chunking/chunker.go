// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package chunking

import (
	"strings"
	"unicode/utf8"
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
		Content:     content,
		IsChunk:     false,
		ChunkIndex:  0,
		TotalChunks: 1,
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

	if utf8.RuneCountInString(content) <= opts.ChunkSize {
		return unchunked(content)
	}

	textChunks := splitTextRecursive(content, separators, opts.ChunkSize, opts.ChunkOverlap)
	if len(textChunks) == 0 || (len(textChunks) == 1 && textChunks[0] == content) {
		return unchunked(content)
	}

	// Create chunks with metadata
	result := make([]Chunk, len(textChunks))
	for i, chunk := range textChunks {
		result[i] = Chunk{
			Content:     chunk,
			IsChunk:     true,
			ChunkIndex:  i,
			TotalChunks: len(textChunks),
		}
	}

	return result
}

func splitTextRecursive(text string, separators []string, chunkSize, chunkOverlap int) []string {
	sep := ""
	var nextSeparators []string
	for i, candidate := range separators {
		if candidate == "" || strings.Contains(text, candidate) {
			sep = candidate
			nextSeparators = separators[i+1:]
			break
		}
	}

	splits := strings.Split(text, sep)

	var finalChunks []string
	var goodSplits []string
	for _, s := range splits {
		if utf8.RuneCountInString(s) < chunkSize {
			goodSplits = append(goodSplits, s)
			continue
		}
		if len(goodSplits) > 0 {
			finalChunks = append(finalChunks, mergeSplits(goodSplits, sep, chunkSize, chunkOverlap)...)
			goodSplits = goodSplits[:0]
		}
		if len(nextSeparators) == 0 {
			finalChunks = append(finalChunks, s)
		} else {
			finalChunks = append(finalChunks, splitTextRecursive(s, nextSeparators, chunkSize, chunkOverlap)...)
		}
	}
	if len(goodSplits) > 0 {
		finalChunks = append(finalChunks, mergeSplits(goodSplits, sep, chunkSize, chunkOverlap)...)
	}
	return finalChunks
}

func mergeSplits(splits []string, sep string, chunkSize, chunkOverlap int) []string {
	effectiveOverlap := min(max(chunkOverlap, 0), max(chunkSize-1, 0))
	sepLen := utf8.RuneCountInString(sep)

	var docs []string
	var current []string
	total := 0

	for _, piece := range splits {
		pieceLen := utf8.RuneCountInString(piece)
		addedLen := pieceLen
		if len(current) > 0 {
			addedLen += sepLen
		}

		if len(current) > 0 && total+addedLen > chunkSize {
			if doc := strings.TrimSpace(strings.Join(current, sep)); doc != "" {
				docs = append(docs, doc)
			}
			for len(current) > 0 && (total > effectiveOverlap || (total+pieceLen+sepLen > chunkSize && total > 0)) {
				removedLen := utf8.RuneCountInString(current[0])
				current = current[1:]
				total -= removedLen
				if len(current) > 0 {
					total -= sepLen
				}
			}
			addedLen = pieceLen
			if len(current) > 0 {
				addedLen += sepLen
			}
		}

		current = append(current, piece)
		total += addedLen
	}

	if doc := strings.TrimSpace(strings.Join(current, sep)); doc != "" {
		docs = append(docs, doc)
	}
	return docs
}
