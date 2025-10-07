// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

// ServerConfig interface defines common configuration methods for all server types
type ServerConfig interface {
	GetMMServerURL() string
	GetMMInternalServerURL() string
	GetDevMode() bool
}

// BaseConfig represents common configuration for all MCP server types
type BaseConfig struct {
	// Mattermost server URL (e.g., "https://mattermost.company.com")
	MMServerURL string `json:"mm_server_url"`

	// Internal Mattermost server URL for API communication (e.g., "http://localhost:8065")
	// If empty, MMServerURL will be used for internal communication
	MMInternalServerURL string `json:"mm_internal_server_url"`

	// Development mode enables additional tools for setting up test data
	DevMode bool `json:"dev_mode"`
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

// StdioConfig represents configuration for STDIO transport MCP server
type StdioConfig struct {
	BaseConfig

	// Personal Access Token for authentication
	PersonalAccessToken string `json:"personal_access_token"`
}

// HTTPConfig represents configuration for HTTP transport MCP server
type HTTPConfig struct {
	BaseConfig

	// HTTP server configuration
	HTTPPort     int    `json:"http_port"`      // Port for HTTP server (default: 8080)
	HTTPBindAddr string `json:"http_bind_addr"` // Bind address (default: "127.0.0.1" for security)
	SiteURL      string `json:"site_url"`       // Site URL for external access (optional)
}
