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

// setupAdminTestEnvironment creates a test environment for admin endpoint testing
func setupAdminTestEnvironment(t *testing.T) (*API, *plugintest.API) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	cfg := &testConfigImpl{}
	noopMetrics := &metrics.NoopMetrics{}

	api := New(nil, nil, nil, nil, nil, client, noopMetrics, nil, cfg, nil, nil, nil, nil, nil, nil, &mockMCPClientManager{}, nil, nil, nil)

	return api, mockAPI
}

func TestHandleGetJobStatusIncludesStale(t *testing.T) {
	tests := []struct {
		name           string
		indexerNil     bool
		jobStatus      *indexer.JobStatus
		expectedStatus int
		expectedStale  bool
	}{
		{
			name:           "returns 404 when indexer is nil",
			indexerNil:     true,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:       "running job with recent heartbeat is not stale",
			indexerNil: false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-5 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  false,
		},
		{
			name:       "running job with old heartbeat is stale",
			indexerNil: false,
			jobStatus: &indexer.JobStatus{
				Status:        indexer.JobStatusRunning,
				LastUpdatedAt: time.Now().Add(-45 * time.Minute),
			},
			expectedStatus: http.StatusOK,
			expectedStale:  true,
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
					jobStatus: tt.jobStatus,
				}
				api.indexerService = createMockIndexer(t, mockIndexer)
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/reindex/status", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedStatus == http.StatusOK {
				var response indexer.JobStatus
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Equal(t, tt.expectedStale, response.IsStale)
			}
		})
	}
}

func TestHandleIndexHealthCheck(t *testing.T) {
	tests := []struct {
		name                 string
		indexerNil           bool
		getSearchInitError   func() string
		expectedStatus       int
		expectedResultStatus string
		expectedError        string
	}{
		{
			name:                 "returns 200 with not_configured when indexer is nil",
			indexerNil:           true,
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "not_configured",
		},
		{
			name:       "returns 200 with init_error when indexer is nil and init error exists",
			indexerNil: true,
			getSearchInitError: func() string {
				return "failed to connect to database"
			},
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "init_error",
			expectedError:        "failed to connect to database",
		},
		{
			name:       "returns 200 with not_configured when init error is empty string",
			indexerNil: true,
			getSearchInitError: func() string {
				return ""
			},
			expectedStatus:       http.StatusOK,
			expectedResultStatus: "not_configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()

			if tt.getSearchInitError != nil {
				api.getSearchInitError = tt.getSearchInitError
			}

			req := httptest.NewRequest(http.MethodGet, "/admin/reindex/health-check", nil)
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			resp := recorder.Result()
			require.Equal(t, tt.expectedStatus, resp.StatusCode)

			var result indexer.HealthCheckResult
			err := json.NewDecoder(resp.Body).Decode(&result)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResultStatus, result.Status)
			if tt.expectedError != "" {
				require.Equal(t, tt.expectedError, result.Error)
			}
			// Not configured health checks should report model as compatible
			if tt.expectedResultStatus == "not_configured" {
				require.True(t, result.ModelCompatible)
			}
		})
	}
}

// notFoundError simulates the "not found" error that the indexer checks for
type notFoundError struct{}

func (e notFoundError) Error() string {
	return "not found"
}

// mockIndexerService holds the mock configuration for creating test indexers
type mockIndexerService struct {
	jobStatus *indexer.JobStatus
}

// createMockIndexer creates a real indexer.Indexer with mocked dependencies
func createMockIndexer(t *testing.T, mockService *mockIndexerService) *indexer.Indexer {
	t.Helper()

	mockMutexAPI := &plugintest.API{}
	mockClient := mocks.NewMockClient(t)

	// Setup mock for GetJobStatus - always handle the ReindexJobKey
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

	mockMutexAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockMutexAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	return indexer.New(nil, nil, mockClient, nil, nil, mockMutexAPI)
}
