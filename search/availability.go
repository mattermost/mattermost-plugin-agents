// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"sync/atomic"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
)

// Availability keeps indexing and query-time search gates separate.
//
// IndexSearch is available whenever search is initialized, so admins can run a
// full reindex while the configured model is incompatible with the existing
// index. QuerySearch additionally requires the predicate to report compatible
// model info, and evaluates that predicate on each call.
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
