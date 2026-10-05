// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingLoader struct {
	texts map[string]string
	err   error
	calls [][]string
}

func (l *countingLoader) GetAgentDocumentTexts(ids []string) (map[string]string, error) {
	l.calls = append(l.calls, slices.Clone(ids))
	if l.err != nil {
		return nil, l.err
	}
	out := make(map[string]string)
	for _, id := range ids {
		if text, ok := l.texts[id]; ok {
			out[id] = text
		}
	}
	return out, nil
}

func TestTextCache(t *testing.T) {
	ctx := context.Background()

	t.Run("loads only uncached documents and skips missing ones", func(t *testing.T) {
		loader := &countingLoader{texts: map[string]string{"a": "alpha", "b": "beta"}}
		cache := NewTextCache(loader, 100)

		texts, err := cache.AgentDocumentTexts(ctx, []string{"a", "missing"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"a": "alpha"}, texts)

		texts, err = cache.AgentDocumentTexts(ctx, []string{"a", "b"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"a": "alpha", "b": "beta"}, texts)
		assert.Equal(t, [][]string{{"a", "missing"}, {"b"}}, loader.calls)
	})

	t.Run("evicts the least recently used text beyond the bound", func(t *testing.T) {
		loader := &countingLoader{texts: map[string]string{"a": "aaaa", "b": "bbbb", "c": "cccc"}}
		cache := NewTextCache(loader, 8)

		_, err := cache.AgentDocumentTexts(ctx, []string{"a"})
		require.NoError(t, err)
		_, err = cache.AgentDocumentTexts(ctx, []string{"b"})
		require.NoError(t, err)
		_, err = cache.AgentDocumentTexts(ctx, []string{"a"}) // a is now most recent
		require.NoError(t, err)
		_, err = cache.AgentDocumentTexts(ctx, []string{"c"}) // evicts b
		require.NoError(t, err)

		loader.calls = nil
		texts, err := cache.AgentDocumentTexts(ctx, []string{"a", "b", "c"})
		require.NoError(t, err)
		assert.Len(t, texts, 3)
		assert.Equal(t, [][]string{{"b"}}, loader.calls)
	})

	t.Run("texts larger than the cache are returned but not cached", func(t *testing.T) {
		loader := &countingLoader{texts: map[string]string{"big": "0123456789"}}
		cache := NewTextCache(loader, 5)
		for range 2 {
			texts, err := cache.AgentDocumentTexts(ctx, []string{"big"})
			require.NoError(t, err)
			assert.Equal(t, "0123456789", texts["big"])
		}
		assert.Len(t, loader.calls, 2)
	})

	t.Run("loader errors are returned", func(t *testing.T) {
		cache := NewTextCache(&countingLoader{err: errors.New("db down")}, 5)
		_, err := cache.AgentDocumentTexts(ctx, []string{"a"})
		require.Error(t, err)
	})
}
