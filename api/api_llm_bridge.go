// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"cmp"
	stdcontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// convertBridgePostsToInternal converts bridge posts to internal llm posts.
func (a *API) convertBridgePostsToInternal(req bridgeclient.CompletionRequest) ([]llm.Post, error) {
	posts := make([]llm.Post, len(req.Posts))

	for i, apiPost := range req.Posts {
		// Convert role
		var role llm.PostRole
		switch strings.ToLower(apiPost.Role) {
		case "user":
			role = llm.PostRoleUser
		case "assistant", "bot":
			role = llm.PostRoleBot
		case "system":
			role = llm.PostRoleSystem
		default:
			return nil, fmt.Errorf("invalid role: %s", apiPost.Role)
		}

		// Convert files
		var files []llm.File
		if len(apiPost.FileIDs) > 0 {
			files = make([]llm.File, len(apiPost.FileIDs))
			for j, fileID := range apiPost.FileIDs {
				if fileID == "" {
					return nil, fmt.Errorf("file ID cannot be empty for file %d in post %d", j, i)
				}

				// Get file info
				fileInfo, err := a.mmClient.GetFileInfo(fileID)
				if err != nil {
					return nil, fmt.Errorf("failed to get file info for file ID %s: %w", fileID, err)
				}
				if !llm.IsSupportedImageMimeType(fileInfo.MimeType) {
					files[j] = llm.File{
						MimeType: fileInfo.MimeType,
						Size:     fileInfo.Size,
					}
					continue
				}

				// Get file reader
				fileReader, err := a.mmClient.GetFile(fileID)
				if err != nil {
					return nil, fmt.Errorf("failed to get file for file ID %s: %w", fileID, err)
				}
				data, err := io.ReadAll(fileReader)
				if closeErr := fileReader.Close(); closeErr != nil {
					a.mmClient.LogError("failed to close bridge file reader", "error", closeErr)
				}
				if err != nil {
					return nil, fmt.Errorf("failed to read file for file ID %s: %w", fileID, err)
				}

				files[j] = llm.File{
					MimeType: fileInfo.MimeType,
					Size:     fileInfo.Size,
					Data:     data,
					Reader:   bytes.NewReader(data),
				}
			}
		}

		posts[i] = llm.Post{
			Role:    role,
			Message: apiPost.Message,
			Files:   files,
		}
	}

	return posts, nil
}

// convertServiceBridgeRequestToInternal converts a service bridge request to an
// internal llm.CompletionRequest. The service path has no agent, so the request
// carries no bot identity.
func (a *API) convertServiceBridgeRequestToInternal(req bridgeclient.CompletionRequest, operation, operationSubType string) (llm.CompletionRequest, error) {
	posts, err := a.convertBridgePostsToInternal(req)
	if err != nil {
		return llm.CompletionRequest{}, err
	}

	llmContext := a.buildServiceBridgeContext(req)

	resolvedOperation := operation
	if req.Operation != "" {
		resolvedOperation = req.Operation
	}
	resolvedOperationSubType := operationSubType
	if req.OperationSubType != "" {
		resolvedOperationSubType = req.OperationSubType
	}

	return llm.CompletionRequest{
		Posts:            posts,
		Context:          llmContext,
		Operation:        resolvedOperation,
		OperationSubType: resolvedOperationSubType,
	}, nil
}

// buildServiceBridgeContext builds the LLM context for a service bridge
// request. user_id and channel_id are attribution only: they name the principal
// the calling plugin is acting for, and no bot fields are set because no agent
// is involved.
func (a *API) buildServiceBridgeContext(req bridgeclient.CompletionRequest) *llm.Context {
	var context *llm.Context
	if a.contextBuilder != nil {
		context = llm.NewContext(
			a.contextBuilder.WithLLMContextServerInfo(),
			a.contextBuilder.WithLLMContextNoTools(),
		)
	} else {
		context = llm.NewContext()
	}

	if req.UserID != "" {
		context.RequestingUser = &model.User{Id: req.UserID}
	}
	if req.ChannelID != "" {
		channel, err := a.pluginAPI.Channel.Get(req.ChannelID)
		if err != nil {
			a.pluginAPI.Log.Warn("failed to get channel for bridge context; using channel ID only", "channel_id", req.ChannelID, "error", err)
			context.Channel = &model.Channel{Id: req.ChannelID}
		} else {
			context.Channel = channel
			if channel.TeamId != "" && channel.Type != model.ChannelTypeDirect && channel.Type != model.ChannelTypeGroup {
				context.Team = &model.Team{Id: channel.TeamId}
			}
		}
	}

	return context
}

