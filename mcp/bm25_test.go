// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBM25SearchRanksRelevantDocuments(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "jira__get_issue", Text: "jira__get_issue get issue jira ticket"},
		{ID: "github__create_pull_request", Text: "github__create_pull_request create pull request"},
		{ID: "mattermost__search_users", Text: "mattermost__search_users search users"},
	})

	results := idx.Search("jira issue", 10)

	require.NotEmpty(t, results)
	require.Equal(t, "jira__get_issue", results[0].ID)
}

func TestBM25SearchUsesNameAndDescription(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "jira__get_issue", Text: "jira__get_issue get_issue"},
		{ID: "github__create_pull_request", Text: "opens a collaboration review"},
	})

	nameResults := idx.Search("get issue", 10)
	require.NotEmpty(t, nameResults)
	require.Equal(t, "jira__get_issue", nameResults[0].ID)

	descriptionResults := idx.Search("collaboration review", 10)
	require.NotEmpty(t, descriptionResults)
	require.Equal(t, "github__create_pull_request", descriptionResults[0].ID)
}

func TestBM25SearchLimitAndTieBreak(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "charlie", Text: "shared"},
		{ID: "bravo", Text: "shared"},
		{ID: "alpha", Text: "shared"},
	})

	results := idx.Search("shared", 2)

	require.Len(t, results, 2)
	require.Equal(t, "alpha", results[0].ID)
	require.Equal(t, "bravo", results[1].ID)
}

func TestBM25EmptyQueryReturnsNil(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "jira__get_issue", Text: "jira issue"},
	})

	require.Nil(t, idx.Search("", 10))
	require.Nil(t, idx.Search("   ", 10))
}

func TestBM25NoMatchingTokensReturnsNil(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "jira__get_issue", Text: "jira issue"},
	})

	require.Nil(t, idx.Search("github", 10))
}

func TestBM25TokenizeNonLatin(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "japanese", Text: "検索 ユーザー"},
		{ID: "chinese_contiguous", Text: "用户搜索"},
	})

	japaneseResults := idx.Search("検索", 10)
	require.NotEmpty(t, japaneseResults)
	require.Equal(t, "japanese", japaneseResults[0].ID)

	contiguousResults := idx.Search("用户搜索", 10)
	require.NotEmpty(t, contiguousResults)
	require.Equal(t, "chinese_contiguous", contiguousResults[0].ID)

	// There is no CJK segmentation: a substring query does not match a contiguous token.
	require.Nil(t, idx.Search("用户", 10))
}

func TestBM25TokenizeNamespacedSnakeNames(t *testing.T) {
	idx := NewBM25Index([]BM25Document{
		{ID: "jira__get_issue", Text: "jira__get_issue"},
	})

	results := idx.Search("get issue", 10)

	require.NotEmpty(t, results)
	require.Equal(t, "jira__get_issue", results[0].ID)
}
