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
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPToolContext provides MCP-specific functionality with the authenticated client.
type MCPToolContext struct {
	Ctx        context.Context
	Client     *model.Client4
	AccessMode AccessMode
	BotUserID  string // User ID for AI-generated content tracking: Bot ID (embedded) or authenticated user ID (external servers)

	// UserID is the Mattermost user ID of the user the Client is authenticated as.
	// Empty when the auth provider cannot resolve an authenticated user.
	UserID string
}

// MCPToolResolver defines the signature for MCP tool resolvers
type MCPToolResolver func(*MCPToolContext, llm.ToolArgumentGetter) (string, error)

// typed adapts a resolver that accepts an already-decoded argument struct into
// an MCPToolResolver. It owns argument decoding and the standard "invalid
// arguments" error, so individual resolvers start at their real logic.
func typed[T any](name string, fn func(*MCPToolContext, T) (string, error)) MCPToolResolver {
	return func(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args T
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", name, err)
		}
		return fn(mcpContext, args)
	}
}

// mcpTool builds an MCPTool from a name, description, and typed resolver. The
// input schema is derived from the resolver's argument type and the provider's
// access mode, and the name is wired into the resolver once, so registration
// sites state each fact a single time.
func mcpTool[T any](p *MattermostToolProvider, name, description string, handler func(*MCPToolContext, T) (string, error)) MCPTool {
	return MCPTool{
		Name:        name,
		Description: description,
		Schema:      NewJSONSchemaForAccessMode[T](string(p.accessMode)),
		Resolver:    typed(name, handler),
	}
}

// mcpReadTool is mcpTool with ReadOnly set, for tools that only retrieve data.
func mcpReadTool[T any](p *MattermostToolProvider, name, description string, handler func(*MCPToolContext, T) (string, error)) MCPTool {
	tool := mcpTool(p, name, description, handler)
	tool.ReadOnly = true
	return tool
}

// MCPTool represents a tool specifically for MCP use with our custom context
type MCPTool struct {
	Name        string
	Description string
	Schema      *jsonschema.Schema
	Resolver    MCPToolResolver

	// ReadOnly is true when the tool only retrieves data. State-changing tools
	// are available at Enterprise and above.
	ReadOnly bool
}

type ToolProvider interface {
	ProvideTools(*mcp.Server)
}

// ServerConfig defines the common configuration methods every MCP server type
// provides to the tool provider.
type ServerConfig interface {
	GetMMServerURL() string
	GetMMInternalServerURL() string
	GetDevMode() bool
	GetTrackAIGenerated() bool
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
	authProvider            auth.AuthenticationProvider
	logger                  logger.Logger
	mmServerURL             string // Mattermost server URL for API communication (internal URL if set, otherwise external)
	devMode                 bool
	accessMode              AccessMode
	trackAIGenerated        bool                  // Whether to add ai_generated_by props to posts
	searchService           SemanticSearchService // Optional semantic search service, can be nil
	fileContentService      FileContentService    // Optional file content service for read_file, can be nil
	allowStateChangingTools func() bool           // Evaluated per request; nil fails closed
}

// NewMattermostToolProvider creates a new tool provider.
// searchService is optional and can be nil if semantic search is not available.
// allowStateChangingTools is a runtime predicate evaluated on each tools/list
// and tools/call; a nil predicate means state-changing tools are not available.
func NewMattermostToolProvider(authProvider auth.AuthenticationProvider, logger logger.Logger, config ServerConfig, accessMode AccessMode, searchService SemanticSearchService, fileContentService FileContentService, allowStateChangingTools func() bool) *MattermostToolProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	serverURL := config.GetMMInternalServerURL()
	if serverURL == "" {
		serverURL = config.GetMMServerURL()
	}

	return &MattermostToolProvider{
		authProvider:            authProvider,
		logger:                  logger,
		mmServerURL:             serverURL,
		devMode:                 config.GetDevMode(),
		accessMode:              accessMode,
		trackAIGenerated:        config.GetTrackAIGenerated(),
		searchService:           searchService,
		fileContentService:      fileContentService,
		allowStateChangingTools: allowStateChangingTools,
	}
}

func (p *MattermostToolProvider) mcpTools() []MCPTool {
	// Tool groups in registration order.
	groups := []func() []MCPTool{
		p.getPostTools,
		p.getScheduledPostTools,
		p.getReactionTools,
		p.getThreadTools,
		p.getChannelTools,
		p.getChannelMemberTools,
		p.getBookmarkTools,
		p.getUserTools,
		p.getStatusTools,
		p.getTeamTools,
		p.getSearchTools,
		p.getFileTools,
		p.getIntegrationTools,
		p.getGroupTools,
		p.getRoleTools,
		p.getAgentTools,
	}

	// Dev tools are only exposed when dev mode is enabled.
	if p.devMode {
		groups = append(groups, p.getDevUserTools, p.getDevPostTools, p.getDevTeamTools)
	}

	var mcpTools []MCPTool
	for _, group := range groups {
		mcpTools = append(mcpTools, group()...)
	}
	return mcpTools
}

// ToolNames returns the names of the tools this provider will register.
func (p *MattermostToolProvider) ToolNames() []string {
	mcpTools := p.mcpTools()
	names := make([]string, 0, len(mcpTools))
	for _, mcpTool := range mcpTools {
		names = append(names, mcpTool.Name)
	}
	return names
}