func (a *API) convertAgentBridgeRequestToInternal(ctx stdcontext.Context, bot *bots.Bot, req bridgeclient.CompletionRequest, includeTools bool, operation, operationSubType string) (llm.CompletionRequest, error) {
	posts, err := a.convertBridgePostsToInternal(req)
	if err != nil {
		return llm.CompletionRequest{}, err
	}

	bridgeContext := llm.NewContext()
	bridgeContext.RequestingUser = &model.User{Id: req.UserID}
	if includeTools && a.contextBuilder != nil {
		a.contextBuilder.WithLLMContextConcreteTools(ctx, bot)(bridgeContext)
	}

	resolvedOperation := operation
	if req.Operation != "" {
		resolvedOperation = req.Operation
	}
	resolvedOperationSubType := operationSubType
	if req.OperationSubType != "" {
		resolvedOperationSubType = req.OperationSubType
	}

	return llm.CompletionRequest{
		Posts:            posts,
		Context:          bridgeContext,
		Operation:        resolvedOperation,
		OperationSubType: resolvedOperationSubType,
	}, nil
}

func normalizeAllowedToolNames(rawNames []string) ([]string, error) {
	if rawNames == nil {
		return nil, nil
	}
	if len(rawNames) == 0 {
		return nil, errors.New("allowed_tools cannot be empty when provided")
	}

	seen := make(map[string]struct{}, len(rawNames))
	out := make([]string, 0, len(rawNames))
	for _, name := range rawNames {
		if name == "" {
			return nil, errors.New("allowed_tools entries must be non-empty strings")
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out, nil
}

// bridgeAllowlistToolEligible is true for MCP and embedded tools (non-empty ServerOrigin).
// Built-in plugin tools use an empty ServerOrigin; they are excluded from bridge discovery
// and allowed_tools so they cannot be auto-run via the bridge.
func bridgeAllowlistToolEligible(serverOrigin string) bool {
	return serverOrigin != ""
}

// validateAgentParam is gin middleware that validates the :agent path parameter.
func (a *API) validateAgentParam(c *gin.Context) {
	agent := c.Param("agent")
	if agent == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "agent parameter is required",
		})
		return
	}
	if err := bridgeclient.ValidateID(agent); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid agent ID: %v", err),
		})
		return
	}
}

// validateUserIDQuery is gin middleware that validates the optional user_id query parameter.
func (a *API) validateUserIDQuery(c *gin.Context) {
	userID := c.Query("user_id")
	if userID != "" {
		if err := bridgeclient.ValidateID(userID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
				Error: fmt.Sprintf("invalid user_id: %v", err),
			})
			return
		}
	}
}

// validateCompletionRequestIDs validates optional user_id and channel_id fields
// in the request body after JSON parsing.
func validateCompletionRequestIDs(req bridgeclient.CompletionRequest) (int, error) {
	if req.UserID != "" {
		if err := bridgeclient.ValidateID(req.UserID); err != nil {
			return http.StatusBadRequest, fmt.Errorf("invalid user_id: %w", err)
		}
	}
	if req.ChannelID != "" {
		if err := bridgeclient.ValidateID(req.ChannelID); err != nil {
			return http.StatusBadRequest, fmt.Errorf("invalid channel_id: %w", err)
		}
	}
	return 0, nil
}

