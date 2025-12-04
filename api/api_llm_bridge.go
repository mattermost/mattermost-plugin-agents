// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"
)

// convertLLMBridgeRequestToInternal converts the API request format to internal llm.CompletionRequest
func (a *API) convertLLMBridgeRequestToInternal(req bridgeclient.CompletionRequest) (llm.CompletionRequest, error) {
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
			return llm.CompletionRequest{}, fmt.Errorf("invalid role: %s", apiPost.Role)
		}

		// Convert files
		var files []llm.File
		if len(apiPost.FileIDs) > 0 {
			files = make([]llm.File, len(apiPost.FileIDs))
			for j, fileID := range apiPost.FileIDs {
				if fileID == "" {
					return llm.CompletionRequest{}, fmt.Errorf("file ID cannot be empty for file %d in post %d", j, i)
				}

				// Get file info
				fileInfo, err := a.mmClient.GetFileInfo(fileID)
				if err != nil {
					return llm.CompletionRequest{}, fmt.Errorf("failed to get file info for file ID %s: %w", fileID, err)
				}

				// Get file reader
				fileReader, err := a.mmClient.GetFile(fileID)
				if err != nil {
					return llm.CompletionRequest{}, fmt.Errorf("failed to get file for file ID %s: %w", fileID, err)
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

	return llm.CompletionRequest{
		Posts:   posts,
		Context: &llm.Context{},
	}, nil
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

	// Plugin bridge requests do not allow tools to be enabled
	options = append(options, llm.WithToolsDisabled())
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

// handleNonStreamingLLMResponse handles non-streaming LLM responses
func (a *API) handleNonStreamingLLMResponse(c *gin.Context, bot *bots.Bot, llmRequest llm.CompletionRequest, opts ...llm.LanguageModelOption) {
	// Make the non-streaming LLM call
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
		})
	}

	c.JSON(http.StatusOK, bridgeclient.AgentsResponse{
		Agents: agents,
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

	c.JSON(http.StatusOK, bridgeclient.ServicesResponse{
		Services: services,
	})
}

// handleAgentCompletionStreaming handles streaming completion requests for a specific agent
func (a *API) handleAgentCompletionStreaming(c *gin.Context) {
	agent := c.Param("agent")
	if agent == "" {
		c.JSON(http.StatusBadRequest, bridgeclient.ErrorResponse{
			Error: "agent parameter is required",
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

	// Find the bot by ID
	bot, err := a.getBotByAgent(agent)
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
	llmRequest, err := a.convertLLMBridgeRequestToInternal(req)
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

	// Stream the response
	a.streamLLMResponse(c, bot, llmRequest, opts...)
}

// handleAgentCompletionNoStream handles non-streaming completion requests for a specific agent
func (a *API) handleAgentCompletionNoStream(c *gin.Context) {
	agent := c.Param("agent")
	if agent == "" {
		agent = a.config.GetDefaultBotName()
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

	// Find the bot by ID
	bot, err := a.getBotByAgent(agent)
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
	llmRequest, err := a.convertLLMBridgeRequestToInternal(req)
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
	llmRequest, err := a.convertLLMBridgeRequestToInternal(req)
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
	llmRequest, err := a.convertLLMBridgeRequestToInternal(req)
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

	// Handle non-streaming response
	a.handleNonStreamingLLMResponse(c, bot, llmRequest, opts...)
}
