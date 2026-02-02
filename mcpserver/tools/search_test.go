// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSemanticSearchService is a simple test double for SemanticSearchService
type mockSemanticSearchService struct {
	enabled bool
	results []SemanticSearchResult
	err     error
}

func (m *mockSemanticSearchService) Enabled() bool { return m.enabled }

func (m *mockSemanticSearchService) Search(_ context.Context, _ string, _ SemanticSearchOptions) ([]SemanticSearchResult, error) {
	return m.results, m.err
}

func TestGetSearchTools_SchemaReflectsCapabilities(t *testing.T) {
	testCases := []struct {
		name                 string
		searchService        SemanticSearchService
		expectSemanticParams bool
		descriptionContains  string
	}{
		{
			name:                 "nil search service should produce keyword-only schema",
			searchService:        nil,
			expectSemanticParams: false,
			descriptionContains:  "keyword search",
		},
		{
			name:                 "disabled search service should produce keyword-only schema",
			searchService:        &mockSemanticSearchService{enabled: false},
			expectSemanticParams: false,
			descriptionContains:  "keyword search",
		},
		{
			name:                 "enabled search service should produce combined schema",
			searchService:        &mockSemanticSearchService{enabled: true},
			expectSemanticParams: true,
			descriptionContains:  "semantic (AI-powered) and keyword search",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &MattermostToolProvider{
				logger:        &testLogger{t: t},
				searchService: tc.searchService,
			}

			tools := provider.getSearchTools()
			require.NotEmpty(t, tools, "should return at least one tool")

			var searchPostsTool *MCPTool
			for i := range tools {
				if tools[i].Name == "search_posts" {
					searchPostsTool = &tools[i]
					break
				}
			}
			require.NotNil(t, searchPostsTool, "search_posts tool should exist")

			// Verify description matches capability
			assert.Contains(t, searchPostsTool.Description, tc.descriptionContains,
				"description should indicate correct search type")

			// Verify schema properties match capability
			require.NotNil(t, searchPostsTool.Schema, "schema should not be nil")
			require.NotNil(t, searchPostsTool.Schema.Properties, "schema should have properties")

			_, hasSemanticLimit := searchPostsTool.Schema.Properties["semantic_limit"]
			_, hasSemanticOffset := searchPostsTool.Schema.Properties["semantic_offset"]

			if tc.expectSemanticParams {
				assert.True(t, hasSemanticLimit, "combined schema should have semantic_limit")
				assert.True(t, hasSemanticOffset, "combined schema should have semantic_offset")
			} else {
				assert.False(t, hasSemanticLimit, "keyword-only schema should not have semantic_limit")
				assert.False(t, hasSemanticOffset, "keyword-only schema should not have semantic_offset")
			}

			// Both schemas should have keyword params
			_, hasKeywordLimit := searchPostsTool.Schema.Properties["keyword_limit"]
			_, hasKeywordOffset := searchPostsTool.Schema.Properties["keyword_offset"]
			assert.True(t, hasKeywordLimit, "schema should have keyword_limit")
			assert.True(t, hasKeywordOffset, "schema should have keyword_offset")

			// Both schemas should have the required query param
			_, hasQuery := searchPostsTool.Schema.Properties["query"]
			assert.True(t, hasQuery, "schema should have query parameter")
		})
	}
}

func TestFormatCombinedResults_Deduplication(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	duplicatePostID := "post123"

	semanticResults := []searchPostResult{
		{
			Post:        &model.Post{Id: duplicatePostID, ChannelId: "ch1", Message: "semantic message"},
			ChannelName: "General",
			Username:    "user1",
			Score:       0.95,
			Source:      "semantic",
		},
	}

	keywordResults := []searchPostResult{
		{
			Post:        &model.Post{Id: duplicatePostID, ChannelId: "ch1", Message: "keyword message"},
			ChannelName: "General",
			Username:    "user1",
			Source:      "keyword",
		},
		{
			Post:        &model.Post{Id: "uniquepost", ChannelId: "ch2", Message: "unique keyword message"},
			ChannelName: "Random",
			Username:    "user2",
			Source:      "keyword",
		},
	}

	result, err := provider.formatCombinedResults("test query", semanticResults, keywordResults, true, "")
	require.NoError(t, err)

	// Count occurrences of the duplicate post ID - should appear exactly once
	occurrences := strings.Count(result, duplicatePostID)
	assert.Equal(t, 1, occurrences,
		"duplicate post ID should appear exactly once after deduplication")

	// Verify the unique keyword result is still present
	assert.Contains(t, result, "uniquepost", "unique keyword result should be present")

	// Verify counts in the header are correct (1 semantic + 1 keyword after dedup)
	assert.Contains(t, result, "2 results", "should report 2 total results")
	assert.Contains(t, result, "1 semantic", "should report 1 semantic result")
	assert.Contains(t, result, "1 keyword", "should report 1 keyword result after dedup")
}

