// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
)

// TestHTTPServerCreation tests basic HTTP server creation scenarios
func TestHTTPServerCreation(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	tests := []struct {
		name        string
		config      mcpserver.HTTPConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "ValidConfiguration",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: suite.serverURL,
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			expectError: false,
		},
		{
			name: "ValidConfigurationWithSiteURL",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: suite.serverURL,
					DevMode:     false,
				},
				HTTPPort:     8081,
				HTTPBindAddr: "0.0.0.0",
				SiteURL:      "https://example.com",
			},
			expectError: false,
		},
		{
			name: "EmptyServerURL",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			expectError: true,
			errorMsg:    "server URL cannot be empty",
		},
		{
			name: "InvalidPort",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: suite.serverURL,
					DevMode:     false,
				},
				HTTPPort:     0,
				HTTPBindAddr: "127.0.0.1",
			},
			expectError: true,
			errorMsg:    "HTTP port must be greater than 0",
		},
		{
			name: "EmptyBindAddress",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: suite.serverURL,
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "",
			},
			expectError: true,
			errorMsg:    "HTTP bind address cannot be empty",
		},
		{
			name: "BindToAllWithoutSiteURL",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: suite.serverURL,
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "0.0.0.0",
				SiteURL:      "",
			},
			expectError: true,
			errorMsg:    "site-url is required when http-bind-addr is 0.0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := mcpserver.NewHTTPServer(tt.config, suite.logger)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, server)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, server)
			}
		})
	}
}

// TestHTTPServerSecurity tests security features of the HTTP server
func TestHTTPServerSecurity(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     false,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
	}

	server, err := mcpserver.NewHTTPServer(config, suite.logger)
	require.NoError(t, err)
	require.NotNil(t, server)

	// Create a test server using the HTTP server's handler
	testServer := httptest.NewServer(server.GetTestHandler())
	defer testServer.Close()

	tests := []struct {
		name           string
		path           string
		method         string
		origin         string
		expectedStatus int
		checkHeaders   bool
	}{
		{
			name:           "ValidOriginRequest",
			path:           "/.well-known/oauth-protected-resource",
			method:         "GET",
			origin:         suite.serverURL,
			expectedStatus: http.StatusNotFound, // Will be 404 because SiteURL is not set
			checkHeaders:   true,
		},
		{
			name:           "InvalidOriginRequest",
			path:           "/.well-known/oauth-protected-resource",
			method:         "GET",
			origin:         "http://malicious-site.com",
			expectedStatus: http.StatusForbidden,
			checkHeaders:   false,
		},
		{
			name:           "NoOriginRequest",
			path:           "/.well-known/oauth-protected-resource",
			method:         "GET",
			origin:         "",
			expectedStatus: http.StatusNotFound, // Will be 404 because SiteURL is not set
			checkHeaders:   false,
		},
		{
			name:           "OptionsRequest",
			path:           "/mcp",
			method:         "OPTIONS",
			origin:         suite.serverURL,
			expectedStatus: http.StatusOK,
			checkHeaders:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, testServer.URL+tt.path, nil)
			require.NoError(t, err)

			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.checkHeaders {
				// For OAuth metadata endpoints, CORS is set to "*" by default
				if strings.Contains(tt.path, ".well-known/oauth") {
					assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
				} else {
					// Check security headers for other endpoints
					assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
					assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
					assert.Equal(t, "none", resp.Header.Get("X-Permitted-Cross-Domain-Policies"))
					assert.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
					assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'none'")

					// Check CORS headers if origin was provided
					if tt.origin != "" {
						assert.Equal(t, tt.origin, resp.Header.Get("Access-Control-Allow-Origin"))
						assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
					}
				}
			}
		})
	}
}

// TestOAuthMetadataEndpoints tests OAuth metadata endpoints
func TestOAuthMetadataEndpoints(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     false,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
		SiteURL:      "https://example.com",
	}

	server, err := mcpserver.NewHTTPServer(config, suite.logger)
	require.NoError(t, err)

	testServer := httptest.NewServer(server.GetTestHandler())
	defer testServer.Close()

	tests := []struct {
		name         string
		path         string
		expectedKeys []string
	}{
		{
			name: "ProtectedResourceMetadata",
			path: "/.well-known/oauth-protected-resource",
			expectedKeys: []string{
				"resource",
				"authorization_servers",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(testServer.URL + tt.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var metadata map[string]interface{}
			err = json.Unmarshal(body, &metadata)
			require.NoError(t, err)

			for _, key := range tt.expectedKeys {
				assert.Contains(t, metadata, key, "Missing key: %s", key)
			}
		})
	}
}

