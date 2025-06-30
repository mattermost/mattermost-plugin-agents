// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomHeadersTransport(t *testing.T) {
	// Create a test server that captures request headers
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test","object":"chat.completion","choices":[{"message":{"content":"test response"}}]}`))
	}))
	defer server.Close()

	// Create custom headers
	customHeaders := map[string]string{
		"X-Custom-Header-1": "value1",
		"X-Custom-Header-2": "value2",
		"Authorization":     "Bearer custom-token", // This should override any existing auth
	}

	// Create a base HTTP client
	baseClient := &http.Client{}

	// Wrap it with custom headers
	wrappedClient := wrapHTTPClientWithCustomHeaders(baseClient, customHeaders)

	// Make a request
	req, err := http.NewRequest("POST", server.URL, strings.NewReader("test body"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := wrappedClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify the response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify custom headers were added
	assert.Equal(t, "value1", capturedHeaders.Get("X-Custom-Header-1"))
	assert.Equal(t, "value2", capturedHeaders.Get("X-Custom-Header-2"))
	assert.Equal(t, "Bearer custom-token", capturedHeaders.Get("Authorization"))
	assert.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
}

func TestCustomHeadersTransportNoHeaders(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create a base HTTP client
	baseClient := &http.Client{}

	// Wrap it with no custom headers
	wrappedClient := wrapHTTPClientWithCustomHeaders(baseClient, nil)

	// Should return the same client when no headers are provided
	assert.Equal(t, baseClient, wrappedClient)

	// Test with empty map too
	wrappedClient2 := wrapHTTPClientWithCustomHeaders(baseClient, map[string]string{})
	assert.Equal(t, baseClient, wrappedClient2)
}

func TestOpenAIConfigWithCustomHeaders(t *testing.T) {
	// Test server that captures headers
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)

		// Return a valid OpenAI response
		response := `{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "gpt-3.5-turbo",
			"choices": [
				{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Test response"
					},
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 20,
				"total_tokens": 30
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	}))
	defer server.Close()

	// Create config with custom headers
	config := Config{
		APIKey:       "test-api-key",
		APIURL:       server.URL,
		DefaultModel: "gpt-3.5-turbo",
		CustomHeaders: map[string]string{
			"X-Custom-Org":     "my-org",
			"X-Request-Source": "mattermost-ai",
		},
	}

	// Create OpenAI client
	httpClient := &http.Client{}
	openaiClient := NewCompatible(config, httpClient)

	// We can't easily test a full chat completion without more complex mocking,
	// but we can verify the client was created successfully with custom headers
	assert.NotNil(t, openaiClient)
	assert.Equal(t, config, openaiClient.config)
}