// ProvideTools registers all available MCP tools with the server.
func (p *MattermostToolProvider) ProvideTools(mcpServer *mcp.Server) {
	stateChanging := map[string]struct{}{}
	for _, mcpTool := range p.mcpTools() {
		p.registerDynamicTool(mcpServer, mcpTool)
		if !mcpTool.ReadOnly {
			stateChanging[mcpTool.Name] = struct{}{}
		}
	}

	// State-changing tools are listed and callable when allowStateChangingTools
	// reports they are available (Enterprise and above).
	mcpServer.AddReceivingMiddleware(stateChangingToolsMiddleware(stateChanging, p.allowStateChangingTools))
}

// stateChangingAllowed reports whether state-changing tools are available.
// A nil predicate fails closed.
func stateChangingAllowed(allow func() bool) bool {
	return allow != nil && allow()
}

// stateChangingToolsUnavailableMessage is the tools/call error text when
// state-changing Mattermost tools are not available at the current license level.
func stateChangingToolsUnavailableMessage() string {
	return enterprise.DisplayName(enterprise.CapStateChangingTools) + " are available at Enterprise and above"
}

func stateChangingToolsLicenseResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: stateChangingToolsUnavailableMessage()},
		},
		IsError: true,
	}
}

func mcpCallToolName(req mcp.Request) string {
	if req == nil {
		return ""
	}
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return ""
	}
	return params.Name
}

// stateChangingToolsMiddleware returns MCP receiving middleware that omits
// state-changing tools from tools/list when they are not available at the
// current license level, and answers a tools/call of such a tool with an MCP
// tool error result. allowStateChangingTools is evaluated per request so a
// license change is visible without rebuilding the server.
func stateChangingToolsMiddleware(stateChanging map[string]struct{}, allowStateChangingTools func() bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				name := mcpCallToolName(req)
				if _, gated := stateChanging[name]; gated && !stateChangingAllowed(allowStateChangingTools) {
					return stateChangingToolsLicenseResult(), nil
				}
				return next(ctx, method, req)
			}

			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" || stateChangingAllowed(allowStateChangingTools) {
				return result, err
			}
			listResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}

			filtered := make([]*mcp.Tool, 0, len(listResult.Tools))
			for _, tool := range listResult.Tools {
				if _, gated := stateChanging[tool.Name]; gated {
					continue
				}
				filtered = append(filtered, tool)
			}
			listResult.Tools = filtered
			return listResult, nil
		}
	}
}

// registerDynamicTool registers a single tool with the MCP server.
func (p *MattermostToolProvider) registerDynamicTool(server *mcp.Server, mcpTool MCPTool) {
	tool := &mcp.Tool{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
		InputSchema: nil, // Initialize as nil, will be set below if schema is available
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: mcpTool.ReadOnly,
		},
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
		Ctx:        ctx,
		Client:     client,
		AccessMode: p.accessMode,
		UserID:     userID,
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

	// Identify the properties the current access mode is not allowed to set.
	excluded := excludedFieldsForAccessMode(reflect.TypeFor[T](), accessMode)
	if len(excluded) == 0 {
		return baseSchema
	}

	// Shallow-copy the base schema and drop only the excluded properties, so that
	// everything else the generator produced ($defs, AdditionalProperties, item
	// schemas, ...) is preserved.
	filtered := *baseSchema
	filtered.Properties = make(map[string]*jsonschema.Schema, len(baseSchema.Properties))
	for name, prop := range baseSchema.Properties {
		if !excluded[name] {
			filtered.Properties[name] = prop
		}
	}
	if len(baseSchema.Required) > 0 {
		required := make([]string, 0, len(baseSchema.Required))
		for _, name := range baseSchema.Required {
			if !excluded[name] {
				required = append(required, name)
			}
		}
		filtered.Required = required
	}
	return &filtered
}

// excludedFieldsForAccessMode returns the set of JSON field names on struct type
// t that the given access mode is not allowed to use, per each field's `access:`
// tag. Returns nil when nothing is restricted.
func excludedFieldsForAccessMode(t reflect.Type, accessMode string) map[string]bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}

	var excluded map[string]bool
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := jsonFieldName(field)
		if name == "" {
			continue
		}
		restrictionTag := field.Tag.Get("access")
		if restrictionTag != "" && !isAccessAllowed(restrictionTag, accessMode) {
			if excluded == nil {
				excluded = make(map[string]bool)
			}
			excluded[name] = true
		}
	}
	return excluded
}

// jsonFieldName returns the JSON object key for a struct field, or "" when the
// field has no usable json tag (omitted or "-").
func jsonFieldName(field reflect.StructField) string {
	jsonTag := field.Tag.Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return ""
	}
	return strings.Split(jsonTag, ",")[0]
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
	if targetType.Kind() == reflect.Pointer {
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

		name := jsonFieldName(field)
		if name == "" {
			continue
		}

		// Check if this field is present in the incoming data
		if _, fieldPresent := incomingData[name]; !fieldPresent {
			continue // Field not provided, so no validation needed
		}

		// Check access tag
		restrictionTag := field.Tag.Get("access")

		// If field has access restrictions and current access mode is not allowed
		if restrictionTag != "" && !isAccessAllowed(restrictionTag, currentAccessMode) {
			return fmt.Errorf("field '%s' is not available in %s access mode (requires: %s)", name, currentAccessMode, restrictionTag)
		}
	}

	return nil
}
