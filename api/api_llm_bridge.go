// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
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

				// Get file reader
				fileReader, err := a.mmClient.GetFile(fileID)
				if err != nil {
					return nil, fmt.Errorf("failed to get file for file ID %s: %w", fileID, err)
				}

				files[j] = llm.File{
					MimeType: fileInfo.MimeType,
					Size:     fileInfo.Size,
					Reader:   fileReader,
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

// convertLLMBridgeRequestToInternal converts the API request format to internal llm.CompletionRequest
func (a *API) convertLLMBridgeRequestToInternal(bot *bots.Bot, req bridgeclient.CompletionRequest, operation, operationSubType string) (llm.CompletionRequest, error) {
	posts, err := a.convertBridgePostsToInternal(req)
	if err != nil {
		return llm.CompletionRequest{}, err
	}

	llmContext, err := a.buildLLMBridgeContext(bot, req)
	if err != nil {
		return llm.CompletionRequest{}, err
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
		Context:          llmContext,
		Operation:        resolvedOperation,
		OperationSubType: resolvedOperationSubType,
	}, nil
}

// buildLLMBridgeContext builds the LLM context for bridge requests (service path).
func (a *API) buildLLMBridgeContext(bot *bots.Bot, req bridgeclient.CompletionRequest) (*llm.Context, error) {
	var context *llm.Context
	if a.contextBuilder != nil {
		context = llm.NewContext(
			a.contextBuilder.WithLLMContextServerInfo(),
			a.contextBuilder.WithLLMContextBot(bot),
			a.contextBuilder.WithLLMContextNoTools(),
		)
	} else {
		context = llm.NewContext()
		if bot != nil {
			var botUserID string
			if mmBot := bot.GetMMBot(); mmBot != nil {
				botUserID = mmBot.UserId
			}
			context.SetBotFields(bot.GetConfig().DisplayName, bot.GetConfig().Name, botUserID, bot.GetService().DefaultModel, bot.GetService().Type, bot.GetConfig().CustomInstructions)
		}
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

	return context, nil
}

func (a *API) convertAgentBridgeRequestToInternal(bot *bots.Bot, req bridgeclient.CompletionRequest, includeTools bool, operation, operationSubType string) (llm.CompletionRequest, error) {
	posts, err := a.convertBridgePostsToInternal(req)
	if err != nil {
		return llm.CompletionRequest{}, err
	}

	bridgeContext := llm.NewContext()
	bridgeContext.RequestingUser = &model.User{Id: req.UserID}
	if includeTools && a.contextBuilder != nil {
		a.contextBuilder.WithLLMContextTools(bot)(bridgeContext)
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

func normalizeAllowedToolRefs(rawRefs []bridgeclient.AllowedToolRef) ([]bridgeclient.AllowedToolRef, error) {
	if rawRefs == nil {
		return nil, nil
	}
	if len(rawRefs) == 0 {
		return nil, errors.New("allowed_tools cannot be empty when provided")
	}

	seen := make(map[string]struct{}, len(rawRefs))
	out := make([]bridgeclient.AllowedToolRef, 0, len(rawRefs))
	for _, ref := range rawRefs {
		if ref.Name == "" {
			return nil, errors.New("allowed_tools entries must have a non-empty name")
		}
		key := llm.ToolAutoRunKey(ref.ServerOrigin, ref.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}

	return out, nil
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
	agent string,
	req bridgeclient.CompletionRequest,
	operation, operationSubType string,
) (*bots.Bot, llm.CompletionRequest, []llm.LanguageModelOption, int, error) {
	if statusCode, err := validateCompletionRequestIDs(req); err != nil {
		return nil, llm.CompletionRequest{}, nil, statusCode, err
	}

	allowedRefs, err := normalizeAllowedToolRefs(req.AllowedTools)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, fmt.Errorf("invalid allowed_tools: %w", err)
	}

	bot, err := a.getBotByAgent(agent)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, http.StatusNotFound, err
	}

	err = a.checkBridgePermissions(req.UserID, req.ChannelID, bot)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, http.StatusForbidden, fmt.Errorf("permission denied: %v", err)
	}

	toolsRequested := allowedRefs != nil
	llmRequest, err := a.convertAgentBridgeRequestToInternal(bot, req, toolsRequested, operation, operationSubType)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, fmt.Errorf("invalid request: %v", err)
	}

	var autoRunKeys []string
	if toolsRequested {
		if bot.GetConfig().DisableTools {
			return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, errors.New("agent has tools disabled")
		}

		if llmRequest.Context.Tools == nil || len(llmRequest.Context.Tools.GetTools()) == 0 {
			return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, errors.New("no eligible tools available for this agent")
		}

		scopedTools := llm.NewToolStore(nil, false)
		for _, ref := range allowedRefs {
			tool := llmRequest.Context.Tools.GetTool(ref.Name)
			if tool == nil {
				return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, fmt.Errorf("tool %q is not eligible or not available for this agent", ref.Name)
			}
			if tool.ServerOrigin != ref.ServerOrigin {
				return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, fmt.Errorf(
					"allowed_tools server_origin for tool %q does not match (got %q, expected %q)",
					ref.Name, ref.ServerOrigin, tool.ServerOrigin,
				)
			}
			scopedTools.AddTools([]llm.Tool{*tool})
			autoRunKeys = append(autoRunKeys, llm.ToolAutoRunKey(tool.ServerOrigin, tool.Name))
		}
		llmRequest.Context.Tools = scopedTools
	}

	opts, err := a.convertRequestToLLMOptions(req)
	if err != nil {
		return nil, llm.CompletionRequest{}, nil, http.StatusBadRequest, fmt.Errorf("invalid options: %v", err)
	}

	// Handle tool options
	if !toolsRequested {
		opts = append(opts, llm.WithToolsDisabled())
	} else if len(autoRunKeys) > 0 {
		opts = append(opts, llm.WithAutoRunTools(autoRunKeys))
	}

	// Enable native web search if the bot supports it.
	// Native web search is a provider-level feature (not an MCP tool),
	// so it's not part of allowed_tools — it's always available when configured.
	if bot.HasNativeWebSearchEnabled() {
		opts = append(opts, llm.WithNativeWebSearchAllowed())
	}

	return bot, llmRequest, opts, 0, nil
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

// getBotByService finds a bot that uses the specified service (by ID or name)
func (a *API) getBotByService(service string) (*bots.Bot, error) {
	for _, bot := range a.bots.GetAllBots() {
		botService := bot.GetService()
		if botService.ID == service || botService.Name == service {
			return bot, nil
		}
	}
	return nil, fmt.Errorf("no bot found for service: %s", service)
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

// streamLLMResponse handles streaming LLM responses as Server-Sent Events
func (a *API) streamLLMResponse(c *gin.Context, bot *bots.Bot, llmRequest llm.CompletionRequest, opts ...llm.LanguageModelOption) {
	// Start streaming response
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	// Make the streaming LLM call
	streamResult, err := bot.LLM().ChatCompletion(llmRequest, opts...)
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

	// Stream the response as JSON-encoded events
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

// handleNonStreamingLLMResponse handles non-streaming LLM responses.
func (a *API) handleNonStreamingLLMResponse(c *gin.Context, bot *bots.Bot, llmRequest llm.CompletionRequest, opts ...llm.LanguageModelOption) {
	response, err := bot.LLM().ChatCompletionNoStream(llmRequest, opts...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("failed to complete LLM request: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, bridgeclient.CompletionResponse{
		Completion: response,
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
// Only tools that are eligible for allowed_tools execution are returned.
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
		a.contextBuilder.WithLLMContextTools(bot)(toolContext)
	}

	var tools []bridgeclient.BridgeToolInfo
	if toolContext.Tools != nil {
		for _, info := range toolContext.Tools.GetToolsInfo() {
			tools = append(tools, bridgeclient.BridgeToolInfo{
				Name:         info.Name,
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

// handleGetServices returns all available services, optionally filtered by user permissions
func (a *API) handleGetServices(c *gin.Context) {
	userID := c.Query("user_id")

	// Get all unique services
	servicesMap := make(map[string]bridgeclient.BridgeServiceInfo)
	allBots := a.bots.GetAllBots()

	for _, bot := range allBots {
		// If user_id is provided, filter by permissions
		if userID != "" {
			if err := a.bots.CheckUsageRestrictionsForUser(bot, userID); err != nil {
				continue
			}
		}

		service := bot.GetService()
		if _, exists := servicesMap[service.ID]; !exists {
			servicesMap[service.ID] = bridgeclient.BridgeServiceInfo{
				ID:   service.ID,
				Name: service.Name,
				Type: service.Type,
			}
		}
	}

	// Convert map to slice
	services := make([]bridgeclient.BridgeServiceInfo, 0, len(servicesMap))
	for _, service := range servicesMap {
		services = append(services, service)
	}

	sort.Slice(services, func(i, j int) bool {
		if services[i].Name == services[j].Name {
			return services[i].ID < services[j].ID
		}
		return services[i].Name < services[j].Name
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

	bot, llmRequest, opts, statusCode, err := a.prepareAgentBridgeCompletion(agent, req, llm.OperationBridgeAgent, llm.SubTypeStreaming)
	if err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Stream the response
	a.streamLLMResponse(c, bot, llmRequest, opts...)
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

	bot, llmRequest, opts, statusCode, err := a.prepareAgentBridgeCompletion(agent, req, llm.OperationBridgeAgent, llm.SubTypeNoStream)
	if err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Handle non-streaming response
	a.handleNonStreamingLLMResponse(c, bot, llmRequest, opts...)
}

// handleServiceCompletionStreaming handles streaming completion requests for a specific service
func (a *API) handleServiceCompletionStreaming(c *gin.Context) {
	service := c.Param("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "service parameter is required",
		})
		return
	}

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

	if req.AllowedTools != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "allowed_tools is only supported for agent completion endpoints",
		})
		return
	}

	if statusCode, err := validateCompletionRequestIDs(req); err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Find a bot that uses the specified service (by ID or name)
	bot, err := a.getBotByService(service)
	if err != nil {
		c.JSON(http.StatusNotFound, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Check permissions if UserID/ChannelID provided
	err = a.checkBridgePermissions(req.UserID, req.ChannelID, bot)
	if err != nil {
		c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("permission denied: %v", err),
		})
		return
	}

	// Convert request to internal format
	llmRequest, err := a.convertLLMBridgeRequestToInternal(bot, req, llm.OperationBridgeService, llm.SubTypeStreaming)
	if err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	// Convert request options
	opts, err := a.convertRequestToLLMOptions(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid options: %v", err),
		})
		return
	}
	opts = append(opts, llm.WithToolsDisabled())

	// Stream the response
	a.streamLLMResponse(c, bot, llmRequest, opts...)
}

// handleServiceCompletionNoStream handles non-streaming completion requests for a specific service
func (a *API) handleServiceCompletionNoStream(c *gin.Context) {
	service := c.Param("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "service parameter is required",
		})
		return
	}

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

	if req.AllowedTools != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "allowed_tools is only supported for agent completion endpoints",
		})
		return
	}

	if statusCode, err := validateCompletionRequestIDs(req); err != nil {
		c.JSON(statusCode, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Find a bot that uses the specified service (by ID or name)
	bot, err := a.getBotByService(service)
	if err != nil {
		c.JSON(http.StatusNotFound, bridgeclient.ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Check permissions if UserID/ChannelID provided
	err = a.checkBridgePermissions(req.UserID, req.ChannelID, bot)
	if err != nil {
		c.JSON(http.StatusForbidden, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("permission denied: %v", err),
		})
		return
	}

	// Convert request to internal format
	llmRequest, err := a.convertLLMBridgeRequestToInternal(bot, req, llm.OperationBridgeService, llm.SubTypeNoStream)
	if err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	// Convert request options
	opts, err := a.convertRequestToLLMOptions(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: fmt.Sprintf("invalid options: %v", err),
		})
		return
	}
	opts = append(opts, llm.WithToolsDisabled())

	// Handle non-streaming response
	a.handleNonStreamingLLMResponse(c, bot, llmRequest, opts...)
}
