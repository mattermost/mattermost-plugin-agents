// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"sync/atomic"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
)

// Availability is the single source of truth for the initialized embedding
// search and whether it may be used for query-time search.
//
// Indexing and querying have different availability rules. When the configured
// embedding model no longer matches the model the existing index was built
// with, query-time search must be disabled (results would be wrong), but
// indexing must remain available — otherwise the admin cannot run the full
// reindex that is the only way to recover. Gating both on a single nil pointer
// (the previous behavior) deadlocked recovery: the reindex endpoint returned
// 500 because search was "not configured", and the reindex was the very thing
// needed to re-enable it.
//
// IndexSearch is therefore available whenever search is initialized, while
// QuerySearch additionally requires the query predicate to report the model as
// compatible. Because the predicate is evaluated on each call, query search
// re-enables automatically once a reindex updates the stored model info.
type Availability struct {
	search       atomic.Pointer[embeddings.EmbeddingSearch]
	queryAllowed atomic.Pointer[func() bool]
}

// NewAvailability returns an Availability with no search initialized.
func NewAvailability() *Availability {
	return &Availability{}
}

// SetQueryAllowedFunc installs the predicate used to decide whether query-time
// search is currently allowed. A nil predicate allows querying whenever search
// is initialized.
func (a *Availability) SetQueryAllowedFunc(fn func() bool) {
	if fn == nil {
		a.queryAllowed.Store(nil)
		return
	}
	a.queryAllowed.Store(&fn)
}

// Set stores the initialized embedding search. Passing nil marks search as
// uninitialized, which disables both indexing and querying.
func (a *Availability) Set(s embeddings.EmbeddingSearch) {
	if s == nil {
		a.search.Store(nil)
		return
	}
	a.search.Store(&s)
}

// IndexSearch returns the embedding search for indexing and reindexing. It is
// available whenever search is initialized, regardless of model compatibility.
func (a *Availability) IndexSearch() embeddings.EmbeddingSearch {
	ptr := a.search.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

// QuerySearch returns the embedding search for query-time use, or nil when
// search is uninitialized or the query predicate reports the configured model
// as incompatible with the existing index.
func (a *Availability) QuerySearch() embeddings.EmbeddingSearch {
	s := a.IndexSearch()
	if s == nil {
		return nil
	}
	if fn := a.queryAllowed.Load(); fn != nil && !(*fn)() {
		return nil
	}
	return s
}