func (a *API) prepareAgentBridgeCompletion(
	ctx stdcontext.Context,
	agent string,
	req bridgeclient.CompletionRequest,
	pluginID string,
	operation, operationSubType string,
) (*bots.Bot, llm.CompletionRequest, []llm.LanguageModelOption, func(llm.ToolCall) bool, []string, int, error) {
	var beforeHookKeys []string
	success := false
	defer func() {
		if !success {
			a.cleanupBeforeHookKeys(beforeHookKeys)
		}
	}()

	if statusCode, err := validateCompletionRequestIDs(req); err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, statusCode, err
	}

	normalizedPluginID := strings.TrimSpace(pluginID)
	if len(req.ToolHooks) > 0 && normalizedPluginID == "" {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("tool_hooks requires Mattermost-Plugin-ID header")
	}
	if len(req.ToolHooks) > 0 && req.UserID == "" {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("tool_hooks requires user_id")
	}

	allowedToolNames, err := normalizeAllowedToolNames(req.AllowedTools)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf("invalid allowed_tools: %w", err)
	}
	if allowedToolNames != nil && req.UserID == "" {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("allowed_tools requires user_id")
	}

	bot, err := a.getBotByAgent(agent)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusNotFound, err
	}

	err = a.checkBridgePermissions(req.UserID, req.ChannelID, bot)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusForbidden, fmt.Errorf("permission denied: %v", err)
	}

	toolsRequested := allowedToolNames != nil
	llmRequest, err := a.convertAgentBridgeRequestToInternal(ctx, bot, req, toolsRequested, operation, operationSubType)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf("invalid request: %v", err)
	}

	if len(req.ToolHooks) > 0 && !toolsRequested {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("tool_hooks requires allowed_tools")
	}

	// Normalize tool_hooks keys to bare names so they match the embedded MCP
	// server, which registers and runs before-hooks by bare name. Callers may key
	// hooks by either the bare or namespaced form; both collapse to the same bare
	// key. Keys are issued lazily per scoped tool below, tracked in beforeHookKeys
	// so the deferred cleanup runs even on later failures.
	//
	// Reject two distinct keys (e.g. "search_posts" and "mattermost__search_posts")
	// that collapse to the same bare name: silently keeping one would be
	// non-deterministic. Each tool may be hooked once.
	hooksByBareName := make(map[string]bridgeclient.ToolHookConfig, len(req.ToolHooks))
	hookKeyByBareName := make(map[string]string, len(req.ToolHooks))
	for name, cfg := range req.ToolHooks {
		bare := llm.BareMCPToolName(name)
		if existing, ok := hookKeyByBareName[bare]; ok {
			return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf("tool_hooks has conflicting entries %q and %q for the same tool; specify it once", existing, name)
		}
		hookKeyByBareName[bare] = name
		hooksByBareName[bare] = cfg
	}

	autoRunNames := make(map[string]struct{})
	if toolsRequested {
		if bot.GetConfig().DisableTools {
			return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("agent has tools disabled")
		}

		if llmRequest.Context.Tools == nil || len(llmRequest.Context.Tools.GetTools()) == 0 {
			return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, errors.New("no eligible tools available for this agent")
		}

		scopedTools := llm.NewToolStore()
		for _, name := range allowedToolNames {
			// GetTool resolves exact namespaced names and unique bare names. A bare
			// name that collides across servers returns nil here; the caller must
			// pass the namespaced name to disambiguate.
			tool := llmRequest.Context.Tools.GetTool(name)
			if tool == nil {
				return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf("tool %q is not eligible or not available for this agent", name)
			}
			if !bridgeAllowlistToolEligible(tool.ServerOrigin) {
				return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf(
					"tool %q is not eligible for bridge allowed_tools (built-in tools cannot be allowlisted; use MCP or embedded tools from GET .../agents/{id}/tools only)",
					name,
				)
			}

			scopedTool := *tool
			// Bind a before-hook (if the caller registered one for this tool) keyed
			// by the resolved tool's bare name, so it matches the embedded server's
			// bare lookup at execution time regardless of the name form the caller
			// passed.
			bare := llm.BareMCPToolName(scopedTool.Name)
			if cfg, ok := hooksByBareName[bare]; ok && cfg.BeforeCallback != "" {
				beforeHookKey, hookErr := a.beforeHookStore.Issue(req.UserID, bare, normalizedPluginID, cfg.BeforeCallback)
				if hookErr != nil {
					statusCode := http.StatusInternalServerError
					if errors.Is(hookErr, mcp.ErrInvalidBeforeHookConfig) {
						statusCode = http.StatusBadRequest
					}
					return nil, llm.CompletionRequest{}, nil, nil, nil, statusCode, fmt.Errorf("invalid tool_hooks: %w", hookErr)
				}
				beforeHookKeys = append(beforeHookKeys, beforeHookKey)
				// Wire format on the MCP server side keys hooks by tool name (see
				// mcpserver/tools/provider.go decodeToolHooksFromMetadata).
				scopedTool = scopedTool.WithCallMetadata(map[string]any{
					"tool_hooks": map[string]any{
						bare: map[string]any{"before_hook_key": beforeHookKey},
					},
				})
			}

			scopedTools.AddTools([]llm.Tool{scopedTool})
			autoRunNames[scopedTool.Name] = struct{}{}
		}
		llmRequest.Context.Tools = scopedTools
	}

	opts, err := a.convertRequestToLLMOptions(req)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, nil, nil, http.StatusBadRequest, fmt.Errorf("invalid options: %v", err)
	}

	if !toolsRequested {
		opts = append(opts, llm.WithToolsDisabled())
	}

	// Enable native web search if the bot supports it.
	// Native web search is a provider-level feature (not an MCP tool),
	// so it's not part of allowed_tools — it's always available when configured.
	if bot.HasNativeWebSearchEnabled() {
		opts = append(opts, llm.WithNativeWebSearchAllowed())
	}

	// Build the auto-run predicate from the explicit allowlist. Returning nil
	// when no tools are eligible keeps the response loop using the direct
	// ChatCompletion path so the caller is responsible for tool execution.
	//
	// We key on tool name only because the underlying ToolStore is itself
	// keyed by name (`map[string]Tool`), so the scoped store cannot hold two
	// tools with the same name from different servers. If that ever changes,
	// the bridge protocol must also grow a way for callers to qualify tools
	// by origin, and this predicate becomes a composite-key check at the
	// same time.
	var shouldExecute func(llm.ToolCall) bool
	if len(autoRunNames) > 0 {
		shouldExecute = func(tc llm.ToolCall) bool {
			_, ok := autoRunNames[tc.Name]
			return ok
		}
	}

	success = true
	return bot, llmRequest, opts, shouldExecute, beforeHookKeys, 0, nil
}

