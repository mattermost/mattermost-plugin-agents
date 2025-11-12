// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	MMUserIDHeader     = "X-Mattermost-UserID"
	EmbeddedServerName = "Mattermost"
	EmbeddedClientKey  = "embedded://mattermost"
)

// EmbeddedMCPServer interface for dependency injection
type EmbeddedMCPServer interface {
	CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*mcp.InMemoryTransport, error)
}

// EmbeddedServerClient handles connections to the embedded MCP server
type EmbeddedServerClient struct {
	server    EmbeddedMCPServer
	log       pluginapi.LogService
	pluginAPI *pluginapi.Client
}

// Client represents the connection to a single MCP server
type Client struct {
	session        *mcp.ClientSession
	config         ServerConfig
	tools          map[string]*mcp.Tool
	userID         string
	log            pluginapi.LogService
	oauthManager   *OAuthManager
	embeddedClient *EmbeddedServerClient // for reconnection (nil for remote servers)
	sessionID      string                // session ID for embedded server reconnection
}

// ServerConfig contains the configuration for a single MCP server
type ServerConfig struct {
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	BaseURL string            `json:"baseURL"`
	Headers map[string]string `json:"headers,omitempty"`
}

func NewEmbeddedServerClient(server EmbeddedMCPServer, log pluginapi.LogService, pluginAPI *pluginapi.Client) *EmbeddedServerClient {
	return &EmbeddedServerClient{
		server:    server,
		log:       log,
		pluginAPI: pluginAPI,
	}
}

// CreateClient creates an embedded MCP client using session ID for authentication
// If sessionID is empty, creates an unauthenticated client (used for tool discovery)
func (c *EmbeddedServerClient) CreateClient(ctx context.Context, userID, sessionID string) (*Client, error) {
	// Validate session exists before creating transport (unless empty for tool discovery)
	if sessionID != "" {
		mmSession, err := c.pluginAPI.Session.Get(sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to get session: %w", err)
		}
		if mmSession == nil {
			return nil, fmt.Errorf("session not found")
		}
	}

	// Get the in-memory transport from the embedded server
	transport, err := c.server.CreateClientTransport(userID, sessionID, c.pluginAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory transport: %w", err)
	}

	// Create MCP client
	mcpClient := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mattermost-agents-embedded",
			Version: "1.0",
		},
		&mcp.ClientOptions{},
	)

	// Connect to the embedded server using in-memory transport
	mcpSession, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to embedded MCP server: %w", err)
	}

	// Create client instance
	client := &Client{
		session:        mcpSession,
		config:         ServerConfig{Name: EmbeddedClientKey},
		tools:          make(map[string]*mcp.Tool),
		userID:         userID,
		log:            c.log,
		oauthManager:   nil,       // Embedded servers don't use OAuth
		embeddedClient: c,         // Store client helper for reconnection
		sessionID:      sessionID, // Store session ID for reconnection
	}

	// Initialize tools
	initResult, err := mcpSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		mcpSession.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if len(initResult.Tools) == 0 {
		mcpSession.Close()
		return nil, fmt.Errorf("no tools found on MCP server %s for user %s", EmbeddedClientKey, userID)
	}

	// Store the tools for this server
	for _, tool := range initResult.Tools {
		client.tools[tool.Name] = tool
		c.log.Debug("Registered MCP tool",
			"userID", userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", EmbeddedClientKey)
	}

	c.log.Debug("Successfully connected to embedded MCP server",
		"userID", userID,
		"server", EmbeddedClientKey)

	return client, nil
}

// NewClient creates a new MCP client for the given server and user and connects to the specified MCP server
func NewClient(ctx context.Context, userID string, serverConfig ServerConfig, log pluginapi.LogService, oauthManager *OAuthManager, toolsCache *ToolsCache) (*Client, error) {
	c := &Client{
		session:      nil,
		config:       serverConfig,
		tools:        make(map[string]*mcp.Tool),
		userID:       userID,
		log:          log,
		oauthManager: oauthManager,
	}

	session, err := c.createSession(ctx, serverConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP session for server %s: %w", serverConfig.Name, err)
	}

	// Try to get tools from global cache first
	serverID := serverConfig.Name
	if toolsCache != nil {
		cachedTools := toolsCache.GetTools(serverID)
		if len(cachedTools) > 0 {
			// Cache hit - use cached tools
			c.tools = cachedTools
			log.Debug("Using cached tools for MCP server",
				"userID", userID,
				"server", serverConfig.Name,
				"toolCount", len(cachedTools))
			c.session = session
			return c, nil
		}
	}

	// Cache miss - fetch tools from server
	initResult, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if len(initResult.Tools) == 0 {
		session.Close()
		return nil, fmt.Errorf("no tools found on MCP server %s for user %s", serverConfig.Name, userID)
	}

	// Store the tools for this server
	for _, tool := range initResult.Tools {
		c.tools[tool.Name] = tool
		log.Debug("Registered MCP tool",
			"userID", userID,
			"name", tool.Name,
			"description", tool.Description,
			"server", serverConfig.Name)
	}

	// Update the global cache with fetched tools
	if toolsCache != nil {
		if err := toolsCache.SetTools(serverID, serverConfig.Name, serverConfig.BaseURL, c.tools, time.Now()); err != nil {
			log.Warn("Failed to update tools cache", "server", serverConfig.Name, "error", err)
		}
	}

	c.session = session
	return c, nil
}

