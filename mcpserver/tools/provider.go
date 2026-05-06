// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/types"
	"github.com/mattermost/mattermost-plugin-agents/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHookConfig holds an optional opaque before-hook key for a tool.
type ToolHookConfig struct {
	BeforeHookKey string `json:"before_hook_key,omitempty"`
}

// MCPToolContext provides MCP-specific functionality with the authenticated client.
type MCPToolContext struct {
	Ctx        context.Context
	Client     *model.Client4
	AccessMode AccessMode
	BotUserID  string // User ID for AI-generated content tracking: Bot ID (embedded) or authenticated user ID (external servers)

	// UserID is the Mattermost user ID of the user the Client is authenticated as.
	// Empty when the auth provider cannot resolve an authenticated user.
	UserID string

	// MMServerURL is the Mattermost server base URL (same as API Client4 origin) for resolving hook keys and firing callbacks.
	MMServerURL        string
	BeforeHookResolver auth.BeforeHookResolver
	ToolHooks          map[string]ToolHookConfig
}

// MCPToolResolver defines the signature for MCP tool resolvers
type MCPToolResolver func(*MCPToolContext, llm.ToolArgumentGetter) (string, error)

// MCPTool represents a tool specifically for MCP use with our custom context
type MCPTool struct {
	Name        string
	Description string
	Schema      *jsonschema.Schema
	Resolver    MCPToolResolver
}

type ToolProvider interface {
	ProvideTools(*mcp.Server)
}

// SemanticSearchService provides semantic search capabilities for the MCP server.
// *search.Search implements this interface directly for embedded servers.
// HTTPSemanticSearchService implements it for external servers via HTTP callbacks.
type SemanticSearchService interface {
	Enabled() bool
	Search(ctx context.Context, query string, opts search.Options) ([]search.RAGResult, error)
}

// MattermostToolProvider provides Mattermost tools following the mmtools pattern
type MattermostToolProvider struct {
	authProvider     auth.AuthenticationProvider
	logger           logger.Logger
	mmServerURL      string // Mattermost server URL for API communication (internal URL if set, otherwise external)
	devMode          bool
	accessMode       AccessMode
	trackAIGenerated bool                  // Whether to add ai_generated_by props to posts
	searchService    SemanticSearchService // Optional semantic search service, can be nil
}

// NewMattermostToolProvider creates a new tool provider
// Now accepts a ServerConfig interface to avoid circular dependencies
// searchService is optional and can be nil if semantic search is not available
func NewMattermostToolProvider(authProvider auth.AuthenticationProvider, logger logger.Logger, config types.ServerConfig, accessMode AccessMode, searchService SemanticSearchService) *MattermostToolProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	serverURL := config.GetMMInternalServerURL()
	if serverURL == "" {
		serverURL = config.GetMMServerURL()
	}

	return &MattermostToolProvider{
		authProvider:     authProvider,
		logger:           logger,
		mmServerURL:      serverURL,
		devMode:          config.GetDevMode(),
		accessMode:       accessMode,
		trackAIGenerated: config.GetTrackAIGenerated(),
		searchService:    searchService,
	}
}

// ProvideTools registers all available MCP tools with the server.
func (p *MattermostToolProvider) ProvideTools(mcpServer *mcp.Server) {
	mcpTools := []MCPTool{}

	// Add regular tools
	mcpTools = append(mcpTools, p.getPostTools()...)
	mcpTools = append(mcpTools, p.getChannelTools()...)
	mcpTools = append(mcpTools, p.getTeamTools()...)
	mcpTools = append(mcpTools, p.getSearchTools()...)
	mcpTools = append(mcpTools, p.getAgentTools()...)

	// Automation tools are always registered; availability is checked dynamically
	// via middleware on each tools/list request.
	mcpTools = append(mcpTools, p.getAutomationTools()...)

	// Add dev tools if dev mode is enabled
	if p.devMode {
		mcpTools = append(mcpTools, p.getDevUserTools()...)
		mcpTools = append(mcpTools, p.getDevPostTools()...)
		mcpTools = append(mcpTools, p.getDevTeamTools()...)
	}

	for _, mcpTool := range mcpTools {
		p.registerDynamicTool(mcpServer, mcpTool)
	}

	// Add middleware to dynamically filter automation tools from tools/list
	// when the channel automation plugin is not installed.
	mcpServer.AddReceivingMiddleware(p.automationToolFilterMiddleware())
}

func (p *MattermostToolProvider) stripAutomationFromToolsListResult(result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok {
		return result
	}
	filtered := make([]*mcp.Tool, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		if !IsAutomationTool(tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	listResult.Tools = filtered
	return listResult
}

// automationToolFilterMiddleware returns MCP receiving middleware that filters
// automation tools from tools/list when the channel automation plugin is not installed.
func (p *MattermostToolProvider) automationToolFilterMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return result, err
			}

			if !p.isAutomationPluginInstalled() {
				return p.stripAutomationFromToolsListResult(result), nil
			}
			return result, nil
		}
	}
}

