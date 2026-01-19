// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestIsDenylisted(t *testing.T) {
	t.Run("returns false for empty denylist", func(t *testing.T) {
		result := isDenylisted("https://example.com/page", []string{})
		require.False(t, result)
	})

	t.Run("returns true for exact domain match", func(t *testing.T) {
		denylist := []string{"example.com", "blocked.org"}
		require.True(t, isDenylisted("https://example.com/page", denylist))
		require.True(t, isDenylisted("https://blocked.org/something", denylist))
	})

	t.Run("returns true for subdomain match", func(t *testing.T) {
		denylist := []string{"example.com"}
		require.True(t, isDenylisted("https://www.example.com/page", denylist))
		require.True(t, isDenylisted("https://sub.domain.example.com/page", denylist))
	})

	t.Run("returns false for non-matching domains", func(t *testing.T) {
		denylist := []string{"example.com"}
		require.False(t, isDenylisted("https://different.com/page", denylist))
		require.False(t, isDenylisted("https://examplecom.net/page", denylist))
	})

	t.Run("handles case insensitivity", func(t *testing.T) {
		denylist := []string{"Example.COM"}
		require.True(t, isDenylisted("https://example.com/page", denylist))
		require.True(t, isDenylisted("https://EXAMPLE.COM/page", denylist))
	})

	t.Run("handles whitespace in denylist", func(t *testing.T) {
		denylist := []string{"  example.com  ", "blocked.org"}
		require.True(t, isDenylisted("https://example.com/page", denylist))
	})

	t.Run("handles invalid URLs gracefully", func(t *testing.T) {
		denylist := []string{"example.com"}
		require.False(t, isDenylisted("not a valid url", denylist))
	})
}

func TestWrapSourceContentWithContext(t *testing.T) {
	service := &webSearchService{}

	t.Run("includes citation context with matched result", func(t *testing.T) {
		content := "This is the fetched web page content."
		matchedResult := &WebSearchResult{
			Index:   2,
			Title:   "Example Page",
			URL:     "https://example.com/page",
			Snippet: "Example snippet",
		}

		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchContextKey: []WebSearchContextValue{
					{
						Query: "test query",
						Results: []WebSearchResult{
							{Index: 1, Title: "Result 1", URL: "https://example.com/1"},
							{Index: 2, Title: "Example Page", URL: "https://example.com/page"},
						},
					},
				},
			},
		}

		wrapped := service.wrapSourceContentWithContext(content, matchedResult, ctx)

		require.Contains(t, wrapped, "FETCHED WEB SOURCE CONTENT")
		require.Contains(t, wrapped, "[2] Example Page")
		require.Contains(t, wrapped, "AVAILABLE SEARCH RESULTS FOR CITATION")
		require.Contains(t, wrapped, "!!CITE#!!")
		require.Contains(t, wrapped, "!!CITE2!!")
		require.Contains(t, wrapped, content)
		require.Contains(t, wrapped, "SECURITY WARNING")
	})

	t.Run("handles nil matched result", func(t *testing.T) {
		content := "Content without matched result"
		wrapped := service.wrapSourceContentWithContext(content, nil, nil)

		require.Contains(t, wrapped, content)
		require.Contains(t, wrapped, "!!CITE#!!")
		require.Contains(t, wrapped, "SECURITY WARNING")
	})

	t.Run("handles nil context", func(t *testing.T) {
		content := "Content without context"
		matchedResult := &WebSearchResult{
			Index: 1,
			Title: "Test",
			URL:   "https://test.com",
		}
		wrapped := service.wrapSourceContentWithContext(content, matchedResult, nil)

		require.Contains(t, wrapped, content)
		require.Contains(t, wrapped, "[1] Test")
		require.Contains(t, wrapped, "!!CITE1!!")
	})
}