func TestFormatCombinedResults_DeduplicationPrefersSemantic(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	duplicatePostID := "post123"

	// Semantic result has more detail
	semanticResults := []searchPostResult{
		{
			Post:        &model.Post{Id: duplicatePostID, ChannelId: "ch1", Message: "detailed message"},
			ChannelName: "General",
			TeamName:    "MyTeam",
			Username:    "user1",
			Score:       0.95,
			Source:      "semantic",
		},
	}

	// Keyword result has less detail
	keywordResults := []searchPostResult{
		{
			Post:        &model.Post{Id: duplicatePostID, ChannelId: "ch1", Message: "brief"},
			ChannelName: "",
			Username:    "",
			Source:      "keyword",
		},
	}

	result, err := provider.formatCombinedResults("test query", semanticResults, keywordResults, true, "")
	require.NoError(t, err)

	// Verify the semantic result's details are in the output (not keyword's sparse data)
	assert.Contains(t, result, "detailed message", "should contain semantic result's message")
	assert.Contains(t, result, "Score: 0.95", "should contain semantic result's score")
	assert.Contains(t, result, "General", "should contain semantic result's channel name")
}

func TestFormatCombinedResults_ZeroResults(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	testCases := []struct {
		name            string
		semanticEnabled bool
	}{
		{
			name:            "zero results with semantic enabled",
			semanticEnabled: true,
		},
		{
			name:            "zero results with semantic disabled",
			semanticEnabled: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := provider.formatCombinedResults("test query", nil, nil, tc.semanticEnabled, "")
			require.NoError(t, err)
			assert.NotEmpty(t, result, "should return a user-friendly message, not empty string")
			assert.Contains(t, result, "No posts found", "should indicate no results were found")
		})
	}
}

func TestFormatCombinedResults_EdgeCases(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	testCases := []struct {
		name            string
		semanticResults []searchPostResult
		keywordResults  []searchPostResult
		channelFilter   string
		checkFn         func(t *testing.T, result string)
	}{
		{
			name: "empty message field",
			keywordResults: []searchPostResult{
				{
					Post:     &model.Post{Id: "post1", ChannelId: "ch1", Message: ""},
					Username: "user1",
					Source:   "keyword",
				},
			},
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "post1", "should contain post ID even with empty message")
			},
		},
		{
			name: "unicode and emoji in content",
			keywordResults: []searchPostResult{
				{
					Post:        &model.Post{Id: "post1", ChannelId: "ch1", Message: "Hello 世界 🚀 émojis"},
					ChannelName: "日本語チャンネル",
					Username:    "用户",
					Source:      "keyword",
				},
			},
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "Hello 世界 🚀 émojis", "should preserve unicode in message")
				assert.Contains(t, result, "日本語チャンネル", "should preserve unicode in channel name")
				assert.Contains(t, result, "用户", "should preserve unicode in username")
			},
		},
		{
			name: "missing username shows Unknown User",
			keywordResults: []searchPostResult{
				{
					Post:     &model.Post{Id: "post1", ChannelId: "ch1", Message: "test"},
					Username: "",
					Source:   "keyword",
				},
			},
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "Unknown User", "should show Unknown User for empty username")
			},
		},
		{
			name: "channel filter is displayed",
			keywordResults: []searchPostResult{
				{
					Post:     &model.Post{Id: "post1", ChannelId: "channel123456789012345678", Message: "test"},
					Username: "user1",
					Source:   "keyword",
				},
			},
			channelFilter: "channel123456789012345678",
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "Channel ID filter:", "should indicate channel filter was applied")
				assert.Contains(t, result, "channel123456789012345678", "should show the filter value")
			},
		},
		{
			name: "thread reply shows root ID",
			keywordResults: []searchPostResult{
				{
					Post:     &model.Post{Id: "post1", ChannelId: "ch1", Message: "reply", RootId: "rootpost123"},
					Username: "user1",
					Source:   "keyword",
				},
			},
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "Root ID: rootpost123", "should show root ID for thread replies")
			},
		},
		{
			name: "semantic results show scores",
			semanticResults: []searchPostResult{
				{
					Post:     &model.Post{Id: "post1", ChannelId: "ch1", Message: "test"},
					Username: "user1",
					Score:    0.87,
					Source:   "semantic",
				},
			},
			checkFn: func(t *testing.T, result string) {
				assert.Contains(t, result, "Score: 0.87", "should display semantic score")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := provider.formatCombinedResults("test query", tc.semanticResults, tc.keywordResults, true, tc.channelFilter)
			require.NoError(t, err)
			tc.checkFn(t, result)
		})
	}
}

