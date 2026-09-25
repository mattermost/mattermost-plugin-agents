// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// openAICompatibleSSE writes the minimal streaming chat-completion response an
// OpenAI-compatible provider returns. bifrost issues streaming requests even
// for ChatCompletionNoStream, so a plain JSON body would not be understood.
func openAICompatibleSSE(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", content)
	fmt.Fprint(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// TestHandleTestService covers POST /admin/services/test. The cases that reach
// a provider run against a local OpenAI-compatible server, so a reachable
// provider and a rejecting one are both exercised for real rather than mocked
// at the bifrost boundary.
func TestHandleTestService(t *testing.T) {
	tests := []struct {
		name string

		// handler, when set, stands in for the provider and its URL is used as
		// the service APIURL.
		handler http.HandlerFunc

		// service is the config under test. When handler is set, APIURL is
		// overwritten with the test server's URL.
		service llm.ServiceConfig

		wantHTTPStatus int
		wantOK         bool
		wantErrorSet   bool
		wantErrorIs    string
	}{
		{
			name: "provider answers",
			handler: func(w http.ResponseWriter, r *http.Request) {
				openAICompatibleSSE(w, "OK")
			},
			service: llm.ServiceConfig{
				ID:           "svc-1",
				Type:         llm.ServiceTypeOpenAICompatible,
				APIKey:       "key",
				DefaultModel: "test-model",
			},
			wantHTTPStatus: http.StatusOK,
			wantOK:         true,
		},
		{
			name: "provider rejects the credentials",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
			},
			service: llm.ServiceConfig{
				ID:           "svc-1",
				Type:         llm.ServiceTypeOpenAICompatible,
				APIKey:       "wrong-key",
				DefaultModel: "test-model",
			},
			wantHTTPStatus: http.StatusOK,
			wantOK:         false,
			wantErrorSet:   true,
		},
		{
			name: "service has no default model",
			service: llm.ServiceConfig{
				ID:     "svc-1",
				Type:   llm.ServiceTypeOpenAICompatible,
				APIKey: "key",
				APIURL: "http://127.0.0.1:1",
			},
			wantHTTPStatus: http.StatusOK,
			wantOK:         false,
			wantErrorIs:    "This service has no default model set, so no request can be sent to the provider.",
		},
		{
			name: "load-test mock has no provider to reach",
			service: llm.ServiceConfig{
				ID:           "svc-1",
				Type:         llm.ServiceTypeLoadTestMock,
				DefaultModel: "mock-model",
			},
			wantHTTPStatus: http.StatusOK,
			wantOK:         false,
			wantErrorIs:    "The load-test mock does not contact a provider, so there is nothing to test.",
		},
		{
			name:           "missing service type",
			service:        llm.ServiceConfig{ID: "svc-1", APIKey: "key"},
			wantHTTPStatus: http.StatusBadRequest,
		},
		{
			name:           "unsupported service type",
			service:        llm.ServiceConfig{ID: "svc-1", Type: "not-a-provider", DefaultModel: "m"},
			wantHTTPStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, mockAPI, _ := setupAdminTestEnvironment(t)
			defer mockAPI.AssertExpectations(t)

			mockAPI.On("HasPermissionTo", "admin-user", model.PermissionManageSystem).Return(true).Maybe()
			mockAPI.On("LogError", mock.Anything).Return().Maybe()
			mockAPI.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

			service := tt.service
			if tt.handler != nil {
				server := httptest.NewServer(tt.handler)
				defer server.Close()
				service.APIURL = server.URL
			}

			raw, err := json.Marshal(TestServiceRequest{Service: service})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/admin/services/test", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mattermost-User-Id", "admin-user")

			recorder := httptest.NewRecorder()
			api.ServeHTTP(&plugin.Context{}, recorder, req)

			require.Equal(t, tt.wantHTTPStatus, recorder.Result().StatusCode)
			if tt.wantHTTPStatus != http.StatusOK {
				return
			}

			var resp TestServiceResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))

			require.Equal(t, tt.wantOK, resp.OK)
			if tt.wantErrorIs != "" {
				require.Equal(t, tt.wantErrorIs, resp.Error)
			}
			if tt.wantErrorSet {
				require.NotEmpty(t, resp.Error, "a failed probe must report why")
			}
			if tt.wantOK {
				require.Empty(t, resp.Error)
			}
		})
	}
}

// TestHandleTestServiceRequiresAdmin guards the route's authorization: the
// handler accepts credentials in its body, so a non-admin reaching it would be
// able to use the server as a request proxy.
func TestHandleTestServiceRequiresAdmin(t *testing.T) {
	api, mockAPI, _ := setupAdminTestEnvironment(t)
	defer mockAPI.AssertExpectations(t)

	mockAPI.On("HasPermissionTo", "regular-user", model.PermissionManageSystem).Return(false).Maybe()
	mockAPI.On("LogError", mock.Anything).Return().Maybe()
	mockAPI.On("LogError", mock.Anything, mock.Anything).Return().Maybe()

	raw, err := json.Marshal(TestServiceRequest{Service: llm.ServiceConfig{
		ID:           "svc-1",
		Type:         llm.ServiceTypeOpenAICompatible,
		APIKey:       "key",
		DefaultModel: "test-model",
	}})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/services/test", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mattermost-User-Id", "regular-user")

	recorder := httptest.NewRecorder()
	api.ServeHTTP(&plugin.Context{}, recorder, req)

	require.Equal(t, http.StatusForbidden, recorder.Result().StatusCode)
}