func TestBuildWebSearchAnnotations(t *testing.T) {
	results := []WebSearchResult{
		{
			Index:   1,
			Title:   "Example Title 1",
			URL:     "https://example.com/page1",
			Snippet: "This is snippet 1",
		},
		{
			Index:   2,
			Title:   "Example Title 2",
			URL:     "https://example.com/page2",
			Snippet: "This is snippet 2",
		},
	}

	t.Run("parses !!CITE!! format correctly and cleans text", func(t *testing.T) {
		message := "Here is some text !!CITE1!! and more text !!CITE2!! at the end."
		annotations, cleanedMessage := buildWebSearchAnnotationsAndCleanText(message, results)

		require.Len(t, annotations, 2)

		// Verify cleaned message has markers removed
		require.Equal(t, "Here is some text  and more text  at the end.", cleanedMessage)
		require.NotContains(t, cleanedMessage, "!!CITE")

		// First annotation - should have zero-width indices in cleaned message
		require.Equal(t, llm.AnnotationTypeURLCitation, annotations[0].Type)
		require.Equal(t, 1, annotations[0].Index)
		require.Equal(t, "https://example.com/page1", annotations[0].URL)
		require.Equal(t, "Example Title 1", annotations[0].Title)
		require.Equal(t, "This is snippet 1", annotations[0].CitedText)
		require.Equal(t, annotations[0].StartIndex, annotations[0].EndIndex, "Should be zero-width")

		// Second annotation
		require.Equal(t, llm.AnnotationTypeURLCitation, annotations[1].Type)
		require.Equal(t, 2, annotations[1].Index)
		require.Equal(t, "https://example.com/page2", annotations[1].URL)
		require.Equal(t, "Example Title 2", annotations[1].Title)
		require.Equal(t, annotations[1].StartIndex, annotations[1].EndIndex, "Should be zero-width")
	})

	t.Run("ignores text without markers", func(t *testing.T) {
		message := "This is plain text without any citations."
		annotations := buildWebSearchAnnotations(message, results)

		require.Empty(t, annotations)
	})

	t.Run("ignores malformed markers", func(t *testing.T) {
		message := "This has !!CITE without closing, and [1] old format, and !!CITE!! without number."
		annotations := buildWebSearchAnnotations(message, results)

		require.Empty(t, annotations)
	})

	t.Run("handles multiple citations of same source", func(t *testing.T) {
		message := "First mention !!CITE1!! and second mention !!CITE1!! again."
		annotations := buildWebSearchAnnotations(message, results)

		require.Len(t, annotations, 2)
		require.Equal(t, 1, annotations[0].Index)
		require.Equal(t, 1, annotations[1].Index)
	})

	t.Run("handles UTF-8 characters correctly", func(t *testing.T) {
		message := "Unicode text 你好 !!CITE1!! más text 🎉 !!CITE2!! end."
		annotations := buildWebSearchAnnotations(message, results)

		require.Len(t, annotations, 2)
		require.Greater(t, annotations[0].StartIndex, 0)
		require.Greater(t, annotations[1].StartIndex, annotations[0].EndIndex)
	})
}

// mockLogger implements WebSearchLog for testing
type mockLogger struct{}

func (m *mockLogger) Debug(message string, keyValuePairs ...any) {}
func (m *mockLogger) Info(message string, keyValuePairs ...any)  {}
func (m *mockLogger) Warn(message string, keyValuePairs ...any)  {}
func (m *mockLogger) Error(message string, keyValuePairs ...any) {}

