// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRegistrationRequest(t *testing.T) {
	redirectURI := "https://example.com/callback"
	clientName := "Test Client"

	req := DefaultRegistrationRequest(redirectURI, clientName)

	assert.Equal(t, []string{redirectURI}, req.RedirectURIs)
	assert.Equal(t, "client_secret_basic", req.TokenEndpointAuthMethod)
	assert.Equal(t, []string{"authorization_code", "refresh_token"}, req.GrantTypes)
	assert.Equal(t, []string{"code"}, req.ResponseTypes)
	assert.Equal(t, clientName, req.ClientName)
	assert.Equal(t, "", req.Scope)
}

func TestRegisterClient_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and headers
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		// Verify request body
		var req RegistrationRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://example.com/callback"}, req.RedirectURIs)
		assert.Equal(t, "Test Client", req.ClientName)

		// Send success response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		response := RegistrationResponse{
			ClientID:     "client123",
			ClientSecret: "secret456",
			RedirectURIs: req.RedirectURIs,
			ClientName:   req.ClientName,
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	request := DefaultRegistrationRequest("https://example.com/callback", "Test Client")

	response, err := RegisterClient(context.Background(), http.DefaultClient, server.URL, request, "")
	require.NoError(t, err)
	assert.Equal(t, "client123", response.ClientID)
	assert.Equal(t, "secret456", response.ClientSecret)
	assert.Equal(t, []string{"https://example.com/callback"}, response.RedirectURIs)
	assert.Equal(t, "Test Client", response.ClientName)
}

func TestRegisterClient_WithInitialAccessToken(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authorization header
		assert.Equal(t, "Bearer initial_token_123", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		response := RegistrationResponse{
			ClientID:     "client123",
			ClientSecret: "secret456",
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	request := DefaultRegistrationRequest("https://example.com/callback", "Test Client")

	_, err := RegisterClient(context.Background(), http.DefaultClient, server.URL, request, "initial_token_123")
	require.NoError(t, err)
}

func TestRegisterClient_ErrorResponse(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)

		errorResp := RegistrationError{
			ErrorCode:        "invalid_redirect_uri",
			ErrorDescription: "The redirect URI is invalid",
		}
		_ = json.NewEncoder(w).Encode(errorResp)
	}))
	defer server.Close()

	request := DefaultRegistrationRequest("invalid-uri", "Test Client")

	_, err := RegisterClient(context.Background(), http.DefaultClient, server.URL, request, "")
	require.Error(t, err)

	var regErr *RegistrationError
	assert.ErrorAs(t, err, &regErr)
	assert.Equal(t, "invalid_redirect_uri", regErr.ErrorCode)
	assert.Equal(t, "The redirect URI is invalid", regErr.ErrorDescription)
	assert.Equal(t, http.StatusBadRequest, regErr.HTTPStatusCode)
}

func TestRegisterClient_MissingClientID(t *testing.T) {
	// Create mock server that returns response without client_id
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		// Send invalid response missing client_id
		response := map[string]string{
			"client_secret": "secret456",
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	request := DefaultRegistrationRequest("https://example.com/callback", "Test Client")

	_, err := RegisterClient(context.Background(), http.DefaultClient, server.URL, request, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server response missing required client_id")
}

func TestRegisterClient_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		request *RegistrationRequest
		wantErr string
	}{
		{
			name: "missing redirect URIs",
			request: &RegistrationRequest{
				ClientName: "Test Client",
			},
			wantErr: "redirect_uris is required",
		},
		{
			name: "invalid redirect URI",
			request: &RegistrationRequest{
				RedirectURIs: []string{"ht\ntp://invalid-url-with-newline"},
				ClientName:   "Test Client",
			},
			wantErr: "invalid redirect_uri",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a fake URL that won't actually be reached for validation errors
			_, err := RegisterClient(context.Background(), http.DefaultClient, "http://localhost:99999/register", tt.request, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestGetRegistrationEndpoint(t *testing.T) {
	// Create mock server for metadata endpoint
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/oauth-authorization-server", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		metadata := map[string]string{
			"registration_endpoint": serverURL + "/register",
		}
		_ = json.NewEncoder(w).Encode(metadata)
	}))
	defer server.Close()
	serverURL = server.URL

	endpoint, err := GetRegistrationEndpoint(context.Background(), http.DefaultClient, server.URL)
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/register", endpoint)
}