// registerDynamicTool registers a single tool with the MCP server.
func (p *MattermostToolProvider) registerDynamicTool(server *mcp.Server, mcpTool MCPTool) {
	tool := &mcp.Tool{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
		InputSchema: nil, // Initialize as nil, will be set below if schema is available
	}

	// Set the InputSchema from the MCPTool schema
	if mcpTool.Schema != nil {
		tool.InputSchema = mcpTool.Schema
		p.logger.Debug("Registered tool with schema", "tool", mcpTool.Name)
	} else {
		// The MCP SDK requires an input schema, so provide a basic empty object schema
		// This maintains compatibility with tools that don't define schemas
		emptySchema := &jsonschema.Schema{
			Type:       "object",
			Properties: make(map[string]*jsonschema.Schema),
		}
		tool.InputSchema = emptySchema
		p.logger.Debug("Registered tool with empty schema (no schema provided)", "tool", mcpTool.Name)
	}

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Log tool invocation
		p.logger.Debug("MCP tool called", "tool", mcpTool.Name)

		// Create MCP context from the authenticated client, passing along any metadata
		mcpContext, err := p.createMCPToolContext(ctx, req.Params.Meta)
		if err != nil {
			p.logger.Debug("Failed to create MCP tool context", "error", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Error: " + err.Error()},
				},
				IsError: true,
			}, nil
		}

		// Create argument getter that extracts arguments from the MCP request
		argsGetter := func(target interface{}) error {
			// Convert MCP arguments to the target struct
			argumentsBytes, marshalErr := json.Marshal(req.Params.Arguments)
			if marshalErr != nil {
				return fmt.Errorf("failed to marshal arguments: %w", marshalErr)
			}

			// Validate access restrictions before unmarshaling
			if validationErr := validateAccessRestrictions(argumentsBytes, target, string(mcpContext.AccessMode)); validationErr != nil {
				return fmt.Errorf("access validation failed: %w", validationErr)
			}

			return json.Unmarshal(argumentsBytes, target)
		}

		// Run the optional before-hook with the raw tool arguments. The hook can
		// reject the call by returning an error which is surfaced as a tool error
		// to the LLM.
		if hookErr := RunBeforeHook(mcpContext, mcpTool.Name, req.Params.Arguments); hookErr != nil {
			p.logger.Debug("MCP tool before-hook rejected or failed", "tool", mcpTool.Name, "error", hookErr.Error())
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Error: " + hookErr.Error()},
				},
				IsError: true,
			}, nil
		}

		// Call the tool resolver
		result, err := mcpTool.Resolver(mcpContext, argsGetter)
		if err != nil {
			p.logger.Debug("MCP tool failed", "tool", mcpTool.Name, "error", err.Error())
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Error: " + err.Error()},
				},
				IsError: true,
			}, nil
		}

		// Log successful completion
		p.logger.Debug("MCP tool completed successfully", "tool", mcpTool.Name)

		// Return successful result
		callToolResult := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: result},
			},
			IsError: false,
		}
		return callToolResult, nil
	}

	// Register the tool using the Server.AddTool method
	server.AddTool(tool, handler)
}

// createMCPToolContext creates an MCPToolContext from the Go context, authenticated client, and request metadata
func (p *MattermostToolProvider) createMCPToolContext(ctx context.Context, metadata mcp.Meta) (*MCPToolContext, error) {
	client, err := p.authProvider.GetAuthenticatedMattermostClient(ctx)
	if err != nil {
		return nil, err
	}

	var userID string
	if identityProvider, ok := p.authProvider.(auth.UserIdentityProvider); ok {
		if user, userErr := identityProvider.GetAuthenticatedUser(ctx); userErr == nil && user != nil {
			userID = user.Id
		} else if userErr != nil {
			p.logger.Debug("failed to resolve authenticated user for tool-call context", "error", userErr.Error())
		}
	}

	mcpContext := &MCPToolContext{
		Ctx:         ctx,
		Client:      client,
		AccessMode:  p.accessMode,
		MMServerURL: p.mmServerURL,
		ToolHooks:   decodeToolHooksFromMetadata(metadata),
		UserID:      userID,
	}

	if resolver, ok := ctx.Value(auth.BeforeHookResolverContextKey).(auth.BeforeHookResolver); ok {
		mcpContext.BeforeHookResolver = resolver
	}

	// Extract bot_user_id from metadata if present (for embedded servers)
	// Only do this when tracking is enabled
	if p.trackAIGenerated && metadata != nil {
		if botUserID, ok := metadata["bot_user_id"].(string); ok {
			mcpContext.BotUserID = botUserID
		}
	}

	return mcpContext, nil
}

