// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"context"
	"hash/fnv"
)

const defaultMockDimensions = 1536

// mockEmbeddingProvider generates deterministic embeddings without calling any upstream service.
type mockEmbeddingProvider struct {
	dimensions int
}

// NewMockEmbeddingProvider creates a new mock embedding provider that produces repeatable vectors.
func NewMockEmbeddingProvider(dimensions int) EmbeddingProvider {
	if dimensions <= 0 {
		dimensions = defaultMockDimensions
	}

	return &mockEmbeddingProvider{
		dimensions: dimensions,
	}
}

func (m *mockEmbeddingProvider) CreateEmbedding(_ context.Context, text string) ([]float32, error) {
	return generateDeterministicEmbedding(text, m.dimensions), nil
}

func (m *mockEmbeddingProvider) BatchCreateEmbeddings(_ context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		embeddings[i] = generateDeterministicEmbedding(text, m.dimensions)
	}

	return embeddings, nil
}

func (m *mockEmbeddingProvider) Dimensions() int {
	return m.dimensions
}

func generateDeterministicEmbedding(text string, dims int) []float32 {
	embedding := make([]float32, dims)
	hasher := fnv.New32a()

	for i := 0; i < dims; i++ {
		_, _ = hasher.Write([]byte(text))
		_, _ = hasher.Write([]byte{byte(i), byte(i >> 8)})
		hash := hasher.Sum32()

		// Map hash into [-1, 1] range for stability
		embedding[i] = (float32(hash%2000) / 1000.0) - 1.0
		hasher.Reset()
	}

	return embedding
}
