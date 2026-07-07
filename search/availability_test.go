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
	// boolPtr distinguishes "no predicate installed" (nil) from an installed
	// predicate returning true/false.
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name         string
		initialized  bool
		queryAllowed *bool
		wantIndex    bool
		wantQuery    bool
	}{
		{
			name:        "uninitialized disables indexing and querying",
			initialized: false,
			wantIndex:   false,
			wantQuery:   false,
		},
		{
			name:         "initialized without predicate allows both",
			initialized:  true,
			queryAllowed: nil,
			wantIndex:    true,
			wantQuery:    true,
		},
		{
			// Core of the fix: an incompatible model must not disable indexing,
			// otherwise the recovery reindex cannot run.
			name:         "incompatible model keeps indexing available but disables querying",
			initialized:  true,
			queryAllowed: boolPtr(false),
			wantIndex:    true,
			wantQuery:    false,
		},
		{
			name:         "compatible model allows querying",
			initialized:  true,
			queryAllowed: boolPtr(true),
			wantIndex:    true,
			wantQuery:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAvailability()
			var s embeddings.EmbeddingSearch
			if tc.initialized {
				s = mocks.NewMockEmbeddingSearch(t)
				a.Set(s)
			}
			if tc.queryAllowed != nil {
				allowed := *tc.queryAllowed
				a.SetQueryAllowedFunc(func() bool { return allowed })
			}

			if tc.wantIndex {
				require.Equal(t, s, a.IndexSearch())
			} else {
				require.Nil(t, a.IndexSearch())
			}
			if tc.wantQuery {
				require.Equal(t, s, a.QuerySearch())
			} else {
				require.Nil(t, a.QuerySearch())
			}
		})
	}

	t.Run("query search re-enables when the model becomes compatible again", func(t *testing.T) {
		// Simulates a reindex updating the stored model info so the predicate
		// flips from incompatible to compatible without re-setting the search.
		a := NewAvailability()
		s := mocks.NewMockEmbeddingSearch(t)
		a.Set(s)

		compatible := false
		a.SetQueryAllowedFunc(func() bool { return compatible })

		require.Nil(t, a.QuerySearch())
		require.Equal(t, s, a.IndexSearch())

		compatible = true
		require.Equal(t, s, a.QuerySearch(), "query search should recover automatically after a reindex")
	})

	t.Run("predicate is not consulted when search is uninitialized", func(t *testing.T) {
		// In production the predicate reads the stored model info from the KV
		// store; short-circuiting on a nil search avoids that DB access during
		// early initialization when no search exists yet.
		a := NewAvailability()
		a.SetQueryAllowedFunc(func() bool {
			t.Fatal("predicate must not be called when search is nil")
			return true
		})
		require.Nil(t, a.QuerySearch())
	})

	t.Run("setting nil disables a previously initialized search", func(t *testing.T) {
		a := NewAvailability()
		a.Set(mocks.NewMockEmbeddingSearch(t))
		a.Set(nil)
		require.Nil(t, a.IndexSearch())
		require.Nil(t, a.QuerySearch())
	})

	t.Run("clearing predicate re-enables querying after model incompatibility", func(t *testing.T) {
		a := NewAvailability()
		s := mocks.NewMockEmbeddingSearch(t)
		a.Set(s)
		a.SetQueryAllowedFunc(func() bool { return false })
		require.Nil(t, a.QuerySearch())

		a.SetQueryAllowedFunc(nil)
		require.Equal(t, s, a.QuerySearch())
	})
}
