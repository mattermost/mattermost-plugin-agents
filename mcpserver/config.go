// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

// BaseConfig represents common configuration for all MCP server types
type BaseConfig struct {
	// Mattermost server URL (e.g., "https://mattermost.company.com")
	MMServerURL string `json:"mm_server_url"`

	// Internal Mattermost server URL for API communication (e.g., "http://localhost:8065")
	// If empty, MMServerURL will be used for internal communication
	MMInternalServerURL string `json:"mm_internal_server_url"`

	// Development mode enables additional tools for setting up test data
	DevMode bool `json:"dev_mode"`

	// TrackAIGenerated controls whether to add ai_generated_by props to posts
	// For embedded servers: uses Bot UserID from metadata
	// For external servers: uses authenticated user's ID
	// Default: true for embedded, true for HTTP, false for stdio
	TrackAIGenerated *bool `json:"track_ai_generated,omitempty"`
}

// GetMMServerURL returns the Mattermost server URL
func (c BaseConfig) GetMMServerURL() string {
	return c.MMServerURL
}

// GetMMInternalServerURL returns the internal Mattermost server URL for API communication
// If not set, falls back to the external server URL for backward compatibility
func (c BaseConfig) GetMMInternalServerURL() string {
	if c.MMInternalServerURL != "" {
		return c.MMInternalServerURL
	}
	return c.MMServerURL
}

// GetDevMode returns the development mode setting
func (c BaseConfig) GetDevMode() bool {
	return c.DevMode
}

// GetTrackAIGenerated returns whether to track AI-generated content
// Defaults to true (always track) if not explicitly set
func (c BaseConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return true // Default to tracking
	}
	return *c.TrackAIGenerated
}

// StdioConfig represents configuration for STDIO transport MCP server
type StdioConfig struct {
	BaseConfig

	// Personal Access Token for authentication
	PersonalAccessToken string `json:"personal_access_token"`
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For stdio, defaults to false (disabled) unless explicitly enabled
func (c StdioConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return false // Default to NOT tracking for stdio
	}
	return *c.TrackAIGenerated
}

// HTTPConfig represents configuration for HTTP transport MCP server
type HTTPConfig struct {
	BaseConfig

	// HTTP server configuration
	HTTPPort     int    `json:"http_port"`      // Port for HTTP server (default: 8080)
	HTTPBindAddr string `json:"http_bind_addr"` // Bind address (default: "127.0.0.1" for security)
	SiteURL      string `json:"site_url"`       // Site URL for external access (optional)
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For HTTP, defaults to true (enabled) unless explicitly disabled
func (c HTTPConfig) GetTrackAIGenerated() bool {
	if c.TrackAIGenerated == nil {
		return true // Default to tracking for HTTP
	}
	return *c.TrackAIGenerated
}

// InMemoryConfig represents configuration for in-memory transport MCP server
// Used for embedded MCP servers that run within the same process as the plugin
type InMemoryConfig struct {
	BaseConfig
	// No additional configuration needed for in-memory transport
	// Authentication is handled through session tokens passed via context
}

// GetTrackAIGenerated returns whether to track AI-generated content
// For embedded/in-memory servers, always returns true (always track)
func (c InMemoryConfig) GetTrackAIGenerated() bool {
	// Always track for embedded servers, regardless of config
	return true
}