func (a *API) cleanupBeforeHookKeys(keys []string) {
	for _, key := range keys {
		if err := a.beforeHookStore.Delete(key); err != nil {
			a.pluginAPI.Log.Warn("failed to clean up before-hook key", "error", err)
		}
	}
}

// convertRequestToLLMOptions converts the API request options to llm.LanguageModelOption
func (a *API) convertRequestToLLMOptions(req bridgeclient.CompletionRequest) ([]llm.LanguageModelOption, error) {
	var options []llm.LanguageModelOption

	// Add MaxGeneratedTokens option if provided
	if req.MaxGeneratedTokens != 0 {
		options = append(options, llm.WithMaxGeneratedTokens(req.MaxGeneratedTokens))
	}

	// Add JSONOutputFormat option if provided
	if len(req.JSONOutputFormat) > 0 {
		// Convert map to *jsonschema.Schema
		schemaJSON, err := json.Marshal(req.JSONOutputFormat)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal JSON schema: %w", err)
		}

		var schema jsonschema.Schema
		if err := json.Unmarshal(schemaJSON, &schema); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON schema: %w", err)
		}

		// Add the option directly using a closure
		options = append(options, func(cfg *llm.LanguageModelConfig) {
			cfg.JSONOutputFormat = &schema
		})
	}

	return options, nil
}

// getBotByAgent finds a bot by its Bot ID
func (a *API) getBotByAgent(agent string) (*bots.Bot, error) {
	bot := a.bots.GetBotByID(agent)
	if bot == nil {
		return nil, fmt.Errorf("bot not found with ID: %s", agent)
	}
	return bot, nil
}

