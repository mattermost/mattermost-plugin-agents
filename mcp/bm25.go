// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

type BM25Document struct {
	ID   string
	Text string
}

type BM25Result struct {
	ID    string
	Score float64
}

type BM25Index struct {
	documents        []bm25IndexedDocument
	documentCount    int
	documentFreqs    map[string]int
	averageDocLength float64
}

type bm25IndexedDocument struct {
	id         string
	termFreqs  map[string]int
	tokenCount float64
}

func NewBM25Index(docs []BM25Document) *BM25Index {
	idx := &BM25Index{
		documents:     make([]bm25IndexedDocument, 0, len(docs)),
		documentCount: len(docs),
		documentFreqs: make(map[string]int),
	}

	var totalDocLength float64
	for _, doc := range docs {
		tokens := tokenizeBM25Text(doc.Text)
		termFreqs := make(map[string]int)
		for _, token := range tokens {
			termFreqs[token]++
		}

		for token := range termFreqs {
			idx.documentFreqs[token]++
		}

		tokenCount := float64(len(tokens))
		totalDocLength += tokenCount
		idx.documents = append(idx.documents, bm25IndexedDocument{
			id:         doc.ID,
			termFreqs:  termFreqs,
			tokenCount: tokenCount,
		})
	}

	if idx.documentCount > 0 {
		idx.averageDocLength = totalDocLength / float64(idx.documentCount)
	}

	return idx
}

func (idx *BM25Index) Search(query string, limit int) []BM25Result {
	if idx == nil || idx.documentCount == 0 || idx.averageDocLength == 0 {
		return nil
	}

	queryTokens := uniqueBM25Tokens(tokenizeBM25Text(query))
	if len(queryTokens) == 0 {
		return nil
	}

	results := make([]BM25Result, 0, len(idx.documents))
	for _, doc := range idx.documents {
		var score float64
		for _, token := range queryTokens {
			tf := float64(doc.termFreqs[token])
			if tf == 0 {
				continue
			}

			df := idx.documentFreqs[token]
			idf := math.Log(1 + (float64(idx.documentCount-df)+0.5)/(float64(df)+0.5))
			score += idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*doc.tokenCount/idx.averageDocLength))
		}

		if score > 0 {
			results = append(results, BM25Result{
				ID:    doc.id,
				Score: score,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})

	if len(results) == 0 {
		return nil
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

func tokenizeBM25Text(text string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(unicode.ToLower(r))
			continue
		}

		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

func uniqueBM25Tokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		unique = append(unique, token)
	}
	return unique
}
