// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// ProtectedResourceMetadata represents OAuth 2.0 Protected Resource Metadata (RFC 9728)
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`                        // Required: The protected resource's resource identifier URL
	AuthorizationServers []string `json:"authorization_servers,omitempty"` // Optional: Authorization servers
	ScopesSupported      []string `json:"scopes_supported,omitempty"`      // Recommended: OAuth scopes
	ResourceName         string   `json:"resource_name,omitempty"`         // Recommended: Human-readable name
}

// CreateOAuthMetadataHandler creates an HTTP handler for OAuth 2.0 Protected Resource Metadata (RFC 9728)
func CreateOAuthMetadataHandler(resourceURL, authServerURL, resourceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handleOAuthMetadataCommon(w, r, resourceURL, authServerURL, resourceName)
	}
}

// handleOAuthMetadataCommon is the shared implementation for OAuth metadata endpoints
func handleOAuthMetadataCommon(w http.ResponseWriter, r *http.Request, resourceURL, authServerURL, resourceName string) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Return 404 if resource URL is not configured
	if resourceURL == "" {
		http.NotFound(w, r)
		return
	}

	// Ensure resource URL is RFC 9728 compliant (HTTPS, no query/fragment)
	if u, err := url.Parse(resourceURL); err == nil {
		u.RawQuery = ""
		u.Fragment = ""
		resourceURL = u.String()
	}

	// Create protected resource metadata per RFC 9728
	metadata := ProtectedResourceMetadata{
		Resource: resourceURL,
		AuthorizationServers: []string{
			authServerURL,
		},
		ScopesSupported: []string{
			"user",
		},
		ResourceName: resourceName,
	}

	// Set required headers per RFC 9728
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour

	// Marshal and write the JSON response
	jsonBytes, err := json.Marshal(metadata)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonBytes)
}

// handleProtectedResourceMetadata handles OAuth 2.0 Protected Resource Metadata requests (RFC 9728)
func (s *MattermostHTTPMCPServer) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resourceURL := s.config.SiteURL
	authServerURL := s.config.GetMMServerURL()
	handleOAuthMetadataCommon(w, r, resourceURL, authServerURL, "Mattermost MCP Server")
	s.logger.Debug("Protected resource metadata requested", "remote_addr", r.RemoteAddr)
}