// resolveBridgeService resolves a bridge `:service` path value against a
// snapshot of the stored service configuration.
//
// An exact ID match anywhere in the snapshot wins over a name match, so a
// service can always be addressed unambiguously by ID even when another
// service's name happens to equal that ID. Within each pass the first entry in
// configuration order wins, which is how duplicate IDs and duplicate names
// resolve. A service stored with a blank name is only ever reachable by ID.
//
// Both passes see only the effective configuration: for duplicate IDs the
// first entry wins everywhere else (discovery, fallback resolution, the LLM
// registry, config lookup), so a later entry shadowed by ID is not reachable
// through its otherwise-unique name either. Resolving it would execute a
// configuration callers cannot discover and combine it with the first entry's
// fallback chain.
func resolveBridgeService(services []llm.ServiceConfig, value string) (llm.ServiceConfig, bool) {
	if value == "" {
		return llm.ServiceConfig{}, false
	}

	effective := make([]llm.ServiceConfig, 0, len(services))
	seenIDs := make(map[string]struct{}, len(services))
	for _, svc := range services {
		if _, shadowed := seenIDs[svc.ID]; shadowed {
			continue
		}
		seenIDs[svc.ID] = struct{}{}
		effective = append(effective, svc)
	}

	for _, svc := range effective {
		if svc.ID == value {
			return svc, true
		}
	}
	for _, svc := range effective {
		if svc.Name == value {
			return svc, true
		}
	}
	return llm.ServiceConfig{}, false
}

