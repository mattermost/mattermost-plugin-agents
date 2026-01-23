// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/indexer"
	"github.com/mattermost/mattermost-plugin-ai/metrics"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockIndexerService holds the mock configuration for creating test indexers
type mockIndexerService struct {
	isStale   bool
	jobStatus *indexer.JobStatus
}

// setupAdminTestEnvironment creates a test environment for admin endpoint testing
func setupAdminTestEnvironment(t *testing.T) (*API, *plugintest.API) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	cfg := &testConfigImpl{}
	noopMetrics := &metrics.NoopMetrics{}

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil)

	return api, mockAPI
}

func TestHandleGetStaleJobStatus(t *testing.T) {
	tests := []struct {
		name           string
		indexerNil     bool
		isStale        bool
		jobStatus      *indexer.JobStatus
		expectedStatus int
		expectedStale  bool
	}{
		{
			name:           "returns 400 when indexer not configured",
			indexerNil:     true,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "returns 404 when no job exists",
			indexerNil:     false,
			jobStatus:      nil,
			expectedStatus: http.StatusNotFound,
			expectedStale:  false,
		},
		{
			name:       "returns stale status correctly when job is stale",
			indexerNil: false,
			isStale:    true,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				NodeID:        "test-node",
				LastUpdatedAt: time.Now().Add(-45 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  true,
		},
		{
			name:       "returns stale status correctly when job is not stale",
			indexerNil: false,
			isStale:    false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				NodeID:        "test-node",
				LastUpdatedAt: time.Now().Add(-5 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			if !tt.indexerNil {
				mockIndexer := &mockIndexerService{
					isStale:   tt.isStale,
					jobStatus: tt.jobStatus,
				}
				api.indexerService = createMockIndexer(t, mockIndexer)
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/reindex/stale", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Equal(t, tt.expectedStale, response["stale"])
			}
		})
	}
}

func TestHandleResetStaleJob(t *testing.T) {
	tests := []struct {
		name           string
		indexerNil     bool
		isStale        bool
		jobStatus      *indexer.JobStatus
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "returns 400 when indexer not configured",
			indexerNil:     true,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "returns 404 when no job exists",
			indexerNil:     false,
			jobStatus:      nil,
			expectedStatus: http.StatusNotFound,
			expectedError:  "no job found",
		},
		{
			name:       "returns 400 when job is not stale",
			indexerNil: false,
			isStale:    false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-5 * time.Minute),
				NodeID:        "test-node",
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "job is not stale",
		},
		{
			name:    "successfully resets stale job",
			isStale: true,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-45 * time.Minute),
				NodeID:        "test-node",
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			if !tt.indexerNil {
				mockIndexer := &mockIndexerService{
					isStale:   tt.isStale,
					jobStatus: tt.jobStatus,
				}
				api.indexerService = createMockIndexer(t, mockIndexer)
			}

			req := httptest.NewRequest(http.MethodPost, "/admin/reindex/reset-stale", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedError != "" {
				var response map[string]string
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Contains(t, response["error"], tt.expectedError)
			}
		})
	}
}

// notFoundError simulates the "not found" error that the indexer checks for
type notFoundError struct{}

func (e notFoundError) Error() string {
	return "not found"
}

// createMockIndexer creates a real indexer.Indexer with mocked dependencies
// Since we can't easily mock the Indexer interface (it's a struct, not interface),
// we use a helper to create a minimal indexer that we can control through mocks
func createMockIndexer(t *testing.T, mockService *mockIndexerService) *indexer.Indexer {
	t.Helper()

	mockMutexAPI := &plugintest.API{}
	mockClient := mocks.NewMockClient(t)

	// Setup mock for IsJobStale / GetJobStatus - always handle the ReindexJobKey
	if mockService.jobStatus == nil {
		// No job exists - return "not found" error
		mockClient.On("KVGet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(notFoundError{}).Maybe()
	} else {
		// Job exists - populate the status
		mockClient.On("KVGet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Run(func(args mock.Arguments) {
				status := args.Get(1).(*indexer.JobStatus)
				*status = *mockService.jobStatus
			}).
			Return(nil).Maybe()
	}

	// Setup mock for ResetStaleJob KVSet
	if mockService.jobStatus != nil && mockService.isStale {
		mockClient.On("KVSet", indexer.ReindexJobKey, mock.AnythingOfType("*indexer.JobStatus")).
			Return(nil).Maybe()
	}

	// Setup mutex mocks for ResetStaleJob
	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	return indexer.New(nil, nil, mockClient, nil, nil, mockMutexAPI)
}
