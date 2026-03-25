// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/search"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleRunSearch(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		searchService  *search.Search
		setupMock      func(t *testing.T) *search.Search
		requestBody    SearchRequest
		expectedStatus int
		expectError    bool
	}{
		{
			name: "search fails - DM error, service enabled",
			setupMock: func(t *testing.T) *search.Search {
				mockClient := mmapimocks.NewMockClient(t)
				mockClient.On("DM", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("DM failed"))
				me := mocks.NewMockEmbeddingSearch(t)
				return search.New(func() embeddings.EmbeddingSearch { return me }, mockClient, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusInternalServerError,
			expectError:    true,
		},
		{
			name:          "search fails - service disabled",
			searchService: search.New(nil, nil, nil, nil, nil),
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:          "search fails - no service",
			searchService: nil,
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "search fails - empty query",
			setupMock: func(t *testing.T) *search.Search {
				me := mocks.NewMockEmbeddingSearch(t)
				return search.New(func() embeddings.EmbeddingSearch { return me }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Override the search service for this test
			if test.setupMock != nil {
				e.api.searchService = test.setupMock(t)
			} else {
				e.api.searchService = test.searchService
			}

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request body
			bodyBytes, err := json.Marshal(test.requestBody)
			require.NoError(t, err)

			// Create request
			request := httptest.NewRequest(http.MethodPost, "/search/run?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Add("Mattermost-User-ID", "userid")
			request.Header.Set("Content-Type", "application/json")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode)
		})
	}
}

func TestHandleSearchQuery(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		setupMock      func(t *testing.T) *search.Search
		searchService  *search.Search
		requestBody    SearchRequest
		expectedStatus int
		expectError    bool
	}{
		{
			name: "search query succeeds - service enabled",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				mockEmbedding.On("Search", mock.Anything, "test query", mock.Anything).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:          "search query fails - service disabled",
			searchService: search.New(nil, nil, nil, nil, nil),
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:          "search query fails - no service",
			searchService: nil,
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "search query succeeds - negative maxResults defaults to 5",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				// Verify that the limit is set to 5 (default) when negative value is passed
				mockEmbedding.On("Search", mock.Anything, "test query", mock.MatchedBy(func(opts embeddings.SearchOptions) bool {
					return opts.Limit == 5
				})).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: -10,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "search query succeeds - zero maxResults defaults to 5",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				// Verify that the limit is set to 5 (default) when zero value is passed
				mockEmbedding.On("Search", mock.Anything, "test query", mock.MatchedBy(func(opts embeddings.SearchOptions) bool {
					return opts.Limit == 5
				})).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 0,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "search query succeeds - very large maxResults capped to 100",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				// Verify that the limit is capped at 100 when a very large value is passed
				mockEmbedding.On("Search", mock.Anything, "test query", mock.MatchedBy(func(opts embeddings.SearchOptions) bool {
					return opts.Limit == 100
				})).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10000,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "search query succeeds - maxResults at boundary (100)",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				// Verify that the limit stays at 100 when exactly 100 is passed
				mockEmbedding.On("Search", mock.Anything, "test query", mock.MatchedBy(func(opts embeddings.SearchOptions) bool {
					return opts.Limit == 100
				})).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 100,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "search query succeeds - maxResults just above boundary (101) capped to 100",
			setupMock: func(t *testing.T) *search.Search {
				mockEmbedding := mocks.NewMockEmbeddingSearch(t)
				// Verify that the limit is capped at 100 when 101 is passed
				mockEmbedding.On("Search", mock.Anything, "test query", mock.MatchedBy(func(opts embeddings.SearchOptions) bool {
					return opts.Limit == 100
				})).Return([]embeddings.SearchResult{}, nil)
				return search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)
			},
			requestBody: SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 101,
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Override the search service for this test
			if test.setupMock != nil {
				e.api.searchService = test.setupMock(t)
			} else {
				e.api.searchService = test.searchService
			}

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request body
			bodyBytes, err := json.Marshal(test.requestBody)
			require.NoError(t, err)

			// Create request
			request := httptest.NewRequest(http.MethodPost, "/search?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Add("Mattermost-User-ID", "userid")
			request.Header.Set("Content-Type", "application/json")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode)
		})
	}
}

func TestHandleSearchQueryMalformedJSON(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
	}{
		{
			name:           "completely invalid JSON",
			requestBody:    "this is not json at all",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "truncated JSON",
			requestBody:    `{"query": "test`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "wrong type for query field",
			requestBody:    `{"query": 123, "teamId": "team123"}`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup search service (enabled)
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			e.api.searchService = search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request with malformed JSON body
			request := httptest.NewRequest(http.MethodPost, "/search?botUsername=test-bot", strings.NewReader(test.requestBody))
			request.Header.Add("Mattermost-User-ID", "userid")
			request.Header.Set("Content-Type", "application/json")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode, "Expected status %d for %s", test.expectedStatus, test.name)
		})
	}
}