// prepareServiceBridgeCompletion validates a service bridge completion request,
// resolves the requested service from stored configuration, and leases a
// language model for it. On failure it writes the error response and reports
// ok=false; on success the caller owns release and must call it exactly once,
// after the response has been fully produced.
//
// There is deliberately no user or channel permission check here: agent ACLs
// describe who may talk to an agent, and the service endpoints have no agent.
// Inter-plugin trust (the Mattermost-Plugin-ID header) is the boundary, and
// user_id / channel_id are attribution only.
func (a *API) prepareServiceBridgeCompletion(c *gin.Context, operationSubType string) (llm.LanguageModel, llm.CompletionRequest, []llm.LanguageModelOption, func(), bool) {
	fail := func(statusCode int, message string) (llm.LanguageModel, llm.CompletionRequest, []llm.LanguageModelOption, func(), bool) {
		c.JSON(statusCode, bridgeclient.ErrorResponse{Error: message})
		return nil, llm.CompletionRequest{}, nil, nil, false
	}

	service := c.Param("service")
	if service == "" {
		return fail(http.StatusBadRequest, "service parameter is required")
	}

	var req bridgeclient.CompletionRequest
	if err := c.BindJSON(&req); err != nil {
		return fail(http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
	}

	if len(req.Posts) == 0 {
		return fail(http.StatusBadRequest, "posts array cannot be empty")
	}

	if req.AllowedTools != nil {
		return fail(http.StatusBadRequest, "allowed_tools is only supported for agent completion endpoints")
	}

	if statusCode, err := validateCompletionRequestIDs(req); err != nil {
		return fail(statusCode, err.Error())
	}

	// One snapshot serves the whole request so resolution and the fallback
	// chain cannot disagree about the configuration they were built from.
	services := a.config.GetServices()

	primary, found := resolveBridgeService(services, service)
	if !found || !bots.ServiceCanServeCompletions(primary) {
		return fail(http.StatusNotFound, fmt.Sprintf("service not found: %s", service))
	}

	// Converting the request before leasing a model keeps a malformed role,
	// file, or JSON schema a 400 that never allocates a provider client.
	llmRequest, err := a.convertServiceBridgeRequestToInternal(req, llm.OperationBridgeService, operationSubType)
	if err != nil {
		return fail(http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
	}

	opts, err := a.convertRequestToLLMOptions(req)
	if err != nil {
		return fail(http.StatusBadRequest, fmt.Sprintf("invalid options: %v", err))
	}
	opts = append(opts, llm.WithToolsDisabled())

	// The primary is configured and eligible, so a broken chain is a server
	// configuration problem rather than a missing service.
	fallbacks, err := bots.ResolveBridgeFallbacks(services, primary)
	if err != nil {
		return fail(http.StatusInternalServerError, fmt.Sprintf("service %q is not usable: %v", primary.ID, err))
	}

	model, release, err := a.bots.AcquireServiceLLM(primary, fallbacks)
	if err != nil {
		return fail(http.StatusInternalServerError, fmt.Sprintf("failed to build a language model for service %q: %v", primary.ID, err))
	}

	return model, llmRequest, opts, release, true
}

// checkBridgePermissions checks usage restrictions based on the provided UserID and ChannelID.
// Returns nil if checks pass or should be skipped.
// Permission checking behavior:
//   - Both UserID and ChannelID empty: no checks performed (backward compatibility)
//   - UserID only: checks user-level permissions
//   - Both provided: checks both user and channel-level permissions
func (a *API) checkBridgePermissions(userID, channelID string, bot *bots.Bot) error {
	// If no user ID provided, skip permission checks
	if userID == "" {
		return nil
	}

	// If only user ID provided, check user permissions
	if channelID == "" {
		return a.bots.CheckUsageRestrictionsForUser(bot, userID)
	}

	// Both user ID and channel ID provided, check full permissions
	channel, err := a.pluginAPI.Channel.Get(channelID)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}

	return a.bots.CheckUsageRestrictions(userID, bot, channel)
}

func drainToolRunnerStream(stream *llm.TextStreamResult) error {
	var firstErr error
	for event := range stream.Stream {
		if event.Type != llm.EventTypeError {
			continue
		}
		if firstErr != nil {
			continue
		}
		if e, ok := event.Value.(error); ok {
			firstErr = e
			continue
		}
		if s, ok := event.Value.(string); ok {
			firstErr = errors.New(s)
			continue
		}
		firstErr = errors.New("tool runner stream failed")
	}
	return firstErr
}

// streamLLMResponse handles streaming LLM responses as Server-Sent Events.
// When shouldExecute is non-nil, the stream is wrapped in a toolrunner bounded
// by maxToolTurns so allowlisted tool calls are auto-executed and their results
// fed back to the LLM until the model produces a final text response.
// maxToolTurns is unused when shouldExecute is nil.
func (a *API) streamLLMResponse(c *gin.Context, model llm.LanguageModel, maxToolTurns int, llmRequest llm.CompletionRequest, shouldExecute func(llm.ToolCall) bool, opts ...llm.LanguageModelOption) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	var streamResult *llm.TextStreamResult
	var err error
	if shouldExecute != nil {
		var runResult *toolrunner.ToolRunResult
		runResult, err = toolrunner.New(model, toolrunner.WithMaxRounds(maxToolTurns)).Run(c.Request.Context(), llmRequest, shouldExecute, nil, opts...)
		if runResult != nil {
			streamResult = runResult.Stream
		}
	} else {
		streamResult, err = model.ChatCompletion(c.Request.Context(), llmRequest, opts...)
	}
	if err != nil {
		// If streaming hasn't started, we can still send a JSON error
		errorEvent := llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: err.Error(),
		}
		eventJSON, _ := json.Marshal(errorEvent)
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(eventJSON))
		return
	}

	// Stream the response as JSON-encoded events. Whatever is left on the
	// stream is drained on the way out: the caller releases its lease on the
	// model as soon as this returns, so a producer still blocked on an unread
	// send would be left holding a provider client that is being shut down.
	defer drainStream(streamResult)

	for event := range streamResult.Stream {
		// Convert the event to JSON
		eventJSON, err := json.Marshal(event)
		if err != nil {
			errorEvent := llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: fmt.Sprintf("Error marshaling event: %v", err),
			}
			errorJSON, _ := json.Marshal(errorEvent)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(errorJSON))
			continue
		}

		fmt.Fprintf(c.Writer, "data: %s\n\n", string(eventJSON))

		if event.Type == llm.EventTypeEnd || event.Type == llm.EventTypeError {
			break
		}
	}
}

// drainStream discards whatever is left on the stream so its producer always
// runs to completion.
func drainStream(stream *llm.TextStreamResult) {
	for range stream.Stream { //nolint:revive // empty-block: the remaining events are deliberately discarded
	}
}

