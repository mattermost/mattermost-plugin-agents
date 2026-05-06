// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

const DefaultMCPToolSearchLimit = 8

type MCPToolRegistry struct {
	tools map[string]MCPToolRegistryEntry
	order []string
	bm25  *BM25Index
}

type MCPToolRegistryEntry struct {
	Tool             llm.Tool
	Name             string
	BareName         string
	ServerOrigin     string
	RetrievalSummary string
}

type MCPToolSearchResult struct {
	Name    string
	Summary string
	Score   float64
}

type MCPToolRegistryOption func(*mcpToolRegistryOptions)

type MCPToolRetrievalOverride struct {
	Summary string
}

type mcpToolRegistryOptions struct {
	retrievalOverrides map[string]MCPToolRetrievalOverride
}

func NewMCPToolRegistry(tools []llm.Tool, opts ...MCPToolRegistryOption) *MCPToolRegistry {
	options := mcpToolRegistryOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	registry := &MCPToolRegistry{
		tools: make(map[string]MCPToolRegistryEntry),
	}

	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}

		bareName := llm.BareMCPToolName(tool.Name)
		retrievalSummary := tool.Description
		if override, ok := options.retrievalOverrides[MCPToolRetrievalOverrideKey(tool.ServerOrigin, bareName)]; ok {
			if summary := strings.TrimSpace(override.Summary); summary != "" {
				retrievalSummary = summary
			}
		}

		if _, exists := registry.tools[tool.Name]; !exists {
			registry.order = append(registry.order, tool.Name)
		}

		registry.tools[tool.Name] = MCPToolRegistryEntry{
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

func WithMCPToolRetrievalOverrides(overrides map[string]MCPToolRetrievalOverride) MCPToolRegistryOption {
	return func(options *mcpToolRegistryOptions) {
		options.retrievalOverrides = overrides
	}
}

func MCPToolRetrievalOverrideKey(serverOrigin, toolName string) string {
	return serverOrigin + "\x00" + llm.BareMCPToolName(toolName)
}

func (r *MCPToolRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

func (r *MCPToolRegistry) List() []MCPToolRegistryEntry {
	if r == nil || len(r.order) == 0 {
		return nil
	}

	entries := make([]MCPToolRegistryEntry, 0, len(r.order))
	for _, name := range r.order {
		entries = append(entries, r.tools[name])
	}
	return entries
}

func (r *MCPToolRegistry) Lookup(name string) (MCPToolRegistryEntry, bool) {
	if r == nil {
		return MCPToolRegistryEntry{}, false
	}

	entry, ok := r.tools[name]
	return entry, ok
}

func (r *MCPToolRegistry) Search(query string, limit int) []MCPToolSearchResult {
	if r == nil || strings.TrimSpace(query) == "" {
		return nil
	}

	return r.searchWithIndex(query, normalizedMCPToolSearchLimit(limit))
}

func (r *MCPToolRegistry) ClosestMatches(name string, limit int) []MCPToolSearchResult {
	if r == nil || strings.TrimSpace(name) == "" {
		return nil
	}

	limit = normalizedMCPToolSearchLimit(limit)
	if results := r.searchWithIndex(name, limit); len(results) > 0 {
		return results
	}

	return r.closestMatchesByName(name, limit)
}

func (r *MCPToolRegistry) rebuildIndex() {
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

func (r *MCPToolRegistry) searchWithIndex(query string, limit int) []MCPToolSearchResult {
	bm25Results := r.bm25.Search(query, limit)
	if len(bm25Results) == 0 {
		return nil
	}

	results := make([]MCPToolSearchResult, 0, len(bm25Results))
	for _, result := range bm25Results {
		entry, ok := r.tools[result.ID]
		if !ok {
			continue
		}
		results = append(results, MCPToolSearchResult{
			Name:    entry.Name,
			Summary: entry.RetrievalSummary,
			Score:   result.Score,
		})
	}
	return results
}

func (r *MCPToolRegistry) closestMatchesByName(query string, limit int) []MCPToolSearchResult {
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

	results := make([]MCPToolSearchResult, 0, len(r.order))
	for _, name := range r.order {
		entry := r.tools[name]
		score := fallbackMCPToolNameScore(normalizedQuery, queryTokenSet, entry)
		if score <= 0 {
			continue
		}

		results = append(results, MCPToolSearchResult{
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

func fallbackMCPToolNameScore(normalizedQuery string, queryTokens map[string]bool, entry MCPToolRegistryEntry) float64 {
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
