// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

// MCPEmbeddedServerConfig contains configuration for the embedded MCP server
type MCPEmbeddedServerConfig struct {
	Enabled bool `json:"enabled"`
}

// MCPConfig contains the configuration for the MCP servers
type MCPConfig struct {
	Enabled            bool                    `json:"enabled"`
	EnablePluginServer bool                    `json:"enablePluginServer"`
	Servers            []MCPServerConfig       `json:"servers"`
	EmbeddedServer     MCPEmbeddedServerConfig `json:"embeddedServer"`
	IdleTimeoutMinutes int                     `json:"idleTimeoutMinutes"`
}

// MCPServerConfig contains the configuration for a single MCP server
type MCPServerConfig struct {
	Name         string            `json:"name"`
	Enabled      bool              `json:"enabled"`
	BaseURL      string            `json:"baseURL"`
	Headers      map[string]string `json:"headers,omitempty"`
	ClientID     string            `json:"clientID,omitempty"`
	ClientSecret string            `json:"clientSecret,omitempty"`
}
