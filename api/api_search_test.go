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