// When shouldExecute is non-nil, the call is routed through a toolrunner
// bounded by maxToolTurns so allowlisted tool calls are auto-executed; the
// runner's text stream is drained into a single concatenated string before
// responding, mirroring what ChatCompletionNoStream would have produced.
// maxToolTurns is unused when shouldExecute is nil.
func (a *API) handleNonStreamingLLMResponse(c *gin.Context, model llm.LanguageModel, maxToolTurns int, llmRequest llm.CompletionRequest, shouldExecute func(llm.ToolCall) bool, opts ...llm.LanguageModelOption) {
	if shouldExecute == nil {
		response, err := model.ChatCompletionNoStream(c.Request.Context(), llmRequest, opts...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, bridgeclient.ErrorResponse{
				Error: fmt.Sprintf("failed to complete LLM request: %v", err),
			})
			return
		}
		c.JSON(http.StatusOK, bridgeclient.CompletionResponse{
			Completion: response,
		})
		return
	}

	runResult, err := toolrunner.New(model, toolrunner.WithMaxRounds(maxToolTurns)).Run(c.Request.Context(), llmRequest, shouldExecute, nil, opts...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("failed to complete LLM request: %v", err),
		})
		return
	}

	if streamErr := drainToolRunnerStream(runResult.Stream); streamErr != nil {
		c.JSON(http.StatusInternalServerError, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("failed to complete LLM request: %v", streamErr),
		})
		return
	}

	c.JSON(http.StatusOK, bridgeclient.CompletionResponse{
		Completion: runResult.FinalText,
	})
}

// handleGetAgents returns all available agents, optionally filtered by user permissions
func (a *API) handleGetAgents(c *gin.Context) {
	userID := c.Query("user_id")

	allBots := a.bots.GetAllBots()
	agents := make([]bridgeclient.BridgeAgentInfo, 0, len(allBots))
	defaultBotName := a.config.GetDefaultBotName()

	for _, bot := range allBots {
		// If user_id is provided, filter by permissions
		if userID != "" {
			if err := a.bots.CheckUsageRestrictionsForUser(bot, userID); err != nil {
				continue
			}
		}

		service := bot.GetService()
		agents = append(agents, bridgeclient.BridgeAgentInfo{
			ID:          bot.GetMMBot().UserId,
			DisplayName: bot.GetMMBot().DisplayName,
			Username:    bot.GetMMBot().Username,
			ServiceID:   service.ID,
			ServiceType: service.Type,
			IsDefault:   bot.GetMMBot().Username == defaultBotName,
		})
	}

	sort.Slice(agents, func(i, j int) bool {
		if agents[i].DisplayName == agents[j].DisplayName {
			return agents[i].ID < agents[j].ID
		}
		return agents[i].DisplayName < agents[j].DisplayName
	})

	c.JSON(http.StatusOK, bridgeclient.AgentsResponse{
		Agents: agents,
	})
}

// handleGetAgentTools returns bridge-eligible tools for a specific agent.
// Only tools that are eligible for allowed_tools execution are returned (MCP and
// embedded tools with a non-empty ServerOrigin; built-in tools are omitted).
func (a *API) handleGetAgentTools(c *gin.Context) {
	agent := c.Param("agent")
	userID := c.Query("user_id")

	bot, err := a.getBotByAgent(agent)
	if err != nil {
		c.JSON(http.StatusNotFound, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	if userID != "" {
		err = a.bots.CheckUsageRestrictionsForUser(bot, userID)
		if err != nil {
			c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
				Error: fmt.Sprintf("permission denied: %v", err),
			})
			return
		}
	}

	if bot.GetConfig().DisableTools {
		c.JSON(http.StatusOK, bridgeclient.AgentToolsResponse{
			Tools: []bridgeclient.BridgeToolInfo{},
		})
		return
	}

	// Build a minimal context just to resolve the bot's available tools.
	toolContext := llm.NewContext()
	toolContext.RequestingUser = &model.User{Id: userID}
	if a.contextBuilder != nil {
		a.contextBuilder.WithLLMContextConcreteTools(c.Request.Context(), bot)(toolContext)
	}

	var tools []bridgeclient.BridgeToolInfo
	if toolContext.Tools != nil {
		for _, info := range toolContext.Tools.GetToolsInfo() {
			if !bridgeAllowlistToolEligible(info.ServerOrigin) {
				continue
			}
			tools = append(tools, bridgeclient.BridgeToolInfo{
				Name:         info.Name,
				BareName:     llm.BareMCPToolName(info.Name),
				Description:  info.Description,
				ServerOrigin: info.ServerOrigin,
			})
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Name != tools[j].Name {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].ServerOrigin < tools[j].ServerOrigin
	})

	c.JSON(http.StatusOK, bridgeclient.AgentToolsResponse{
		Tools: tools,
	})
}

