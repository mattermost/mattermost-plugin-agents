// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package embeddings

import (
	"math"
	"sort"
)

// Recency bias defaults, chosen to match common time-decay ranking practice
// (Elasticsearch function_score exp decay, Azure AI Search freshness
// boosting). A 7-day half-life suits workplace chat, where this week's
// messages matter far more than last quarter's. The 0.7 floor bounds the
// worst-case demotion so a strong old match (raw 0.9 -> 0.63) still outranks
// a weak fresh one (raw <0.63), keeping old canonical answers findable.
const (
	DefaultRecencyHalfLifeDays = 7.0
	DefaultRecencyFloor        = 0.7
)

// Candidate pool sizing for over-fetch + rerank: fetch multiplier x limit
// candidates (bounded) by pure similarity, then rerank in memory. Since decay
// demotes a score by at most (1-floor), only candidates near the top raw
// score can be reordered, so a modest pool suffices.
const (
	recencyCandidateMultiplier = 4
	recencyMinCandidates       = 20
	recencyMaxCandidates       = 200
)

// RecencyBiasSettings holds resolved (defaulted and clamped) recency
// reranking parameters used by CompositeSearch.
type RecencyBiasSettings struct {
	Enabled      bool
	HalfLifeDays float64
	// Floor is the minimum decay multiplier in [0, 1]; it bounds the
	// worst-case demotion of old results to Floor x their raw score.
	Floor float64
}

// recencyMultiplier returns the decay multiplier in [floor, 1] for a document
// of the given age: floor + (1-floor) * 0.5^(ageDays/halfLifeDays).
// Non-positive ages (future timestamps, clock skew) return 1.
func recencyMultiplier(ageMillis int64, halfLifeDays, floor float64) float64 {
	if ageMillis <= 0 {
		return 1
	}
	ageDays := float64(ageMillis) / float64(MillisPerDay)
	decay := math.Pow(0.5, ageDays/halfLifeDays)
	return floor + (1-floor)*decay
}

// rerankByRecency reorders results by recency-adjusted score (raw similarity
// x decay multiplier) descending, tie-breaking by CreateAt descending and
// then by the incoming (similarity) order. The Score field is left as the raw
// similarity: the relevance surfaced to callers stays similarity-based;
// recency influences ordering only.
func rerankByRecency(results []SearchResult, nowMillis int64, settings RecencyBiasSettings) {
	adjusted := make(map[int]float64, len(results))
	order := make([]int, len(results))
	for i, r := range results {
		order[i] = i
		if r.Document.CreateAt <= 0 {
			// Unknown timestamp: treat as fully decayed rather than fresh.
			adjusted[i] = float64(r.Score) * settings.Floor
			continue
		}
		age := nowMillis - r.Document.CreateAt
		adjusted[i] = float64(r.Score) * recencyMultiplier(age, settings.HalfLifeDays, settings.Floor)
	}

	sort.SliceStable(order, func(a, b int) bool {
		i, j := order[a], order[b]
		if adjusted[i] != adjusted[j] {
			return adjusted[i] > adjusted[j]
		}
		return results[i].Document.CreateAt > results[j].Document.CreateAt
	})

	reordered := make([]SearchResult, len(results))
	for pos, idx := range order {
		reordered[pos] = results[idx]
	}
	copy(results, reordered)
}

// recencyFetchLimit returns the store-level limit for the candidate
// over-fetch. Offset is folded in because pagination must apply to the
// reranked order, not the raw similarity order. 0 means "store maximum"
// (rerank everything the store would return). The result may exceed
// MaxSearchResults; the caller detects that and falls back to store-side
// pagination, since the store would silently clamp the fetch and drop the
// tail of the window.
func recencyFetchLimit(limit, offset int) int {
	if limit <= 0 {
		return 0
	}
	pool := min(max(limit*recencyCandidateMultiplier, recencyMinCandidates), recencyMaxCandidates)
	pool = max(pool, limit)
	return max(offset, 0) + pool
}