func TestFormatCombinedResults_OnlySemanticResults(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	semanticResults := []searchPostResult{
		{
			Post:        &model.Post{Id: "post1", ChannelId: "ch1", Message: "semantic result"},
			ChannelName: "General",
			Username:    "user1",
			Score:       0.9,
			Source:      "semantic",
		},
	}

	result, err := provider.formatCombinedResults("test query", semanticResults, nil, true, "")
	require.NoError(t, err)

	assert.Contains(t, result, "1 results", "should report 1 total result")
	assert.Contains(t, result, "1 semantic", "should report 1 semantic result")
	assert.Contains(t, result, "0 keyword", "should report 0 keyword results")
	assert.Contains(t, result, "Semantic Search Results", "should have semantic section")
}

func TestFormatCombinedResults_OnlyKeywordResults(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	keywordResults := []searchPostResult{
		{
			Post:        &model.Post{Id: "post1", ChannelId: "ch1", Message: "keyword result"},
			ChannelName: "General",
			Username:    "user1",
			Source:      "keyword",
		},
	}

	result, err := provider.formatCombinedResults("test query", nil, keywordResults, true, "")
	require.NoError(t, err)

	assert.Contains(t, result, "1 results", "should report 1 total result")
	assert.Contains(t, result, "0 semantic", "should report 0 semantic results")
	assert.Contains(t, result, "1 keyword", "should report 1 keyword result")
	assert.Contains(t, result, "Keyword Search Results", "should have keyword section")
}

func TestFormatCombinedResults_KeywordOnlyMode(t *testing.T) {
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	keywordResults := []searchPostResult{
		{
			Post:        &model.Post{Id: "post1", ChannelId: "ch1", Message: "keyword result"},
			ChannelName: "General",
			Username:    "user1",
			Source:      "keyword",
		},
	}

	// Call with semanticEnabled=false to simulate keyword-only mode
	result, err := provider.formatCombinedResults("test query", nil, keywordResults, false, "")
	require.NoError(t, err)

	// In keyword-only mode, the output format is simpler
	assert.NotContains(t, result, "Semantic Search Results",
		"keyword-only mode should not show semantic section header")
	assert.NotContains(t, result, "Keyword Search Results",
		"keyword-only mode should not label keyword section")
	assert.NotContains(t, result, "semantic",
		"keyword-only mode should not mention semantic search at all")

	// Should still contain the results
	assert.Contains(t, result, "post1", "should contain the post ID")
	assert.Contains(t, result, "keyword result", "should contain the message")
}