func decodeToolHooksFromMetadata(metadata mcp.Meta) map[string]ToolHookConfig {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["tool_hooks"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]ToolHookConfig, len(raw))
	for name, v := range raw {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		var cfg ToolHookConfig
		if s, ok := entry["before_hook_key"].(string); ok {
			cfg.BeforeHookKey = s
		}
		out[name] = cfg
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NewJSONSchemaForAccessMode creates a JSONSchema from a Go struct, filtering fields based on access mode
//
// Access tag examples:
//   - access:"local" - only available for local access mode
//   - access:"remote" - only available for remote access mode
//   - access:"local,remote" - available for both local and remote access modes
//   - no access tag - available in all access modes
//
// The function uses comma-separated parsing, so you can specify multiple access modes.
func NewJSONSchemaForAccessMode[T any](accessMode string) *jsonschema.Schema {
	// Validate access mode - empty string indicates uninitialized AccessMode
	if accessMode == "" {
		panic("access mode cannot be empty - indicates uninitialized AccessMode")
	}

	// Get the base schema
	baseSchema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create JSON schema from struct: %v", err))
	}

	// If no properties to filter, return the base schema
	if baseSchema.Properties == nil {
		return baseSchema
	}

	// Get the struct type to inspect field tags
	var zero T
	structType := reflect.TypeOf(zero)

	// If it's a pointer, get the underlying type
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}

	// If it's not a struct, return the base schema
	if structType.Kind() != reflect.Struct {
		return baseSchema
	}

	// Create a new schema with filtered properties
	filteredSchema := &jsonschema.Schema{
		Type:        baseSchema.Type,
		Title:       baseSchema.Title,
		Description: baseSchema.Description,
		Properties:  make(map[string]*jsonschema.Schema),
		Required:    []string{},
	}

	// Check each field and its access tag
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)

		// Get the JSON field name
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		// Extract field name (ignore omitempty and other options)
		jsonFieldName := strings.Split(jsonTag, ",")[0]
		if jsonFieldName == "" {
			continue
		}

		// Check access tag
		restrictionTag := field.Tag.Get("access")

		// Include field if:
		// - No restriction tag (available for all access modes)
		// - Current access mode is in the comma-separated list of allowed modes
		includeField := restrictionTag == "" || isAccessAllowed(restrictionTag, accessMode)

		if includeField {
			// Copy the property from base schema if it exists
			if baseProperty, exists := baseSchema.Properties[jsonFieldName]; exists {
				filteredSchema.Properties[jsonFieldName] = baseProperty
			}

			// Check if field was required in original schema
			for _, requiredField := range baseSchema.Required {
				if requiredField == jsonFieldName {
					filteredSchema.Required = append(filteredSchema.Required, jsonFieldName)
					break
				}
			}
		}
	}

	return filteredSchema
}

// isAccessAllowed checks if the current access mode is allowed based on the access tag
// Supports comma-separated access modes (e.g., "local", "remote", "local,remote")
func isAccessAllowed(restrictionTag, currentAccessMode string) bool {
	if restrictionTag == "" {
		return true // No restrictions
	}

	// Normalize and split by comma
	allowedValues := strings.Split(strings.ReplaceAll(restrictionTag, " ", ""), ",")

	// Check each allowed value
	for _, allowed := range allowedValues {
		// Direct access mode matching
		if allowed == currentAccessMode {
			return true
		}
	}

	return false
}

// validateAccessRestrictions validates that no access-restricted fields are present in the JSON data
// for the current access mode. This prevents clients from sending fields they shouldn't have access to.
func validateAccessRestrictions(jsonData []byte, target interface{}, currentAccessMode string) error {
	if currentAccessMode == "" {
		panic("access mode cannot be empty - indicates uninitialized AccessMode")
	}
	// Get the struct type to inspect field tags
	targetType := reflect.TypeOf(target)
	if targetType.Kind() == reflect.Ptr {
		targetType = targetType.Elem()
	}

	// If it's not a struct, no validation needed
	if targetType.Kind() != reflect.Struct {
		return nil
	}

	// Check if the incoming JSON is actually an object/map
	// If it's not an object, we can't have field restrictions to validate
	var incomingData map[string]interface{}
	if err := json.Unmarshal(jsonData, &incomingData); err != nil {
		// If JSON can't be parsed as an object, it's likely a primitive value or array
		// In this case, there are no fields to validate transport restrictions for
		return nil
	}

	// Check each field in the struct
	for i := 0; i < targetType.NumField(); i++ {
		field := targetType.Field(i)

		// Get the JSON field name
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		// Extract field name (ignore omitempty and other options)
		jsonFieldName := strings.Split(jsonTag, ",")[0]
		if jsonFieldName == "" {
			continue
		}

		// Check if this field is present in the incoming data
		if _, fieldPresent := incomingData[jsonFieldName]; !fieldPresent {
			continue // Field not provided, so no validation needed
		}

		// Check access tag
		restrictionTag := field.Tag.Get("access")

		// If field has access restrictions and current access mode is not allowed
		if restrictionTag != "" && !isAccessAllowed(restrictionTag, currentAccessMode) {
			return fmt.Errorf("field '%s' is not available in %s access mode (requires: %s)", jsonFieldName, currentAccessMode, restrictionTag)
		}
	}

	return nil
}