func (c *Client) createSession(ctx context.Context, serverConfig ServerConfig) (*mcp.ClientSession, error) {
	// Prepare headers for remote servers
	headers := make(map[string]string)
	headers[MMUserIDHeader] = c.userID
	maps.Copy(headers, serverConfig.Headers)

	// TODO: Load and check cached authentication information

	// We have no information about this server, so try to connect various ways.
	client := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mattermost-agents",
			Version: "1.0",
		},
		&mcp.ClientOptions{},
	)

	httpClient := c.httpClient(headers)

	// Create an SSE transport with the authenticated HTTP client
	transport := &mcp.SSEClientTransport{
		Endpoint:   serverConfig.BaseURL,
		HTTPClient: httpClient,
	}

	// Try to connect using the OAuth-enabled SSE transport
	session, errSSEConnect := client.Connect(ctx, transport, nil)
	if errSSEConnect == nil {
		// Successfully connected with OAuth
		return session, nil
	}

	var mcpAuthErr *mcpUnauthrorized
	if errors.As(errSSEConnect, &mcpAuthErr) {
		authURL, oauthErr := c.oauthManager.InitiateOAuthFlow(ctx, c.userID, c.config.Name, serverConfig.BaseURL, mcpAuthErr.MetadataURL())
		if oauthErr != nil {
			return nil, fmt.Errorf("failed to initiate OAuth flow for server %s: %w", c.config.Name, oauthErr)
		}
		return nil, &OAuthNeededError{
			authURL: authURL,
		}
	}

	// Unauthenticated HTTP
	session, errUnauthHTTP := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   serverConfig.BaseURL,
		HTTPClient: httpClient,
	}, nil)
	if errUnauthHTTP == nil {
		// Successfully connected without authentication
		return session, nil
	}

	// If we reach here, all connection attempts failed
	return nil, fmt.Errorf("failed to connect to MCP server %s, SSE: %w, HTTP: %w", c.config.Name, errSSEConnect, errUnauthHTTP)
}

// Close closes the connection to the MCP server
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// Tools returns the tools available from this client
func (c *Client) Tools() map[string]*mcp.Tool {
	return c.tools
}

// CallTool calls a tool on this MCP server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return c.CallToolWithMetadata(ctx, toolName, args, nil)
}

// CallToolWithMetadata calls a tool on this MCP server with optional metadata
func (c *Client) CallToolWithMetadata(ctx context.Context, toolName string, args map[string]any, metadata map[string]any) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("MCP client not connected")
	}

	// Call the tool using new SDK
	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	// Add metadata if provided
	if metadata != nil {
		params.Meta = mcp.Meta(metadata)
	}

	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		if errors.Is(err, mcp.ErrConnectionClosed) {
			if c.embeddedClient != nil {
				// Reconnect to embedded server using stored client helper and session ID
				if c.sessionID == "" {
					return "", fmt.Errorf("embedded server connection lost and cannot be reconnected: missing session ID")
				}

				newClient, reconnectErr := c.embeddedClient.CreateClient(ctx, c.userID, c.sessionID)
				if reconnectErr != nil {
					return "", fmt.Errorf("failed to reconnect to embedded MCP server: %w", reconnectErr)
				}

				// Update session and tools from the new client
				c.session = newClient.session
				c.tools = newClient.tools
				c.log.Debug("Successfully reconnected to embedded MCP server", "userID", c.userID)
			} else {
				// Reconnect to remote server
				c.session, err = c.createSession(ctx, c.config)
				if err != nil {
					return "", fmt.Errorf("failed to reconnect to MCP server %s: %w", c.config.Name, err)
				}
			}

			// Retry the tool call after reconnecting
			result, err = c.session.CallTool(ctx, params)
			if err != nil {
				return "", fmt.Errorf("failed to call tool %s on server %s after reconnecting: %w", toolName, c.config.Name, err)
			}
		} else {
			return "", fmt.Errorf("failed to call tool %s on server %s: %w", toolName, c.config.Name, err)
		}
	}

	// Extract text content from the result
	if len(result.Content) > 0 {
		text := ""
		for _, content := range result.Content {
			// Use type assertion to extract text content
			if textContent, ok := content.(*mcp.TextContent); ok {
				text += textContent.Text + "\n"
			}
		}
		return text, nil
	}

	return "", fmt.Errorf("no text content found in response from tool %s on server %s", toolName, c.config.Name)
}
