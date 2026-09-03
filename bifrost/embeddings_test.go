// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingDimensions(t *testing.T) {
	tests := []struct {
		name       string
		dimensions int
		expectSet  bool
	}{
		{
			name:       "dimensions > 0 sets Params",
			dimensions: 1536,
			expectSet:  true,
		},
		{
			name:       "dimensions == 0 does not set Params",
			dimensions: 0,
			expectSet:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build params the same way CreateEmbedding does
			var params *schemas.EmbeddingParameters
			if tt.dimensions > 0 {
				params = &schemas.EmbeddingParameters{
					Dimensions: new(tt.dimensions),
				}
			}

			if tt.expectSet {
				assert.NotNil(t, params)
				assert.NotNil(t, params.Dimensions)
				assert.Equal(t, tt.dimensions, *params.Dimensions)
			} else {
				assert.Nil(t, params)
			}
		})
	}
}

func TestSplitEmbeddingBatches(t *testing.T) {
	repeat := func(s string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = s
		}
		return out
	}

	tests := []struct {
		name        string
		texts       []string
		wantBatches []int // expected size of each batch
	}{
		{
			name:        "empty input yields no batches",
			texts:       nil,
			wantBatches: nil,
		},
		{
			name:        "small input stays in one batch",
			texts:       []string{"a", "b", "c"},
			wantBatches: []int{3},
		},
		{
			name:        "splits on input count limit",
			texts:       repeat("x", maxEmbeddingRequestInputs+1),
			wantBatches: []int{maxEmbeddingRequestInputs, 1},
		},
		{
			name: "splits on size limit",
			// 3 texts of just over half the size limit each: no two fit together
			texts:       repeat(string(make([]byte, maxEmbeddingRequestBytes/2+1)), 3),
			wantBatches: []int{1, 1, 1},
		},
		{
			name:        "single oversized text still gets a batch",
			texts:       []string{string(make([]byte, maxEmbeddingRequestBytes+1))},
			wantBatches: []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batches := splitEmbeddingBatches(tt.texts)

			var sizes []int
			total := 0
			for _, b := range batches {
				sizes = append(sizes, len(b))
				total += len(b)
			}
			assert.Equal(t, tt.wantBatches, sizes)
			assert.Equal(t, len(tt.texts), total, "no text may be dropped or duplicated")
		})
	}
}

// TestBatchCreateEmbeddingsSplitsLargeBatches drives BatchCreateEmbeddings
// against a real OpenAI-compatible endpoint and verifies that an over-limit
// batch is split into multiple upstream requests whose results are
// reassembled in input order.
func TestBatchCreateEmbeddingsSplitsLargeBatches(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)

		var req struct {
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.LessOrEqual(t, len(req.Input), maxEmbeddingRequestInputs,
			"a single upstream request must stay within the input cap")

		// Echo each input's trailing index as its embedding so the caller
		// can verify ordering across the split.
		data := make([]map[string]any, len(req.Input))
		for i, text := range req.Input {
			idx, err := strconv.Atoi(strings.TrimPrefix(text, "text-"))
			require.NoError(t, err)
			data[i] = map[string]any{
				"object":    "embedding",
				"index":     i,
				"embedding": []float64{float64(idx)},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  "test-model",
			"data":   data,
		}))
	}))
	defer server.Close()

	provider, err := NewEmbeddingProvider(EmbeddingConfig{
		Provider: schemas.OpenAI,
		APIKey:   "test-key",
		APIURL:   server.URL,
		Model:    "test-model",
	})
	require.NoError(t, err)
	defer provider.Shutdown()

	texts := make([]string, maxEmbeddingRequestInputs+10)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	result, err := provider.BatchCreateEmbeddings(t.Context(), texts)
	require.NoError(t, err)

	assert.Equal(t, int32(2), requests.Load(), "over-limit batch should be split into two upstream requests")
	require.Len(t, result, len(texts))
	for i, embedding := range result {
		require.Len(t, embedding, 1)
		assert.Equal(t, float32(i), embedding[0], "embedding %d does not correspond to its input text", i)
	}
}