func TestWebSearchTracking(t *testing.T) {
	t.Run("tracks executed queries", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: make(map[string]interface{}),
		}

		// Simulate first search
		ctx.Parameters[WebSearchExecutedQueriesKey] = []string{}
		ctx.Parameters[WebSearchCountKey] = 0

		// After first search
		ctx.Parameters[WebSearchExecutedQueriesKey] = []string{"mattermost features"}
		ctx.Parameters[WebSearchCountKey] = 1

		queries := ctx.Parameters[WebSearchExecutedQueriesKey].([]string)
		count := ctx.Parameters[WebSearchCountKey].(int)

		require.Len(t, queries, 1)
		require.Equal(t, "mattermost features", queries[0])
		require.Equal(t, 1, count)
	})

	t.Run("prevents duplicate queries", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchExecutedQueriesKey: []string{"test query"},
				WebSearchCountKey:           1,
			},
		}

		executedQueries := ctx.Parameters[WebSearchExecutedQueriesKey].([]string)
		normalizedQuery := "test query"

		isDuplicate := false
		for _, existingQuery := range executedQueries {
			if existingQuery == normalizedQuery {
				isDuplicate = true
				break
			}
		}

		require.True(t, isDuplicate, "Should detect duplicate query")
	})

	t.Run("detects duplicate with different case", func(t *testing.T) {
		executedQueries := []string{"Test Query"}
		testQuery := "test query"

		// Normalize both for comparison (as done in resolve method)
		isDuplicate := false
		for _, existingQuery := range executedQueries {
			if strings.EqualFold(existingQuery, testQuery) {
				isDuplicate = true
				break
			}
		}

		require.True(t, isDuplicate, "Should detect duplicate with case insensitivity")
	})

	t.Run("enforces max search limit", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchExecutedQueriesKey: []string{"query1", "query2", "query3"},
				WebSearchCountKey:           3,
			},
		}

		count := ctx.Parameters[WebSearchCountKey].(int)
		require.GreaterOrEqual(t, count, maxWebSearches, "Should have reached max searches")
	})

	t.Run("tracks count correctly across multiple searches", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: make(map[string]interface{}),
		}

		// Start with empty tracking
		ctx.Parameters[WebSearchExecutedQueriesKey] = []string{}
		ctx.Parameters[WebSearchCountKey] = 0

		// Simulate 3 searches
		for i := 1; i <= 3; i++ {
			queries := ctx.Parameters[WebSearchExecutedQueriesKey].([]string)
			queries = append(queries, "query"+string(rune(i)))
			ctx.Parameters[WebSearchExecutedQueriesKey] = queries
			ctx.Parameters[WebSearchCountKey] = i
		}

		finalCount := ctx.Parameters[WebSearchCountKey].(int)
		finalQueries := ctx.Parameters[WebSearchExecutedQueriesKey].([]string)

		require.Equal(t, 3, finalCount)
		require.Len(t, finalQueries, 3)
	})
}

func TestWebSearchContextPersistence(t *testing.T) {
	t.Run("preserves web search context keys", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchContextKey:         []WebSearchContextValue{},
				WebSearchAllowedURLsKey:     []string{"https://example.com"},
				WebSearchExecutedQueriesKey: []string{"test query"},
				WebSearchCountKey:           1,
			},
		}

		// Verify all keys are present
		_, hasContext := ctx.Parameters[WebSearchContextKey]
		_, hasURLs := ctx.Parameters[WebSearchAllowedURLsKey]
		_, hasQueries := ctx.Parameters[WebSearchExecutedQueriesKey]
		_, hasCount := ctx.Parameters[WebSearchCountKey]

		require.True(t, hasContext, "Should have context key")
		require.True(t, hasURLs, "Should have URLs key")
		require.True(t, hasQueries, "Should have queries key")
		require.True(t, hasCount, "Should have count key")
	})

	t.Run("handles empty executed queries", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchExecutedQueriesKey: []string{},
				WebSearchCountKey:           0,
			},
		}

		queries := ctx.Parameters[WebSearchExecutedQueriesKey].([]string)
		count := ctx.Parameters[WebSearchCountKey].(int)

		require.Empty(t, queries)
		require.Equal(t, 0, count)
	})

	t.Run("handles int count correctly", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchCountKey: 2,
			},
		}

		count, ok := ctx.Parameters[WebSearchCountKey].(int)
		require.True(t, ok, "Should be able to type assert to int")
		require.Equal(t, 2, count)
	})

	t.Run("handles float64 count from JSON unmarshaling", func(t *testing.T) {
		// Simulate what happens when JSON unmarshals a number
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchCountKey: float64(2),
			},
		}

		// Convert float64 to int (as done in unmarshalWebSearchContext)
		var count int
		if raw, ok := ctx.Parameters[WebSearchCountKey]; ok {
			switch v := raw.(type) {
			case float64:
				count = int(v)
			case int:
				count = v
			}
		}

		require.Equal(t, 2, count)
	})
}

