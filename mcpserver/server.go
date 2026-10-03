// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostMCPServer provides a high-level interface for creating an MCP server
// with Mattermost-specific tools and authentication
type MattermostMCPServer struct {
	mcpServer    *mcp.Server
	authProvider auth.AuthenticationProvider
	logger       loggerlib.Logger
	config       tools.ServerConfig
}

// newPluginCallbackServices builds the HTTP search and file-content services
// that call back to the Agents plugin's /api/v1 endpoints on the given
// Mattermost server URL.
func newPluginCallbackServices(mmServerURL string) (*tools.HTTPSemanticSearchService, *tools.HTTPFileContentService) {
	pluginURL := strings.TrimRight(mmServerURL, "/") + "/plugins/mattermost-ai"
	return tools.NewHTTPSemanticSearchService(pluginURL), tools.NewHTTPFileContentService(pluginURL)
}

// registerTools registers all tools using the tool provider.
// searchService and fileContentService are optional and can be nil when the
// corresponding capability is unavailable.
// allowStateChangingTools is evaluated per request; a nil predicate means
// state-changing tools are not available.
// delegationService is optional and can be nil; ask_agent is hidden without it.
// enableDemoApps selects the demo MCP Apps tool group on the same provider (no
// second registration lane).
func (s *MattermostMCPServer) registerTools(accessMode tools.AccessMode, searchService tools.SemanticSearchService, fileContentService tools.FileContentService, allowStateChangingTools func() bool, delegationService tools.DelegationService, enableDemoApps bool) {
	toolProvider := tools.NewMattermostToolProvider(s.authProvider, s.logger, s.config, accessMode, searchService, fileContentService, allowStateChangingTools, delegationService)
	toolProvider.SetEnableDemoApps(enableDemoApps)
	toolProvider.ProvideTools(s.mcpServer)
	if enableDemoApps {
		s.logger.Info("Registered demo MCP Apps tools")
	}
}

// GetMCPServer returns the underlying MCP server for testing purposes
func (s *MattermostMCPServer) GetMCPServer() *mcp.Server {
	return s.mcpServer
}
