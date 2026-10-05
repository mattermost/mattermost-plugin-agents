// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"container/list"
	"context"
	"sync"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"go.opentelemetry.io/otel/codes"
)

// DefaultTextCacheRunes bounds the text kept by a TextCache: enough for
// twenty agents with a full document budget.
const DefaultTextCacheRunes = 20 * MaxTotalTextRunes

// TextLoader loads the extracted text of stored documents.
type TextLoader interface {
	// GetAgentDocumentTexts returns the text of every document in ids that
	// exists, keyed by ID. Missing documents are absent from the result.
	GetAgentDocumentTexts(ids []string) (map[string]string, error)
}

// TextCache is a least-recently-used cache of document texts in front of a
// TextLoader. Stored documents never change, so entries never go stale; the
// cache only bounds memory by the total number of cached characters.
type TextCache struct {
	loader   TextLoader
	maxRunes int

	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
	runes   int
}

type textCacheEntry struct {
	id    string
	text  string
	runes int
}

// NewTextCache returns a cache holding at most maxRunes characters of text.
func NewTextCache(loader TextLoader, maxRunes int) *TextCache {
	return &TextCache{
		loader:   loader,
		maxRunes: maxRunes,
		order:    list.New(),
		entries:  make(map[string]*list.Element),
	}
}

// AgentDocumentTexts returns the text of every document in ids that exists,
// keyed by ID, loading the ones not cached yet in one call.
func (c *TextCache) AgentDocumentTexts(ctx context.Context, ids []string) (map[string]string, error) {
	texts := make(map[string]string, len(ids))
	var missing []string

	c.mu.Lock()
	for _, id := range ids {
		if el, ok := c.entries[id]; ok {
			c.order.MoveToFront(el)
			texts[id] = el.Value.(*textCacheEntry).text
		} else {
			missing = append(missing, id)
		}
	}
	c.mu.Unlock()

	if len(missing) == 0 {
		return texts, nil
	}

	_, span := telemetry.Tracer().Start(ctx, "load agent document texts")
	defer span.End()
	loaded, err := c.loader.GetAgentDocumentTexts(missing)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to load agent document texts")
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for id, text := range loaded {
		texts[id] = text
		c.add(id, text)
	}
	return texts, nil
}

// add caches text under id and evicts the least recently used entries beyond
// maxRunes. A text larger than the whole cache is not cached. c.mu must be held.
func (c *TextCache) add(id, text string) {
	if _, ok := c.entries[id]; ok {
		return
	}
	runes := utf8.RuneCountInString(text)
	if runes > c.maxRunes {
		return
	}
	c.entries[id] = c.order.PushFront(&textCacheEntry{id: id, text: text, runes: runes})
	c.runes += runes
	for c.runes > c.maxRunes {
		oldest := c.order.Back()
		entry := oldest.Value.(*textCacheEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.id)
		c.runes -= entry.runes
	}
}
