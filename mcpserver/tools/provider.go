// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

// MCPToolContext provides MCP-specific functionality with the authenticated client
type MCPToolContext struct {
	Client *model.Client4
}

// MCPToolResolver defines the signature for MCP tool resolvers
type MCPToolResolver func(*MCPToolContext, llm.ToolArgumentGetter) (string, error)

// MCPTool represents a tool specifically for MCP use with our custom context
type MCPTool struct {
	Name        string
	Description string
	Schema      interface{}
	Resolver    MCPToolResolver
}

type ToolProvider interface {
	ProvideTools(*server.MCPServer)
}

// MattermostToolProvider provides Mattermost tools following the mmtools pattern
type MattermostToolProvider struct {
	authProvider auth.AuthenticationProvider
	logger       mlog.LoggerIFace
	serverURL    string
	devMode      bool
}

// NewMattermostToolProvider creates a new tool provider
func NewMattermostToolProvider(authProvider auth.AuthenticationProvider, logger mlog.LoggerIFace, serverURL string, devMode bool) *MattermostToolProvider {
	return &MattermostToolProvider{
		authProvider: authProvider,
		logger:       logger,
		serverURL:    serverURL,
		devMode:      devMode,
	}
}

// ProvideTools provides all tools to the MCP server by registering them
func (p *MattermostToolProvider) ProvideTools(mcpServer *server.MCPServer) {
	mcpTools := []MCPTool{}

	// Add regular tools
	mcpTools = append(mcpTools, p.getPostTools()...)
	mcpTools = append(mcpTools, p.getChannelTools()...)
	mcpTools = append(mcpTools, p.getTeamTools()...)
	mcpTools = append(mcpTools, p.getSearchTools()...)

	// Add dev tools if dev mode is enabled
	if p.devMode {
		mcpTools = append(mcpTools, p.getDevUserTools()...)
		mcpTools = append(mcpTools, p.getDevPostTools()...)
		mcpTools = append(mcpTools, p.getDevTeamTools()...)
		mcpTools = append(mcpTools, p.getDevChannelTools()...)
	}

	// Convert and register each tool
	for _, mcpTool := range mcpTools {
		libMCPTool := p.convertMCPToolToLibMCPTool(mcpTool)
		mcpServer.AddTool(libMCPTool, p.createMCPToolHandler(mcpTool.Resolver))
	}
}

// convertMCPToolToLibMCPTool converts our MCPTool to a library mcp.Tool
func (p *MattermostToolProvider) convertMCPToolToLibMCPTool(mcpTool MCPTool) mcp.Tool {
	// Try to convert the JSON schema to MCP format
	if schema, ok := mcpTool.Schema.(*jsonschema.Schema); ok && schema != nil {
		// Marshal the jsonschema.Schema to JSON for use as raw schema
		schemaBytes, err := json.Marshal(schema)
		if err == nil {
			// Use the raw JSON schema - this provides proper parameter validation and documentation
			return mcp.NewToolWithRawSchema(mcpTool.Name, mcpTool.Description, schemaBytes)
		}
		// Log the error but continue with fallback
		p.logger.Warn("Failed to marshal JSON schema for tool", mlog.String("tool", mcpTool.Name), mlog.Err(err))
	}

	// Fallback to basic tool creation without schema
	// This still works but provides less rich client experience
	return mcp.NewTool(mcpTool.Name, mcp.WithDescription(mcpTool.Description))
}

// createMCPToolHandler creates an MCP tool handler that wraps an MCP tool resolver
func (p *MattermostToolProvider) createMCPToolHandler(resolver MCPToolResolver) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Create MCP tool context from MCP context
		mcpContext, err := p.createMCPToolContext(ctx)
		if err != nil {
			p.logger.Debug("Failed to create LLM context", mlog.Err(err))
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Error: " + err.Error(),
					},
				},
				IsError: true,
			}, nil
		}

		// Create an argument getter that extracts arguments from the MCP request
		argsGetter := func(target interface{}) error {
			// Convert MCP arguments to the target struct
			argumentsBytes, marshalErr := json.Marshal(request.Params.Arguments)
			if marshalErr != nil {
				return fmt.Errorf("failed to marshal arguments: %w", marshalErr)
			}

			return json.Unmarshal(argumentsBytes, target)
		}

		// Call the MCP tool resolver
		result, err := resolver(mcpContext, argsGetter)
		if err != nil {
			p.logger.Debug("LLM tool resolver failed", mlog.Err(err))
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Error: " + err.Error(),
					},
				},
				IsError: true,
			}, nil
		}

		// Return successful result
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: result,
				},
			},
			IsError: false,
		}, nil
	}
}

// createMCPToolContext creates an MCPToolContext from the Go context and authenticated client
func (p *MattermostToolProvider) createMCPToolContext(ctx context.Context) (*MCPToolContext, error) {
	client, err := p.authProvider.GetAuthenticatedMattermostClient(ctx)
	if err != nil {
		return nil, err
	}

	return &MCPToolContext{
		Client: client,
	}, nil
}