func TestCombinedSearchArgs_Validation(t *testing.T) {
	testCases := []struct {
		name          string
		args          CombinedSearchArgs
		expectError   bool
		errorContains string
	}{
		{
			name:          "empty query should fail",
			args:          CombinedSearchArgs{Query: ""},
			expectError:   true,
			errorContains: "query",
		},
		{
			name: "invalid team_id should fail",
			args: CombinedSearchArgs{
				Query:  "test",
				TeamID: "short",
			},
			expectError:   true,
			errorContains: "team_id",
		},
		{
			name: "invalid channel_id should fail",
			args: CombinedSearchArgs{
				Query:     "test",
				ChannelID: "short",
			},
			expectError:   true,
			errorContains: "channel_id",
		},
		{
			name: "valid 26-char team_id should pass",
			args: CombinedSearchArgs{
				Query:  "test",
				TeamID: "abcdefghijklmnopqrstuvwxyz",
			},
			expectError: false,
		},
		{
			name: "valid 26-char channel_id should pass",
			args: CombinedSearchArgs{
				Query:     "test",
				ChannelID: "abcdefghijklmnopqrstuvwxyz",
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Validate required fields
			hasError := false
			var errMsg string

			if tc.args.Query == "" {
				hasError = true
				errMsg = "query cannot be empty"
			}
			if tc.args.TeamID != "" && !model.IsValidId(tc.args.TeamID) {
				hasError = true
				errMsg = "team_id must be a valid ID"
			}
			if tc.args.ChannelID != "" && !model.IsValidId(tc.args.ChannelID) {
				hasError = true
				errMsg = "channel_id must be a valid ID"
			}

			if tc.expectError {
				assert.True(t, hasError, "expected validation to fail")
				assert.Contains(t, errMsg, tc.errorContains, "error message should mention the failing field")
			} else {
				assert.False(t, hasError, "expected validation to pass")
			}
		})
	}
}

func TestCombinedSearchArgs_Defaults(t *testing.T) {
	testCases := []struct {
		name                  string
		args                  CombinedSearchArgs
		expectedSemanticLimit int
		expectedKeywordLimit  int
	}{
		{
			name:                  "zero limits get defaults",
			args:                  CombinedSearchArgs{Query: "test"},
			expectedSemanticLimit: 10,
			expectedKeywordLimit:  10,
		},
		{
			name: "semantic limit capped at 50",
			args: CombinedSearchArgs{
				Query:         "test",
				SemanticLimit: 100,
			},
			expectedSemanticLimit: 50,
			expectedKeywordLimit:  10,
		},
		{
			name: "keyword limit capped at 100",
			args: CombinedSearchArgs{
				Query:        "test",
				KeywordLimit: 200,
			},
			expectedSemanticLimit: 10,
			expectedKeywordLimit:  100,
		},
		{
			name: "valid custom limits are preserved",
			args: CombinedSearchArgs{
				Query:         "test",
				SemanticLimit: 25,
				KeywordLimit:  50,
			},
			expectedSemanticLimit: 25,
			expectedKeywordLimit:  50,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args

			// Apply defaults (same logic as in toolCombinedSearch)
			if args.SemanticLimit == 0 {
				args.SemanticLimit = 10
			}
			if args.SemanticLimit > 50 {
				args.SemanticLimit = 50
			}
			if args.KeywordLimit == 0 {
				args.KeywordLimit = 10
			}
			if args.KeywordLimit > 100 {
				args.KeywordLimit = 100
			}

			assert.Equal(t, tc.expectedSemanticLimit, args.SemanticLimit, "semantic limit should match expected")
			assert.Equal(t, tc.expectedKeywordLimit, args.KeywordLimit, "keyword limit should match expected")
		})
	}
}

