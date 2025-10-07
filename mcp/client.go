// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const MMUserIDHeader = "X-Mattermost-UserID"

// Client represents the connection to a single MCP server
type Client struct {
	session      *mcp.ClientSession
	config       ServerConfig
	tools        map[string]*mcp.Tool
	userID       string
	log          pluginapi.LogService
	oauthManager *OAuthManager
}

// ServerConfig contains the configuration for a single MCP server
type ServerConfig struct {
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	BaseURL string            `json:"baseURL"`
	Headers map[string]string `json:"headers,omitempty"`
}

// NewClient creates a new MCP client for the given server and user and connects to the specified MCP server
func NewClient(ctx context.Context, userID string, serverConfig ServerConfig, log pluginapi.LogService, oauthManager *OAuthManager) (*Client, error) {
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

	c.session = session
	return c, nil
}

func (c *Client) createSession(ctx context.Context, serverConfig ServerConfig) (*mcp.ClientSession, error) {
	// Prepare headers
	headers := make(map[string]string)
	headers[MMUserIDHeader] = c.userID
	maps.Copy(headers, serverConfig.Headers)

	// TODO: Load and check cached authentication information

	// We have no infomration about this server, so try to connect various ways.
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
	if c.session == nil {
		return "", fmt.Errorf("MCP client not connected")
	}

	// Call the tool using new SDK
	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		if errors.Is(err, mcp.ErrConnectionClosed) {
			c.session, err = c.createSession(ctx, c.config)
			if err != nil {
				return "", fmt.Errorf("failed to reconnect to MCP server %s: %w", c.config.Name, err)
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
