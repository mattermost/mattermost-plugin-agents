// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/stretchr/testify/require"
)

func TestAvailability(t *testing.T) {
	newSearch := func(t *testing.T) embeddings.EmbeddingSearch {
		return mocks.NewMockEmbeddingSearch(t)
	}

	t.Run("uninitialized disables indexing and querying", func(t *testing.T) {
		a := NewAvailability()
		require.Nil(t, a.IndexSearch())
		require.Nil(t, a.QuerySearch())
	})

	t.Run("initialized without predicate allows both", func(t *testing.T) {
		a := NewAvailability()
		s := newSearch(t)
		a.Set(s)
		require.Equal(t, s, a.IndexSearch())
		require.Equal(t, s, a.QuerySearch())
	})

	t.Run("incompatible model keeps indexing available but disables querying", func(t *testing.T) {
		// This is the core of the fix: when the configured model is
		// incompatible with the existing index, a reindex (which uses
		// IndexSearch) must still be possible, while query search is disabled.
		a := NewAvailability()
		s := newSearch(t)
		a.Set(s)
		a.SetQueryAllowedFunc(func() bool { return false })

		require.Equal(t, s, a.IndexSearch(), "indexing must remain available so a reindex can recover")
		require.Nil(t, a.QuerySearch(), "query search must be disabled while the model is incompatible")
	})

	t.Run("compatible model allows querying", func(t *testing.T) {
		a := NewAvailability()
		s := newSearch(t)
		a.Set(s)
		a.SetQueryAllowedFunc(func() bool { return true })

		require.Equal(t, s, a.IndexSearch())
		require.Equal(t, s, a.QuerySearch())
	})

	t.Run("query search re-enables when the model becomes compatible again", func(t *testing.T) {
		// Simulates a reindex updating the stored model info so the predicate
		// flips from incompatible to compatible without re-setting the search.
		a := NewAvailability()
		s := newSearch(t)
		a.Set(s)

		compatible := false
		a.SetQueryAllowedFunc(func() bool { return compatible })

		require.Nil(t, a.QuerySearch())
		require.Equal(t, s, a.IndexSearch())

		compatible = true
		require.Equal(t, s, a.QuerySearch(), "query search should recover automatically after a reindex")
	})

	t.Run("predicate is not consulted when search is uninitialized", func(t *testing.T) {
		a := NewAvailability()
		a.SetQueryAllowedFunc(func() bool {
			t.Fatal("predicate must not be called when search is nil")
			return true
		})
		require.Nil(t, a.QuerySearch())
	})

	t.Run("setting nil disables a previously initialized search", func(t *testing.T) {
		a := NewAvailability()
		a.Set(newSearch(t))
		a.Set(nil)
		require.Nil(t, a.IndexSearch())
		require.Nil(t, a.QuerySearch())
	})
}