func TestGetRegistrationEndpoint_NotSupported(t *testing.T) {
	// Create mock server that doesn't support registration
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		metadata := map[string]string{
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
			// No registration_endpoint
		}
		_ = json.NewEncoder(w).Encode(metadata)
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := GetRegistrationEndpoint(context.Background(), http.DefaultClient, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server does not support dynamic client registration")
}

func TestDiscoverAndRegisterClient_Success(t *testing.T) {
	// Create mock server to handle both metadata and registration endpoints
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			// Handle metadata endpoint
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Accept"))

			w.Header().Set("Content-Type", "application/json")
			metadata := map[string]string{
				"registration_endpoint": serverURL + "/register",
			}
			_ = json.NewEncoder(w).Encode(metadata)

		case "/register":
			// Handle registration endpoint
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, "application/json", r.Header.Get("Accept"))

			// Verify request body
			var req RegistrationRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			assert.Equal(t, []string{"https://example.com/callback"}, req.RedirectURIs)
			assert.Equal(t, "Test Client", req.ClientName)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)

			response := RegistrationResponse{
				ClientID:     "client123",
				ClientSecret: "secret456",
				RedirectURIs: req.RedirectURIs,
				ClientName:   req.ClientName,
			}
			_ = json.NewEncoder(w).Encode(response)

		default:
			t.Errorf("Unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	response, err := DiscoverAndRegisterClient(
		context.Background(),
		http.DefaultClient,
		server.URL,
		"https://example.com/callback",
		"Test Client",
		"",
	)

	require.NoError(t, err)
	assert.Equal(t, "client123", response.ClientID)
	assert.Equal(t, "secret456", response.ClientSecret)
	assert.Equal(t, []string{"https://example.com/callback"}, response.RedirectURIs)
	assert.Equal(t, "Test Client", response.ClientName)
}

func TestDiscoverAndRegisterClient_MetadataDiscoveryFailure(t *testing.T) {
	// Create mock server that returns 404 for metadata endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := DiscoverAndRegisterClient(
		context.Background(),
		http.DefaultClient,
		server.URL,
		"https://example.com/callback",
		"Test Client",
		"",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to discover registration endpoint")
}

func TestDiscoverAndRegisterClient_RegistrationFailure(t *testing.T) {
	// Create mock server that succeeds for metadata but fails for registration
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			// Handle metadata endpoint (success)
			w.Header().Set("Content-Type", "application/json")
			metadata := map[string]string{
				"registration_endpoint": serverURL + "/register",
			}
			_ = json.NewEncoder(w).Encode(metadata)

		case "/register":
			// Handle registration endpoint (error)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)

			errorResp := RegistrationError{
				ErrorCode:        "invalid_client_metadata",
				ErrorDescription: "Client metadata is invalid",
			}
			_ = json.NewEncoder(w).Encode(errorResp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := DiscoverAndRegisterClient(
		context.Background(),
		http.DefaultClient,
		server.URL,
		"https://example.com/callback",
		"Test Client",
		"",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to register OAuth client")

	var regErr *RegistrationError
	assert.ErrorAs(t, err, &regErr)
	assert.Equal(t, "invalid_client_metadata", regErr.ErrorCode)
}

func TestDiscoverAndRegisterClient_WithInitialAccessToken(t *testing.T) {
	// Create mock server to verify initial access token is passed through
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			metadata := map[string]string{
				"registration_endpoint": serverURL + "/register",
			}
			_ = json.NewEncoder(w).Encode(metadata)

		case "/register":
			// Verify authorization header contains initial access token
			assert.Equal(t, "Bearer initial_token_123", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)

			response := RegistrationResponse{
				ClientID:     "client123",
				ClientSecret: "secret456",
			}
			_ = json.NewEncoder(w).Encode(response)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := DiscoverAndRegisterClient(
		context.Background(),
		http.DefaultClient,
		server.URL,
		"https://example.com/callback",
		"Test Client",
		"initial_token_123",
	)

	require.NoError(t, err)
}