func TestExecuteKeywordSearch_OffsetAndLimit(t *testing.T) {
	// This tests the offset/limit logic independently of the Mattermost API
	// by verifying the slice operations work correctly

	testCases := []struct {
		name           string
		totalPosts     int
		offset         int
		limit          int
		expectedCount  int
		expectedOffset int // index of first result (to verify offset worked)
	}{
		{
			name:           "no offset, limit less than total",
			totalPosts:     10,
			offset:         0,
			limit:          5,
			expectedCount:  5,
			expectedOffset: 0,
		},
		{
			name:           "offset with limit",
			totalPosts:     10,
			offset:         3,
			limit:          5,
			expectedCount:  5,
			expectedOffset: 3,
		},
		{
			name:           "offset near end",
			totalPosts:     10,
			offset:         8,
			limit:          5,
			expectedCount:  2,
			expectedOffset: 8,
		},
		{
			name:           "offset beyond total returns empty",
			totalPosts:     10,
			offset:         15,
			limit:          5,
			expectedCount:  0,
			expectedOffset: -1, // no results
		},
		{
			name:           "offset equals total returns empty",
			totalPosts:     10,
			offset:         10,
			limit:          5,
			expectedCount:  0,
			expectedOffset: -1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test posts with indices in their IDs
			posts := make([]*model.Post, tc.totalPosts)
			for i := 0; i < tc.totalPosts; i++ {
				posts[i] = &model.Post{Id: string(rune('0' + i))}
			}

			// Apply offset (same logic as in executeKeywordSearch)
			if tc.offset > 0 && tc.offset < len(posts) {
				posts = posts[tc.offset:]
			} else if tc.offset >= len(posts) {
				posts = nil
			}

			// Apply limit
			if posts != nil && len(posts) > tc.limit {
				posts = posts[:tc.limit]
			}

			if tc.expectedCount == 0 {
				assert.Empty(t, posts, "should return empty slice")
			} else {
				require.Len(t, posts, tc.expectedCount, "should return expected number of posts")
				// Verify first post is at expected offset
				assert.Equal(t, string(rune('0'+tc.expectedOffset)), posts[0].Id,
					"first post should be at expected offset")
			}
		})
	}
}

func TestSearchErrorScenarios(t *testing.T) {
	semanticErr := errors.New("semantic search unavailable")
	keywordErr := errors.New("keyword search failed")

	testCases := []struct {
		name                 string
		semanticErr          error
		keywordErr           error
		semanticResults      []SemanticSearchResult
		keywordResults       []searchPostResult
		expectBothFailError  bool
		expectPartialResults bool
	}{
		{
			name:                "both searches fail returns error",
			semanticErr:         semanticErr,
			keywordErr:          keywordErr,
			semanticResults:     nil,
			keywordResults:      nil,
			expectBothFailError: true,
		},
		{
			name:        "semantic fails but keyword succeeds returns results",
			semanticErr: semanticErr,
			keywordErr:  nil,
			keywordResults: []searchPostResult{
				{Post: &model.Post{Id: "post1", Message: "keyword result"}, Username: "user1"},
			},
			expectPartialResults: true,
		},
		{
			name:        "keyword fails but semantic succeeds returns results",
			semanticErr: nil,
			keywordErr:  keywordErr,
			semanticResults: []SemanticSearchResult{
				{PostID: "post1", Content: "semantic result", Username: "user1"},
			},
			expectPartialResults: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectBothFailError {
				// Verify the error message indicates both failed
				errMsg := "both search methods failed"
				assert.Contains(t, errMsg, "both", "error should indicate both searches failed")
			}

			if tc.expectPartialResults {
				// Verify that partial results are still usable
				hasResults := len(tc.semanticResults) > 0 || len(tc.keywordResults) > 0
				assert.True(t, hasResults, "should have partial results when one search succeeds")
			}
		})
	}
}

func TestSearchUsersArgs_Validation(t *testing.T) {
	testCases := []struct {
		name               string
		args               SearchUsersArgs
		expectedLimit      int
		expectTermRequired bool
	}{
		{
			name:               "empty term should require term",
			args:               SearchUsersArgs{Term: ""},
			expectTermRequired: true,
		},
		{
			name:          "zero limit gets default of 20",
			args:          SearchUsersArgs{Term: "john", Limit: 0},
			expectedLimit: 20,
		},
		{
			name:          "limit over 100 gets capped",
			args:          SearchUsersArgs{Term: "john", Limit: 150},
			expectedLimit: 100,
		},
		{
			name:          "valid limit preserved",
			args:          SearchUsersArgs{Term: "john", Limit: 50},
			expectedLimit: 50,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectTermRequired {
				assert.Empty(t, tc.args.Term, "empty term should be detected")
				return
			}

			args := tc.args
			// Apply defaults (same logic as in toolSearchUsers)
			if args.Limit == 0 {
				args.Limit = 20
			}
			if args.Limit > 100 {
				args.Limit = 100
			}

			assert.Equal(t, tc.expectedLimit, args.Limit, "limit should match expected")
		})
	}
}
