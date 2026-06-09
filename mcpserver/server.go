// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostMCPServer provides a high-level interface for creating an MCP server
// with Mattermost-specific tools and authentication
type MattermostMCPServer struct {
	mcpServer    *mcp.Server
	authProvider auth.AuthenticationProvider
	logger       loggerlib.Logger
	config       types.ServerConfig
}

// registerTools registers all tools using the tool provider.
// searchService and fileContentService are optional and can be nil when the
// corresponding capability is unavailable.
func (s *MattermostMCPServer) registerTools(accessMode tools.AccessMode, searchService tools.SemanticSearchService, fileContentService tools.FileContentService) {
	toolProvider := tools.NewMattermostToolProvider(s.authProvider, s.logger, s.config, accessMode, searchService, fileContentService)
	toolProvider.ProvideTools(s.mcpServer)
}

// GetMCPServer returns the underlying MCP server for testing purposes
func (s *MattermostMCPServer) GetMCPServer() *mcp.Server {
	return s.mcpServer
}
