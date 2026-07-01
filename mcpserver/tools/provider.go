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
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/types"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
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

// typed adapts a resolver that accepts an already-decoded argument struct into
// an MCPToolResolver. It owns argument decoding and the standard "invalid
// arguments" error, so individual resolvers start at their real logic. The
// returned resolver is an ordinary MCPToolResolver, so registerDynamicTool and
// the before-hook (which still sees the raw request arguments) are unchanged.
func typed[T any](name string, fn func(*MCPToolContext, T) (string, error)) MCPToolResolver {
	return func(mcpContext *MCPToolContext, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args T
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", name, err)
		}
		return fn(mcpContext, args)
	}
}

// MCPTool represents a tool specifically for MCP use with our custom context
type MCPTool struct {
	Name        string
	Description string
	Schema      *jsonschema.Schema
	Resolver    MCPToolResolver

	// Available, when set, gates the tool's visibility: it is evaluated on each
	// tools/list request and the tool is hidden when it returns false. Nil means
	// always available.
	Available func() bool
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
	authProvider       auth.AuthenticationProvider
	logger             logger.Logger
	mmServerURL        string // Mattermost server URL for API communication (internal URL if set, otherwise external)
	devMode            bool
	accessMode         AccessMode
	trackAIGenerated   bool                  // Whether to add ai_generated_by props to posts
	searchService      SemanticSearchService // Optional semantic search service, can be nil
	fileContentService FileContentService    // Optional file content service for read_file, can be nil
}

// NewMattermostToolProvider creates a new tool provider
// Now accepts a ServerConfig interface to avoid circular dependencies
// searchService is optional and can be nil if semantic search is not available
func NewMattermostToolProvider(authProvider auth.AuthenticationProvider, logger logger.Logger, config types.ServerConfig, accessMode AccessMode, searchService SemanticSearchService, fileContentService FileContentService) *MattermostToolProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	serverURL := config.GetMMInternalServerURL()
	if serverURL == "" {
		serverURL = config.GetMMServerURL()
	}

	return &MattermostToolProvider{
		authProvider:       authProvider,
		logger:             logger,
		mmServerURL:        serverURL,
		devMode:            config.GetDevMode(),
		accessMode:         accessMode,
		trackAIGenerated:   config.GetTrackAIGenerated(),
		searchService:      searchService,
		fileContentService: fileContentService,
	}
}

func (p *MattermostToolProvider) mcpTools() []MCPTool {
	// Tool groups in registration order. Automation tools are always included;
	// each carries an Available predicate so it is hidden from tools/list when the
	// automation plugin is absent.
	groups := []func() []MCPTool{
		p.getPostTools,
		p.getChannelTools,
		p.getTeamTools,
		p.getSearchTools,
		p.getFileTools,
		p.getAgentTools,
		p.getAutomationTools,
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
	availability := map[string]func() bool{}
	for _, mcpTool := range p.mcpTools() {
		p.registerDynamicTool(mcpServer, mcpTool)
		if mcpTool.Available != nil {
			availability[mcpTool.Name] = mcpTool.Available
		}
	}

	// Hide tools whose Available predicate currently returns false on each
	// tools/list request (e.g. automation tools when the plugin is absent).
	mcpServer.AddReceivingMiddleware(toolAvailabilityMiddleware(availability))
}

// toolAvailabilityMiddleware returns MCP receiving middleware that drops any tool
// from tools/list whose Available predicate reports it as unavailable. Each
// distinct predicate is evaluated at most once per request, so tools that share
// a predicate (e.g. all automation tools) trigger a single probe rather than one
// per tool.
func toolAvailabilityMiddleware(availability map[string]func() bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return result, err
			}
			listResult, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}

			// Memoize each distinct predicate (keyed by its code pointer) for the
			// duration of this filtering pass.
			cache := map[uintptr]bool{}
			isAvailable := func(predicate func() bool) bool {
				key := reflect.ValueOf(predicate).Pointer()
				if v, cached := cache[key]; cached {
					return v
				}
				v := predicate()
				cache[key] = v
				return v
			}

			filtered := make([]*mcp.Tool, 0, len(listResult.Tools))
			for _, tool := range listResult.Tools {
				if available, gated := availability[tool.Name]; gated && !isAvailable(available) {
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
	for t != nil && t.Kind() == reflect.Ptr {
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
