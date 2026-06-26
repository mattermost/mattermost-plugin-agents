// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcp provides a client for the Model Control Protocol (MCP) that allows
// the AI plugin to access external tools provided by MCP servers.
//
// The UserClients represents a single user's connection to multiple MCP servers.
// The Client represents a connection to a single MCP server.
// The UserClients currently only supports authentication via Mattermost user ID header
// X-Mattermost-UserID. In the future it will support our OAuth implementation.
//
// The ClientManager manages multiple UserClients, allowing for efficient mangement
// of connections. It is responsible for creating and closing UserClients as needed.
//
// The organization reflects the need for each user to have their own connection to
// the MCP server given the design of MCP.
package mcp

import (
	"context"
	"fmt"
	"net/http"

	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// Errors represents a collection of errors from MCP operations.
type Errors struct {
	ToolAuthErrors []llm.ToolAuthError // Authentication errors users need to resolve
	Errors         []error             // Generic errors (connection, config, etc.)
}

// Type aliases for MCP config types, which are defined in the config package
// to avoid circular imports. Existing callers can continue to use mcp.Config, etc.
type Config = config.MCPConfig
type ServerConfig = config.MCPServerConfig
type EmbeddedServerConfig = config.MCPEmbeddedServerConfig
type ToolConfig = config.MCPToolConfig
type PluginServerConfig = config.PluginServerConfig

// DiscoverRemoteServerTools creates a temporary connection to a remote MCP server and discovers its tools
func DiscoverRemoteServerTools(
	ctx context.Context,
	userID string,
	serverConfig ServerConfig,
	log pluginapi.LogService,
	oauthManger *OAuthManager,
	httpClient *http.Client,
	toolsCache *ToolsCache,
) ([]ToolInfo, error) {
	// Create and connect to the remote server
	client, err := NewClient(ctx, userID, serverConfig, log, oauthManger, httpClient, toolsCache, false)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	serverTools := client.Tools()
	tools := make([]ToolInfo, 0, len(serverTools))
	for _, tool := range serverTools {
		tools = append(tools, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	return tools, nil
}

// DiscoverPluginServerTools lists tools from a plugin-registered MCP server
// over PluginHTTP, bypassing the per-user client cache.
func DiscoverPluginServerTools(
	ctx context.Context,
	userID string,
	cfg PluginServerConfig,
	sourcePluginAPI mmapi.Client,
	log pluginapi.LogService,
) ([]ToolInfo, error) {
	if sourcePluginAPI == nil {
		return nil, fmt.Errorf("sourcePluginAPI is nil; plugin MCP server %s cannot be reached", cfg.PluginID)
	}

	// Transport chain: PluginHTTPRoundTripper (URL rewrite) -> headerTransport (UserID).
	roundTripper := NewPluginHTTPRoundTripper(cfg.PluginID, cfg.Path, sourcePluginAPI)
	httpClient := &http.Client{
		Transport: &headerTransport{
			base:    roundTripper,
			headers: map[string]string{MMUserIDHeader: userID},
		},
	}

	mcpClient := gosdkmcp.NewClient(
		&gosdkmcp.Implementation{
			Name:    "mattermost-agents-admin-probe",
			Version: "1.0",
		},
		&gosdkmcp.ClientOptions{},
	)
	session, err := mcpClient.Connect(ctx, &gosdkmcp.StreamableClientTransport{
		Endpoint:   "http://plugin" + cfg.Path,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to plugin MCP server %s: %w", cfg.PluginID, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.ListTools(ctx, &gosdkmcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools on plugin MCP server %s: %w", cfg.PluginID, err)
	}

	tools := make([]ToolInfo, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return tools, nil
}

// DiscoverEmbeddedServerTools creates a temporary connection to an embedded MCP server and discovers its tools
func DiscoverEmbeddedServerTools(
	ctx context.Context,
	userID string,
	sessionID string,
	embeddedServerConfig EmbeddedServerConfig,
	embeddedServer EmbeddedMCPServer,
	log pluginapi.LogService,
	pluginAPI *pluginapi.Client,
) ([]ToolInfo, error) {
	// Create embedded client helper and connect to the embedded server
	embeddedClient := NewEmbeddedServerClient(embeddedServer, log, pluginAPI)

	client, err := embeddedClient.CreateClient(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	serverTools := client.Tools()
	tools := make([]ToolInfo, 0, len(serverTools))
	for _, tool := range serverTools {
		tools = append(tools, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	return tools, nil
}