// TestMCPEndpoints tests MCP communication endpoints
func TestMCPEndpoints(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     false,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
	}

	server, err := mcpserver.NewHTTPServer(config, suite.logger)
	require.NoError(t, err)

	testServer := httptest.NewServer(server.GetTestHandler())
	defer testServer.Close()

	tests := []struct {
		name           string
		path           string
		method         string
		withAuth       bool
		expectedStatus int
	}{
		{
			name:           "MCPEndpointWithoutAuth",
			path:           "/mcp",
			method:         "GET",
			withAuth:       false,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "SSEEndpointWithoutAuth",
			path:           "/sse",
			method:         "GET",
			withAuth:       false,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "MessageEndpointWithoutAuth",
			path:           "/message",
			method:         "POST",
			withAuth:       false,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "UnknownEndpoint",
			path:           "/unknown",
			method:         "GET",
			withAuth:       false,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, testServer.URL+tt.path, nil)
			require.NoError(t, err)

			if tt.withAuth {
				// Add valid OAuth token (in real scenario)
				req.Header.Set("Authorization", "Bearer "+suite.adminToken)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedStatus == http.StatusUnauthorized {
				// Check WWW-Authenticate header
				authHeader := resp.Header.Get("WWW-Authenticate")
				assert.Contains(t, authHeader, "Bearer resource_metadata=")
			}
		})
	}
}

// TestOriginValidation tests origin validation logic
func TestOriginValidation(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	tests := []struct {
		name           string
		config         mcpserver.HTTPConfig
		requestOrigin  string
		expectedResult bool
	}{
		{
			name: "ValidMattermostOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "http://localhost:8065",
			expectedResult: true,
		},
		{
			name: "ValidSiteURLOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
				SiteURL:      "https://mattermost.example.com",
			},
			requestOrigin:  "https://mattermost.example.com",
			expectedResult: true,
		},
		{
			name: "LocalhostOriginWithLocalhostBinding",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "http://localhost:8080",
			expectedResult: true,
		},
		{
			name: "InvalidOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "http://malicious-site.com",
			expectedResult: false,
		},
		{
			name: "EmptyOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "",
			expectedResult: true, // No Origin header is allowed for non-browser requests
		},
		{
			name: "IPv6MattermostOriginWithDefaultPort",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://[::1]:80",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "http://[::1]:80",
			expectedResult: true,
		},
		{
			name: "IPv6MattermostOriginWithoutPort",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://[::1]",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
			},
			requestOrigin:  "http://[::1]",
			expectedResult: true,
		},
		{
			name: "IPv6SiteURLOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
				SiteURL:      "https://[2001:db8::1]:8443",
			},
			requestOrigin:  "https://[2001:db8::1]:8443",
			expectedResult: true,
		},
		{
			name: "IPv6SiteURLOriginWithDefaultPort",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "127.0.0.1",
				SiteURL:      "https://[2001:db8::1]:443",
			},
			requestOrigin:  "https://[2001:db8::1]", // Client sends without default port
			expectedResult: true,
		},
		{
			name: "IPv6LocalhostBindingOrigin",
			config: mcpserver.HTTPConfig{
				BaseConfig: mcpserver.BaseConfig{
					MMServerURL: "http://localhost:8065",
					DevMode:     false,
				},
				HTTPPort:     8080,
				HTTPBindAddr: "::1", // IPv6 localhost
			},
			requestOrigin:  "http://[::1]:8080",
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := mcpserver.NewHTTPServer(tt.config, suite.logger)
			require.NoError(t, err)

			testServer := httptest.NewServer(server.GetTestHandler())
			defer testServer.Close()

			req, err := http.NewRequest("GET", testServer.URL+"/.well-known/oauth-protected-resource", nil)
			require.NoError(t, err)

			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			if tt.expectedResult {
				assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
			} else {
				assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			}
		})
	}
}

// TestSSEServerIntegration tests SSE server integration
func TestSSEServerIntegration(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     false,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
	}

	server, err := mcpserver.NewHTTPServer(config, suite.logger)
	require.NoError(t, err)

	testServer := httptest.NewServer(server.GetTestHandler())
	defer testServer.Close()

	// Test SSE endpoint accessibility (without auth should return 401)
	req, err := http.NewRequest("GET", testServer.URL+"/sse", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer resource_metadata=")
}

// TestStreamableHTTPServerIntegration tests Streamable HTTP server integration
func TestStreamableHTTPServerIntegration(t *testing.T) {
	suite := SetupTestSuite(t)
	defer suite.TearDown()

	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: suite.serverURL,
			DevMode:     false,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
	}

	server, err := mcpserver.NewHTTPServer(config, suite.logger)
	require.NoError(t, err)

	testServer := httptest.NewServer(server.GetTestHandler())
	defer testServer.Close()

	// Test MCP HTTP endpoint accessibility (without auth should return 401)
	req, err := http.NewRequest("POST", testServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","params":{},"id":1}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer resource_metadata=")
}

// TestConfigurationMethods tests configuration getter methods
func TestConfigurationMethods(t *testing.T) {
	config := mcpserver.HTTPConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL: "http://localhost:8065",
			DevMode:     true,
		},
		HTTPPort:     8080,
		HTTPBindAddr: "127.0.0.1",
		SiteURL:      "https://example.com",
	}

	// Test configuration getter methods
	assert.Equal(t, "http://localhost:8065", config.GetMMServerURL())
	assert.Equal(t, "http://localhost:8065", config.GetMMInternalServerURL()) // Should fallback to MMServerURL
	assert.True(t, config.GetDevMode())
	assert.Equal(t, "https://example.com", config.SiteURL)
}