// handleGetServices returns every stored service that can serve a bridge
// completion. Services are configuration, not agents: the optional user_id
// query parameter is accepted and syntax-validated by middleware, but the list
// is not filtered by agent ACLs.
func (a *API) handleGetServices(c *gin.Context) {
	snapshot := a.config.GetServices()

	seen := make(map[string]struct{}, len(snapshot))
	services := make([]bridgeclient.BridgeServiceInfo, 0, len(snapshot))
	for _, svc := range snapshot {
		// Duplicate IDs resolve to the first configured entry, matching how
		// completion requests resolve the same ID.
		if _, exists := seen[svc.ID]; exists {
			continue
		}
		seen[svc.ID] = struct{}{}

		if !bots.ServiceCanServeCompletions(svc) {
			continue
		}
		// A service whose fallback chain is broken would fail every call, so
		// advertising it would only produce confusing errors later.
		if _, err := bots.ResolveBridgeFallbacks(snapshot, svc); err != nil {
			continue
		}

		services = append(services, bridgeclient.BridgeServiceInfo{
			ID:   svc.ID,
			Name: svc.Name,
			Type: svc.Type,
		})
	}

	slices.SortFunc(services, func(a, b bridgeclient.BridgeServiceInfo) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})

	c.JSON(http.StatusOK, bridgeclient.ServicesResponse{
		Services: services,
	})
}

// handleAgentCompletionStreaming handles streaming completion requests for a specific agent
func (a *API) handleAgentCompletionStreaming(c *gin.Context) {
	agent := c.Param("agent")

	var req bridgeclient.CompletionRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	if len(req.Posts) == 0 {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "posts array cannot be empty",
		})
		return
	}

	bot, llmRequest, opts, shouldExecute, beforeHookKeys, statusCode, err := a.prepareAgentBridgeCompletion(c.Request.Context(), agent, req, c.GetHeader("Mattermost-Plugin-ID"), llm.OperationBridgeAgent, llm.SubTypeStreaming)
	if err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}
	defer a.cleanupBeforeHookKeys(beforeHookKeys)

	a.streamLLMResponse(c, bot.LLM(), bot.GetConfig().EffectiveMaxToolTurns(), llmRequest, shouldExecute, opts...)
}

// handleAgentCompletionNoStream handles non-streaming completion requests for a specific agent
func (a *API) handleAgentCompletionNoStream(c *gin.Context) {
	agent := c.Param("agent")

	var req bridgeclient.CompletionRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request body: %v", err),
		})
		return
	}

	if len(req.Posts) == 0 {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "posts array cannot be empty",
		})
		return
	}

	bot, llmRequest, opts, shouldExecute, beforeHookKeys, statusCode, err := a.prepareAgentBridgeCompletion(c.Request.Context(), agent, req, c.GetHeader("Mattermost-Plugin-ID"), llm.OperationBridgeAgent, llm.SubTypeNoStream)
	if err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}
	defer a.cleanupBeforeHookKeys(beforeHookKeys)

	a.handleNonStreamingLLMResponse(c, bot.LLM(), bot.GetConfig().EffectiveMaxToolTurns(), llmRequest, shouldExecute, opts...)
}

// handleServiceCompletionStreaming handles streaming completion requests for a specific service
func (a *API) handleServiceCompletionStreaming(c *gin.Context) {
	model, llmRequest, opts, release, ok := a.prepareServiceBridgeCompletion(c, llm.SubTypeStreaming)
	if !ok {
		return
	}
	// streamLLMResponse drains the provider stream synchronously, so the lease
	// covers the whole response.
	defer release()

	a.streamLLMResponse(c, model, 0, llmRequest, nil, opts...)
}

// handleServiceCompletionNoStream handles non-streaming completion requests for a specific service
func (a *API) handleServiceCompletionNoStream(c *gin.Context) {
	model, llmRequest, opts, release, ok := a.prepareServiceBridgeCompletion(c, llm.SubTypeNoStream)
	if !ok {
		return
	}
	defer release()

	a.handleNonStreamingLLMResponse(c, model, 0, llmRequest, nil, opts...)
}