func TestHandleSearchQueryMissingFields(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedStatus int
	}{
		{
			name:           "empty object - missing query",
			requestBody:    map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "missing query - only teamId and channelId",
			requestBody: map[string]interface{}{
				"teamId":    "team123",
				"channelId": "channel123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "empty query string",
			requestBody: map[string]interface{}{
				"query":     "",
				"teamId":    "team123",
				"channelId": "channel123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "whitespace-only query",
			requestBody: map[string]interface{}{
				"query":     "   ",
				"teamId":    "team123",
				"channelId": "channel123",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "valid query - missing optional fields is OK",
			requestBody: map[string]interface{}{
				"query": "test query",
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "query with only maxResults (missing teamId, channelId is OK)",
			requestBody: map[string]interface{}{
				"query":      "test query",
				"maxResults": 10,
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup search service (enabled)
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			if test.expectedStatus == http.StatusOK {
				mockEmbedding.On("Search", mock.Anything, mock.Anything, mock.Anything).Return([]embeddings.SearchResult{}, nil)
			}
			e.api.searchService = search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request body
			bodyBytes, err := json.Marshal(test.requestBody)
			require.NoError(t, err)

			// Create request
			request := httptest.NewRequest(http.MethodPost, "/search?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Add("Mattermost-User-ID", "userid")
			request.Header.Set("Content-Type", "application/json")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode, "Expected status %d for %s", test.expectedStatus, test.name)
		})
	}
}

func TestHandleSearchQueryMissingUserHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		headers        map[string]string
		expectedStatus int
	}{
		{
			name:           "missing Mattermost-User-Id header",
			headers:        map[string]string{},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "empty Mattermost-User-Id header",
			headers: map[string]string{
				"Mattermost-User-Id": "",
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "valid Mattermost-User-Id header",
			headers: map[string]string{
				"Mattermost-User-Id": "userid",
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup search service (enabled)
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			if test.expectedStatus == http.StatusOK {
				mockEmbedding.On("Search", mock.Anything, mock.Anything, mock.Anything).Return([]embeddings.SearchResult{}, nil)
			}
			e.api.searchService = search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create valid request body
			bodyBytes, err := json.Marshal(SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			})
			require.NoError(t, err)

			// Create request
			request := httptest.NewRequest(http.MethodPost, "/search?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Set("Content-Type", "application/json")

			// Add headers as specified in test
			for k, v := range test.headers {
				request.Header.Set(k, v)
			}

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode, "Expected status %d for %s", test.expectedStatus, test.name)
		})
	}
}

func TestHandleRunSearchMissingUserHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		headers        map[string]string
		expectedStatus int
	}{
		{
			name:           "missing Mattermost-User-Id header",
			headers:        map[string]string{},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "empty Mattermost-User-Id header",
			headers: map[string]string{
				"Mattermost-User-Id": "",
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup search service (enabled)
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			e.api.searchService = search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create valid request body
			bodyBytes, err := json.Marshal(SearchRequest{
				Query:      "test query",
				TeamID:     "team123",
				ChannelID:  "channel123",
				MaxResults: 10,
			})
			require.NoError(t, err)

			// Create request
			request := httptest.NewRequest(http.MethodPost, "/search/run?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Set("Content-Type", "application/json")

			// Add headers as specified in test
			for k, v := range test.headers {
				request.Header.Set(k, v)
			}

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode, "Expected status %d for %s", test.expectedStatus, test.name)
		})
	}
}

func TestHandleRunSearchMalformedJSON(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
	}{
		{
			name:           "completely invalid JSON",
			requestBody:    "this is not json at all",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "truncated JSON",
			requestBody:    `{"query": "test`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Setup search service (enabled)
			mockEmbedding := mocks.NewMockEmbeddingSearch(t)
			e.api.searchService = search.New(func() embeddings.EmbeddingSearch { return mockEmbedding }, nil, nil, nil, nil)

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request with malformed JSON body
			request := httptest.NewRequest(http.MethodPost, "/search/run?botUsername=test-bot", strings.NewReader(test.requestBody))
			request.Header.Add("Mattermost-User-ID", "userid")
			request.Header.Set("Content-Type", "application/json")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode, "Expected status %d for %s", test.expectedStatus, test.name)
		})
	}
}
