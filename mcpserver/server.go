// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostMCPServer provides a high-level interface for creating an MCP server
// with Mattermost-specific tools and authentication
type MattermostMCPServer struct {
	mcpServer    *mcp.Server
	authProvider auth.AuthenticationProvider
	logger       loggerlib.Logger
	config       ServerConfig
}

// registerTools registers all tools using the tool provider
func (s *MattermostMCPServer) registerTools(accessMode tools.AccessMode) {
	toolProvider := tools.NewMattermostToolProvider(s.authProvider, s.logger, s.config.GetMMServerURL(), s.config.GetMMInternalServerURL(), s.config.GetDevMode(), accessMode)
	toolProvider.ProvideTools(s.mcpServer)
}

// GetMCPServer returns the underlying MCP server for testing purposes
func (s *MattermostMCPServer) GetMCPServer() *mcp.Server {
	return s.mcpServer
}
