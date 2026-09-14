// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecencyMultiplier(t *testing.T) {
	tests := []struct {
		name         string
		ageMillis    int64
		halfLifeDays float64
		floor        float64
		expected     float64
	}{
		{
			name:         "zero age has no decay",
			ageMillis:    0,
			halfLifeDays: 7,
			floor:        0.7,
			expected:     1,
		},
		{
			name:         "future timestamp (negative age) has no decay",
			ageMillis:    -5 * MillisPerDay,
			halfLifeDays: 7,
			floor:        0.7,
			expected:     1,
		},
		{
			name:         "age of one half-life decays half the boostable range",
			ageMillis:    7 * MillisPerDay,
			halfLifeDays: 7,
			floor:        0.7,
			expected:     0.85, // 0.7 + 0.3*0.5
		},
		{
			name:         "age of two half-lives decays to a quarter of the range",
			ageMillis:    14 * MillisPerDay,
			halfLifeDays: 7,
			floor:        0.7,
			expected:     0.775, // 0.7 + 0.3*0.25
		},
		{
			name:         "very old age converges to the floor",
			ageMillis:    10 * 365 * MillisPerDay,
			halfLifeDays: 7,
			floor:        0.7,
			expected:     0.7,
		},
		{
			name:         "zero floor allows full decay",
			ageMillis:    1 * MillisPerDay,
			halfLifeDays: 1,
			floor:        0,
			expected:     0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recencyMultiplier(tt.ageMillis, tt.halfLifeDays, tt.floor)
			assert.InDelta(t, tt.expected, got, 1e-9)
		})
	}
}

func TestRecencyFetchLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		offset   int
		expected int
	}{
		{
			name:     "zero limit means store maximum",
			limit:    0,
			offset:   5,
			expected: 0,
		},
		{
			name:     "negative limit means store maximum",
			limit:    -3,
			offset:   0,
			expected: 0,
		},
		{
			name:     "small limit clamps pool to minimum",
			limit:    3,
			offset:   0,
			expected: recencyMinCandidates,
		},
		{
			name:     "typical limit multiplies",
			limit:    10,
			offset:   0,
			expected: 40,
		},
		{
			name:     "large limit clamps pool to maximum",
			limit:    100,
			offset:   0,
			expected: recencyMaxCandidates,
		},
		{
			name:     "pool never below limit",
			limit:    300,
			offset:   0,
			expected: 300,
		},
		{
			name:     "offset is folded into the fetch",
			limit:    5,
			offset:   10,
			expected: 30,
		},
		{
			name:     "negative offset is ignored",
			limit:    5,
			offset:   -4,
			expected: recencyMinCandidates,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, recencyFetchLimit(tt.limit, tt.offset))
		})
	}
}

func TestGetRecencyBiasSettings(t *testing.T) {
	tests := []struct {
		name     string
		config   EmbeddingSearchConfig
		expected RecencyBiasSettings
	}{
		{
			name:   "zero config falls back to defaults, disabled",
			config: EmbeddingSearchConfig{},
			expected: RecencyBiasSettings{
				Enabled:      false,
				HalfLifeDays: DefaultRecencyHalfLifeDays,
				Floor:        DefaultRecencyFloor,
			},
		},
		{
			name: "explicit values pass through",
			config: EmbeddingSearchConfig{
				RecencyBiasEnabled:  true,
				RecencyHalfLifeDays: 30,
				RecencyFloor:        0.5,
			},
			expected: RecencyBiasSettings{
				Enabled:      true,
				HalfLifeDays: 30,
				Floor:        0.5,
			},
		},
		{
			name: "negative values fall back to defaults",
			config: EmbeddingSearchConfig{
				RecencyBiasEnabled:  true,
				RecencyHalfLifeDays: -1,
				RecencyFloor:        -0.5,
			},
			expected: RecencyBiasSettings{
				Enabled:      true,
				HalfLifeDays: DefaultRecencyHalfLifeDays,
				Floor:        DefaultRecencyFloor,
			},
		},
		{
			name: "floor above one is clamped",
			config: EmbeddingSearchConfig{
				RecencyBiasEnabled: true,
				RecencyFloor:       1.5,
			},
			expected: RecencyBiasSettings{
				Enabled:      true,
				HalfLifeDays: DefaultRecencyHalfLifeDays,
				Floor:        1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.GetRecencyBiasSettings())
		})
	}
}

func TestRerankByRecency(t *testing.T) {
	const now = int64(1_700_000_000_000)
	defaultSettings := RecencyBiasSettings{Enabled: true, HalfLifeDays: 7, Floor: 0.7}

	result := func(postID string, score float32, ageDays int64) SearchResult {
		createAt := int64(0)
		if ageDays >= 0 {
			createAt = now - ageDays*MillisPerDay
		}
		return SearchResult{
			Document: PostDocument{PostID: postID, CreateAt: createAt},
			Score:    score,
		}
	}

	tests := []struct {
		name          string
		results       []SearchResult
		settings      RecencyBiasSettings
		expectedOrder []string
	}{
		{
			name: "fresh result overtakes decayed stronger match",
			results: []SearchResult{
				// old: 0.85 * (0.7 + 0.3*0.5^(30/7)) ~= 0.61 < fresh: 0.80
				result("old", 0.85, 30),
				result("fresh", 0.80, 0),
			},
			settings:      defaultSettings,
			expectedOrder: []string{"fresh", "old"},
		},
		{
			name: "floor keeps old canonical answer above weak fresh result",
			results: []SearchResult{
				result("weak-fresh", 0.50, 0),
				// old: 0.9 * ~0.7 = ~0.63 > 0.50
				result("old-canonical", 0.90, 365),
			},
			settings:      defaultSettings,
			expectedOrder: []string{"old-canonical", "weak-fresh"},
		},
		{
			name: "unknown timestamp is treated as fully decayed",
			results: []SearchResult{
				// no timestamp: 0.9 * 0.7 = 0.63 < fresh 0.7
				result("no-timestamp", 0.90, -1),
				result("fresh", 0.70, 0),
			},
			settings:      defaultSettings,
			expectedOrder: []string{"fresh", "no-timestamp"},
		},
		{
			name: "equal adjusted scores tie-break by recency",
			results: []SearchResult{
				result("older", 0.8, 20),
				result("newer", 0.8, 2),
			},
			// Floor 1 disables decay entirely, so adjusted scores are equal.
			settings:      RecencyBiasSettings{Enabled: true, HalfLifeDays: 7, Floor: 1},
			expectedOrder: []string{"newer", "older"},
		},
		{
			name: "identical results keep input (similarity) order",
			results: []SearchResult{
				result("first", 0.8, 3),
				result("second", 0.8, 3),
			},
			settings:      defaultSettings,
			expectedOrder: []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawScores := make(map[string]float32, len(tt.results))
			for _, r := range tt.results {
				rawScores[r.Document.PostID] = r.Score
			}

			rerankByRecency(tt.results, now, tt.settings)

			order := make([]string, len(tt.results))
			for i, r := range tt.results {
				order[i] = r.Document.PostID
			}
			assert.Equal(t, tt.expectedOrder, order)

			// Reranking must not rewrite the raw similarity scores.
			for _, r := range tt.results {
				assert.Equal(t, rawScores[r.Document.PostID], r.Score,
					"raw score of %s must be preserved", r.Document.PostID)
			}
		})
	}
}
