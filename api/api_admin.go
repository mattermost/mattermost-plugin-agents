// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// handleReindexPosts starts a background job to reindex all posts
func (a *API) handleReindexPosts(c *gin.Context) {
	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}
	if a.indexerService == nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("search functionality is not configured"))
		return
	}

	jobStatus, err := a.indexerService.StartReindexJob()
	if err != nil {
		switch err.Error() {
		case "job already running":
			c.JSON(http.StatusConflict, jobStatus)
			return
		default:
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	c.JSON(http.StatusOK, jobStatus)
}

// handleGetJobStatus gets the status of the reindex job
func (a *API) handleGetJobStatus(c *gin.Context) {
	if a.indexerService == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"status": "no_job",
		})
		return
	}

	jobStatus, err := a.indexerService.GetJobStatus()
	if err != nil {
		if err.Error() == "not found" {
			c.JSON(http.StatusNotFound, gin.H{
				"status": "no_job",
			})
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get job status: %w", err))
		return
	}

	c.JSON(http.StatusOK, jobStatus)
}

// handleCancelJob cancels a running reindex job
func (a *API) handleCancelJob(c *gin.Context) {
	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if a.indexerService == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"status": "no_job",
		})
		return
	}

	jobStatus, err := a.indexerService.CancelJob()
	if err != nil {
		switch err.Error() {
		case "not found":
			c.JSON(http.StatusNotFound, gin.H{
				"status": "no_job",
			})
			return
		case "not running":
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "not_running",
			})
			return
		default:
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get job status: %w", err))
			return
		}
	}

	c.JSON(http.StatusOK, jobStatus)
}

func (a *API) mattermostAdminAuthorizationRequired(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !a.pluginAPI.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		c.AbortWithError(http.StatusForbidden, errors.New("must be a system admin"))
		return
	}
}

// MCPToolInfo represents a tool from an MCP server for API response
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// MCPServerInfo represents a server and its tools for API response
type MCPServerInfo struct {
	Name        string        `json:"name"`
	URL         string        `json:"url"`
	Tools       []MCPToolInfo `json:"tools"`
	NeethsOAuth bool          `json:"needsOAuth"`
	OAuthURL    string        `json:"oauthURL,omitempty"` // URL to redirect for OAuth if needed
	Error       *string       `json:"error"`
}

// MCPToolsResponse represents the response structure for MCP tools endpoint
type MCPToolsResponse struct {
	Servers []MCPServerInfo `json:"servers"`
}

// handleGetMCPTools discovers and returns tools from all configured MCP servers
func (a *API) handleGetMCPTools(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	mcpConfig := a.config.MCP()

	// If MCP is not enabled, return empty response
	if !mcpConfig.Enabled {
		c.JSON(http.StatusOK, MCPToolsResponse{
			Servers: []MCPServerInfo{},
		})
		return
	}

	response := MCPToolsResponse{
		Servers: make([]MCPServerInfo, 0, len(mcpConfig.Servers)+1),
	}

	// Discover tools from embedded server if enabled
	if mcpConfig.EmbeddedServer.Enabled {
		embeddedServer := a.mcpClientManager.GetEmbeddedServer()
		if embeddedServer != nil {
			serverInfo := MCPServerInfo{
				Name:  mcp.EmbeddedServerName,
				URL:   mcp.EmbeddedClientKey,
				Tools: []MCPToolInfo{},
				Error: nil,
			}

			// Try to discover tools from embedded server
			tools, err := a.discoverEmbeddedServerTools(c.Request.Context(), userID, mcpConfig.EmbeddedServer, embeddedServer)
			if err != nil {
				errMsg := err.Error()
				serverInfo.Error = &errMsg
			} else {
				serverInfo.Tools = tools
			}

			response.Servers = append(response.Servers, serverInfo)
		}
	}

	// Discover tools from each configured remote server
	for _, serverConfig := range mcpConfig.Servers {
		if !serverConfig.Enabled {
			continue
		}
		serverInfo := MCPServerInfo{
			Name:  serverConfig.Name,
			URL:   serverConfig.BaseURL,
			Tools: []MCPToolInfo{},
			Error: nil,
		}

		// Try to connect to the server and discover tools
		tools, err := a.discoverRemoteServerTools(c.Request.Context(), userID, serverConfig)
		if err != nil {
			var oauthErr *mcp.OAuthNeededError
			if errors.As(err, &oauthErr) {
				serverInfo.NeethsOAuth = true
				serverInfo.OAuthURL = oauthErr.AuthURL()
			} else {
				errMsg := err.Error()
				serverInfo.Error = &errMsg
			}
		} else {
			serverInfo.Tools = tools
		}

		response.Servers = append(response.Servers, serverInfo)
	}

	c.JSON(http.StatusOK, response)
}

// discoverRemoteServerTools connects to a single remote MCP server and discovers its tools
func (a *API) discoverRemoteServerTools(ctx context.Context, userID string, serverConfig mcp.ServerConfig) ([]MCPToolInfo, error) {
	toolInfos, err := mcp.DiscoverRemoteServerTools(ctx, userID, serverConfig, a.pluginAPI.Log, a.mcpClientManager.GetOAuthManager(), a.mcpClientManager.GetHTTPClient(), a.mcpClientManager.GetToolsCache())
	if err != nil {
		return nil, err
	}

	tools := make([]MCPToolInfo, 0, len(toolInfos))
	for _, toolInfo := range toolInfos {
		tools = append(tools, MCPToolInfo{
			Name:        toolInfo.Name,
			Description: toolInfo.Description,
			InputSchema: toolInfo.InputSchema,
		})
	}

	return tools, nil
}

// discoverEmbeddedServerTools connects to the embedded MCP server and discovers its tools
func (a *API) discoverEmbeddedServerTools(ctx context.Context, requestingAdminID string, embeddedConfig mcp.EmbeddedServerConfig, embeddedServer mcp.EmbeddedMCPServer) ([]MCPToolInfo, error) {
	// Tool discovery doesn't require authentication - just listing available tools
	// Pass empty sessionID to create an unauthenticated connection
	toolInfos, err := mcp.DiscoverEmbeddedServerTools(ctx, requestingAdminID, "", embeddedConfig, embeddedServer, a.pluginAPI.Log, a.pluginAPI)
	if err != nil {
		return nil, err
	}

	tools := make([]MCPToolInfo, 0, len(toolInfos))
	for _, toolInfo := range toolInfos {
		tools = append(tools, MCPToolInfo{
			Name:        toolInfo.Name,
			Description: toolInfo.Description,
			InputSchema: toolInfo.InputSchema,
		})
	}

	return tools, nil
}

// ClearMCPToolsCacheResponse represents the response for clearing the cache
type ClearMCPToolsCacheResponse struct {
	ClearedServers int    `json:"cleared_servers"`
	Message        string `json:"message"`
}

// handleClearMCPToolsCache clears the tools cache for all MCP servers
func (a *API) handleClearMCPToolsCache(c *gin.Context) {
	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	toolsCache := a.mcpClientManager.GetToolsCache()
	if toolsCache == nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("tools cache not available"))
		return
	}

	// Clear all cache entries
	clearedCount, err := toolsCache.ClearAll()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to clear cache: %w", err))
		return
	}

	c.JSON(http.StatusOK, ClearMCPToolsCacheResponse{
		ClearedServers: clearedCount,
		Message:        fmt.Sprintf("Successfully cleared cache for %d servers", clearedCount),
	})
}