func TestWebSearchService(t *testing.T) {
	t.Run("returns nil tool when not configured", func(t *testing.T) {
		cfgGetter := func() *config.Config {
			return &config.Config{
				WebSearch: config.WebSearchConfig{
					Enabled: false,
				},
			}
		}

		service := NewWebSearchService(cfgGetter, &mockLogger{}, http.DefaultClient)
		tool := service.Tool()

		require.Nil(t, tool, "Should return nil when web search is disabled")
	})

	t.Run("returns tool when properly configured", func(t *testing.T) {
		cfgGetter := func() *config.Config {
			return &config.Config{
				WebSearch: config.WebSearchConfig{
					Enabled:  true,
					Provider: "google",
					Google: config.WebSearchGoogleConfig{
						APIKey:         "test-key",
						SearchEngineID: "test-engine-id",
					},
				},
			}
		}

		service := NewWebSearchService(cfgGetter, &mockLogger{}, http.DefaultClient)
		tool := service.Tool()

		require.NotNil(t, tool, "Should return tool when properly configured")
		require.Equal(t, "WebSearch", tool.Name)
		require.Contains(t, tool.Description, "limited to 3 searches")
		require.Contains(t, tool.Description, "DO NOT repeat a search query")
	})
}

func TestWebSearchResetBehavior(t *testing.T) {
	t.Run("search count resets for new request cycle", func(t *testing.T) {
		// Simulate first request cycle with 3 searches
		firstCycleCtx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchContextKey:         []WebSearchContextValue{{Query: "first query", Results: []WebSearchResult{}}},
				WebSearchAllowedURLsKey:     []string{"https://example.com"},
				WebSearchExecutedQueriesKey: []string{"query1", "query2", "query3"},
				WebSearchCountKey:           3,
			},
		}

		// Verify first cycle reached limit
		count := firstCycleCtx.Parameters[WebSearchCountKey].(int)
		require.Equal(t, 3, count)

		// Simulate new request cycle - reset tracking but keep search results
		secondCycleCtx := &llm.Context{
			Parameters: map[string]interface{}{
				// Keep previous results for context
				WebSearchContextKey:     firstCycleCtx.Parameters[WebSearchContextKey],
				WebSearchAllowedURLsKey: firstCycleCtx.Parameters[WebSearchAllowedURLsKey],
				// Reset tracking
				WebSearchCountKey:           0,
				WebSearchExecutedQueriesKey: []string{},
			},
		}

		// Verify tracking was reset
		newCount := secondCycleCtx.Parameters[WebSearchCountKey].(int)
		newQueries := secondCycleCtx.Parameters[WebSearchExecutedQueriesKey].([]string)
		require.Equal(t, 0, newCount, "Count should reset to 0 for new request")
		require.Empty(t, newQueries, "Queries should reset to empty for new request")

		// Verify previous search results are still available
		results := secondCycleCtx.Parameters[WebSearchContextKey].([]WebSearchContextValue)
		require.Len(t, results, 1, "Previous search results should be preserved")
	})

	t.Run("allows same query in new request cycle", func(t *testing.T) {
		// First cycle executes "kubernetes features"
		firstCycleCtx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchExecutedQueriesKey: []string{"kubernetes features"},
				WebSearchCountKey:           1,
			},
		}

		queries := firstCycleCtx.Parameters[WebSearchExecutedQueriesKey].([]string)
		require.Contains(t, queries, "kubernetes features")

		// New request cycle - same query should be allowed
		secondCycleCtx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchExecutedQueriesKey: []string{}, // Reset
				WebSearchCountKey:           0,          // Reset
			},
		}

		// Simulate searching for the same query again
		secondCycleCtx.Parameters[WebSearchExecutedQueriesKey] = []string{"kubernetes features"}
		secondCycleCtx.Parameters[WebSearchCountKey] = 1

		newQueries := secondCycleCtx.Parameters[WebSearchExecutedQueriesKey].([]string)
		require.Contains(t, newQueries, "kubernetes features", "Same query should be allowed in new cycle")
	})

	t.Run("preserves search results across cycles", func(t *testing.T) {
		// Build up search results across multiple request cycles
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchContextKey: []WebSearchContextValue{
					{Query: "first question", Results: []WebSearchResult{{Index: 1, Title: "Result 1"}}},
				},
				WebSearchCountKey:           0,
				WebSearchExecutedQueriesKey: []string{},
			},
		}

		// Add more results in a new cycle
		existingResults := ctx.Parameters[WebSearchContextKey].([]WebSearchContextValue)
		existingResults = append(existingResults, WebSearchContextValue{
			Query:   "second question",
			Results: []WebSearchResult{{Index: 2, Title: "Result 2"}},
		})
		ctx.Parameters[WebSearchContextKey] = existingResults

		// Verify both results are available
		allResults := ctx.Parameters[WebSearchContextKey].([]WebSearchContextValue)
		require.Len(t, allResults, 2, "Should preserve results from both cycles")
		require.Equal(t, "first question", allResults[0].Query)
		require.Equal(t, "second question", allResults[1].Query)
	})
}

