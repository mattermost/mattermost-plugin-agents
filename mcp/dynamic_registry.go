// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const DefaultMCPToolSearchLimit = 8

type ToolRegistry struct {
	tools map[string]ToolRegistryEntry
	order []string
	bm25  *BM25Index
}

type ToolRegistryEntry struct {
	Tool             llm.Tool
	Name             string
	BareName         string
	ServerOrigin     string
	RetrievalSummary string
}

type ToolSearchResult struct {
	Name    string
	Summary string
	Score   float64
}

type ToolRegistryOption func(*toolRegistryOptions)

type toolRegistryOptions struct {
	retrievalOverrides map[string]ToolRetrievalOverride
}

func NewToolRegistry(tools []llm.Tool, opts ...ToolRegistryOption) *ToolRegistry {
	options := toolRegistryOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	registry := &ToolRegistry{
		tools: make(map[string]ToolRegistryEntry),
	}

	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}

		bareName := llm.BareMCPToolName(tool.Name)
		retrievalSummary := tool.Description
		if override, ok := options.retrievalOverrides[ToolRetrievalOverrideKey(tool.ServerOrigin, bareName)]; ok {
			if summary := strings.TrimSpace(override.Summary); summary != "" {
				retrievalSummary = summary
			}
		}

		if _, exists := registry.tools[tool.Name]; !exists {
			registry.order = append(registry.order, tool.Name)
		}

		registry.tools[tool.Name] = ToolRegistryEntry{
			Tool:             tool,
			Name:             tool.Name,
			BareName:         bareName,
			ServerOrigin:     tool.ServerOrigin,
			RetrievalSummary: retrievalSummary,
		}
	}

	sort.Strings(registry.order)
	registry.rebuildIndex()

	return registry
}

func WithToolRetrievalOverrides(overrides map[string]ToolRetrievalOverride) ToolRegistryOption {
	return func(options *toolRegistryOptions) {
		options.retrievalOverrides = overrides
	}
}

func (r *ToolRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

func (r *ToolRegistry) List() []ToolRegistryEntry {
	if r == nil || len(r.order) == 0 {
		return nil
	}

	entries := make([]ToolRegistryEntry, 0, len(r.order))
	for _, name := range r.order {
		entries = append(entries, r.tools[name])
	}
	return entries
}

func (r *ToolRegistry) Lookup(name string) (ToolRegistryEntry, bool) {
	if r == nil {
		return ToolRegistryEntry{}, false
	}

	entry, ok := r.tools[name]
	return entry, ok
}

func (r *ToolRegistry) Search(query string, limit int) []ToolSearchResult {
	if r == nil || strings.TrimSpace(query) == "" {
		return nil
	}

	return r.searchWithIndex(query, normalizedMCPToolSearchLimit(limit))
}

func (r *ToolRegistry) ClosestMatches(name string, limit int) []ToolSearchResult {
	if r == nil || strings.TrimSpace(name) == "" {
		return nil
	}

	limit = normalizedMCPToolSearchLimit(limit)
	if results := r.searchWithIndex(name, limit); len(results) > 0 {
		return results
	}

	return r.closestMatchesByName(name, limit)
}

func (r *ToolRegistry) rebuildIndex() {
	docs := make([]BM25Document, 0, len(r.order))
	for _, name := range r.order {
		entry := r.tools[name]
		docs = append(docs, BM25Document{
			ID:   entry.Name,
			Text: entry.Name + " " + entry.BareName + " " + entry.RetrievalSummary,
		})
	}
	r.bm25 = NewBM25Index(docs)
}

func (r *ToolRegistry) searchWithIndex(query string, limit int) []ToolSearchResult {
	if r == nil || r.bm25 == nil {
		return nil
	}

	bm25Results := r.bm25.Search(query, limit)
	if len(bm25Results) == 0 {
		return nil
	}

	results := make([]ToolSearchResult, 0, len(bm25Results))
	for _, result := range bm25Results {
		entry, ok := r.tools[result.ID]
		if !ok {
			continue
		}
		results = append(results, ToolSearchResult{
			Name:    entry.Name,
			Summary: entry.RetrievalSummary,
			Score:   result.Score,
		})
	}
	return results
}

func (r *ToolRegistry) closestMatchesByName(query string, limit int) []ToolSearchResult {
	normalizedQuery := normalizedMCPToolName(query)
	if normalizedQuery == "" {
		return nil
	}

	queryTokens := tokenizeBM25Text(query)
	if len(queryTokens) == 0 {
		return nil
	}
	queryTokenSet := make(map[string]bool, len(queryTokens))
	for _, token := range queryTokens {
		queryTokenSet[token] = true
	}

	results := make([]ToolSearchResult, 0, len(r.order))
	for _, name := range r.order {
		entry := r.tools[name]
		score := fallbackMCPToolNameScore(normalizedQuery, queryTokenSet, entry)
		if score <= 0 {
			continue
		}

		results = append(results, ToolSearchResult{
			Name:    entry.Name,
			Summary: entry.RetrievalSummary,
			Score:   score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Name < results[j].Name
		}
		return results[i].Score > results[j].Score
	})

	if len(results) == 0 {
		return nil
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

func fallbackMCPToolNameScore(normalizedQuery string, queryTokens map[string]bool, entry ToolRegistryEntry) float64 {
	normalizedCandidate := normalizedMCPToolName(entry.Name)
	var score float64
	if strings.Contains(normalizedCandidate, normalizedQuery) || strings.Contains(normalizedQuery, normalizedCandidate) {
		score += 2
	}

	candidateTokens := tokenizeBM25Text(entry.Name + " " + entry.BareName)
	seenCandidateTokens := make(map[string]bool, len(candidateTokens))
	for _, token := range candidateTokens {
		if seenCandidateTokens[token] {
			continue
		}
		seenCandidateTokens[token] = true
		if queryTokens[token] {
			score++
		}
	}

	return score
}

func normalizedMCPToolName(name string) string {
	return strings.Join(tokenizeBM25Text(name), " ")
}

func normalizedMCPToolSearchLimit(limit int) int {
	if limit <= 0 {
		return DefaultMCPToolSearchLimit
	}
	return limit
}