type mockTransport struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

func TestWebSearchSourceWhitelist(t *testing.T) {
	// Setup a service with a mocked HTTP client
	mockClient := &http.Client{
		Transport: &mockTransport{
			RoundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString("<html><body>Mock Content</body></html>")),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	service := &webSearchService{
		httpClient: mockClient,
		logger:     &mockLogger{},
		cfgGetter: func() *config.Config {
			return &config.Config{
				WebSearch: config.WebSearchConfig{
					Enabled:        true,
					Provider:       "google",
					DomainDenylist: []string{},
				},
			}
		},
	}

	mockBot := bots.NewBot(
		llm.BotConfig{Name: "mockbot"},
		llm.ServiceConfig{Type: "mock"},
		&model.Bot{Username: "mockbot"},
		&mockLanguageModel{},
	)

	t.Run("allows whitelisted URL", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchAllowedURLsKey: []string{"https://allowed.com/page"},
			},
		}

		argsGetter := func(v any) error {
			if args, ok := v.(*WebSearchSourceArgs); ok {
				args.URL = "https://allowed.com/page"
				return nil
			}
			return nil
		}

		// Should succeed (return content)
		resp, err := service.resolveSource(mockBot, ctx, argsGetter)
		require.NoError(t, err)
		require.Contains(t, resp, "Summarized content")
	})

	t.Run("rejects url not in whitelist", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{
				WebSearchAllowedURLsKey: []string{"https://allowed.com/page"},
			},
		}

		argsGetter := func(v any) error {
			if args, ok := v.(*WebSearchSourceArgs); ok {
				args.URL = "https://evil.com/script"
				return nil
			}
			return nil
		}

		resp, err := service.resolveSource(mockBot, ctx, argsGetter)
		require.Error(t, err)
		require.Equal(t, "url not in whitelist", err.Error())
		require.Contains(t, resp, "you can only fetch URLs that were returned from web search results")
	})

	t.Run("rejects when no whitelist exists", func(t *testing.T) {
		ctx := &llm.Context{
			Parameters: map[string]interface{}{},
		}

		argsGetter := func(v any) error {
			if args, ok := v.(*WebSearchSourceArgs); ok {
				args.URL = "https://example.com"
				return nil
			}
			return nil
		}

		resp, err := service.resolveSource(mockBot, ctx, argsGetter)
		require.Error(t, err)
		require.Equal(t, "no whitelist in context", err.Error())
		require.Contains(t, resp, "you can only fetch URLs that were returned from web search results")
	})
}

type mockLanguageModel struct{}

func (m *mockLanguageModel) ChatCompletion(conversation llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return nil, nil
}
func (m *mockLanguageModel) ChatCompletionNoStream(conversation llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	return "Summarized content", nil
}
func (m *mockLanguageModel) CountTokens(text string) int { return 0 }
func (m *mockLanguageModel) InputTokenLimit() int        { return 1000 }
