// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/indexer"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// ReindexRequest represents the request body for reindexing
type ReindexRequest struct {
	ClearIndex *bool `json:"clearIndex"`
}

// handleReindexPosts starts a background job to reindex all posts
func (a *API) handleReindexPosts(c *gin.Context) {
	if a.indexerService == nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("search functionality is not configured"))
		return
	}

	// Parse request body (optional — empty body uses defaults, malformed JSON returns 400)
	var req ReindexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		req.ClearIndex = nil
	}

	// Default to clearIndex=true for backward compatibility
	clearIndex := true
	if req.ClearIndex != nil {
		clearIndex = *req.ClearIndex
	}

	jobStatus, err := a.indexerService.StartReindexJob(clearIndex)
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
		if mmapi.IsKVNotFound(err) {
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
		if mmapi.IsKVNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"status": "no_job",
			})
			return
		}
		switch err.Error() {
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

// handleCatchUpIndex starts a catch-up indexing job
func (a *API) handleCatchUpIndex(c *gin.Context) {
	if err := a.enforceEmptyBody(c); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if a.indexerService == nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("search functionality is not configured"))
		return
	}

	jobStatus, err := a.indexerService.StartCatchUpJob()
	if err != nil {
		switch err.Error() {
		case "job already running":
			c.JSON(http.StatusConflict, jobStatus)
			return
		case "no previous index found, run a full reindex first":
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		default:
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}

	c.JSON(http.StatusOK, jobStatus)
}

// handleIndexHealthCheck performs a health check on the search index,
// including model compatibility information.
func (a *API) handleIndexHealthCheck(c *gin.Context) {
	if a.indexerService == nil {
		c.JSON(http.StatusOK, a.notConfiguredHealthCheck())
		return
	}

	result, err := a.indexerService.CheckIndexHealth(c.Request.Context())
	if err != nil {
		if err.Error() == "search functionality is not configured" {
			c.JSON(http.StatusOK, a.notConfiguredHealthCheck())
			return
		}
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	// Include model compatibility in the health check result
	cfg := a.config.EmbeddingSearchConfig()
	compat := a.indexerService.CheckModelCompatibility(cfg.GetProviderType(), cfg.Dimensions, cfg.GetModelName())
	result.ModelCompatible = compat.Compatible
	result.ModelNeedsReindex = compat.NeedsReindex
	result.ModelCompatReason = compat.Reason
	result.StoredProviderType = compat.StoredProviderType
	result.StoredDimensions = compat.StoredDimensions
	result.StoredModelName = compat.StoredModelName

	c.JSON(http.StatusOK, result)
}

// notConfiguredHealthCheck returns a HealthCheckResult for when search is not configured,
// including any initialization error if available.
func (a *API) notConfiguredHealthCheck() indexer.HealthCheckResult {
	result := indexer.HealthCheckResult{
		Status:          "not_configured",
		ModelCompatible: true,
	}
	if a.getSearchInitError != nil {
		if errMsg := a.getSearchInitError(); errMsg != "" {
			result.Status = "init_error"
			result.Error = errMsg
		}
	}
	return result
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
	Name       string        `json:"name"`
	URL        string        `json:"url"`
	Tools      []MCPToolInfo `json:"tools"`
	NeedsOAuth bool          `json:"needsOAuth"`
	OAuthURL   string        `json:"oauthURL,omitempty"` // URL to redirect for OAuth if needed
	Error      *string       `json:"error"`

	// ServerType is one of "embedded", "remote", or "plugin".
	ServerType string `json:"serverType"`
	Enabled    bool   `json:"enabled"`
	// ToolConfigs is populated for plugin rows only.
	ToolConfigs []mcp.ToolConfig `json:"toolConfigs,omitempty"`
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

	response := MCPToolsResponse{
		Servers: make([]MCPServerInfo, 0, len(mcpConfig.Servers)+1),
	}

	embeddedServer := a.mcpClientManager.GetEmbeddedServer()
	if embeddedServer != nil {
		serverInfo := MCPServerInfo{
			Name:       mcp.EmbeddedServerName,
			URL:        mcp.EmbeddedClientKey,
			Tools:      []MCPToolInfo{},
			Error:      nil,
			ServerType: "embedded",
			Enabled:    true,
		}

		// Embedded MCP is always available after PR #617, even if older configs still
		// have the legacy toggle stored as false.
		tools, err := a.discoverEmbeddedServerTools(c.Request.Context(), userID, mcpConfig.EmbeddedServer, embeddedServer)
		if err != nil {
			errMsg := err.Error()
			serverInfo.Error = &errMsg
		} else {
			serverInfo.Tools = tools
		}

		response.Servers = append(response.Servers, serverInfo)
	}

	// Discover tools from each configured remote server
	for _, serverConfig := range mcpConfig.Servers {
		if !serverConfig.Enabled {
			continue
		}
		serverInfo := MCPServerInfo{
			Name:       serverConfig.Name,
			URL:        serverConfig.BaseURL,
			Tools:      []MCPToolInfo{},
			Error:      nil,
			ServerType: "remote",
			Enabled:    serverConfig.Enabled,
		}

		// Try to connect to the server and discover tools
		tools, err := a.discoverRemoteServerTools(c.Request.Context(), userID, serverConfig)
		if err != nil {
			var oauthErr *mcp.OAuthNeededError
			if errors.As(err, &oauthErr) {
				serverInfo.NeedsOAuth = true
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

	// Render disabled plugin entries (with an empty tool list) so the admin UI
	// can re-enable them. Skip orphans hydrated from persisted config without
	// a live source-plugin registration; surfacing them as "session not found"
	// rows is misleading, and their persisted policy is preserved on disk
	// regardless of whether they appear here.
	for _, cfg := range a.mcpClientManager.ListPluginServers() {
		if !a.mcpClientManager.IsPluginRegistered(cfg.PluginID) {
			continue
		}
		serverInfo := MCPServerInfo{
			Name:        cfg.Name,
			URL:         fmt.Sprintf("plugin://%s%s", cfg.PluginID, cfg.Path),
			Tools:       []MCPToolInfo{},
			Error:       nil,
			ServerType:  "plugin",
			Enabled:     cfg.Enabled,
			ToolConfigs: cfg.ToolConfigs,
		}

		if cfg.Enabled {
			tools, err := a.discoverPluginServerTools(c.Request.Context(), userID, cfg)
			if err != nil {
				errMsg := err.Error()
				serverInfo.Error = &errMsg
			} else {
				serverInfo.Tools = tools
			}
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

func (a *API) discoverPluginServerTools(ctx context.Context, userID string, cfg mcp.PluginServerConfig) ([]MCPToolInfo, error) {
	toolInfos, err := a.mcpClientManager.DiscoverPluginServerTools(ctx, userID, cfg)
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

// UpdatePluginServerRequest is the body shape for PUT /admin/mcp/plugin-servers/:pluginID.
// Pointer fields use partial-update semantics: nil means unchanged.
type UpdatePluginServerRequest struct {
	Enabled     *bool             `json:"enabled,omitempty"`
	ToolConfigs *[]mcp.ToolConfig `json:"tool_configs,omitempty"`
}

// handleUpdatePluginServer updates admin-owned fields (Enabled, ToolConfigs) on
// a registered plugin MCP server; PluginID, Name, Path, and ExposeExternal
// remain owned by the source plugin. The full registry snapshot is captured
// before configUpdater.Update to avoid re-entrant pluginServersMu locking.
func (a *API) handleUpdatePluginServer(c *gin.Context) {
	pluginID := c.Param("pluginID")
	if pluginID == "" {
		c.AbortWithError(http.StatusBadRequest, errors.New("pluginID path parameter required"))
		return
	}

	var req UpdatePluginServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	live, foundLive := a.mcpClientManager.GetPluginServer(pluginID)
	if !foundLive {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("plugin MCP server %q is not registered", pluginID))
		return
	}

	updated := live
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}
	if req.ToolConfigs != nil {
		updated.ToolConfigs = *req.ToolConfigs
	}

	existing, getErr := a.configStore.GetConfig()
	if getErr != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to load config for plugin-server save: %w", getErr))
		return
	}
	if existing == nil {
		c.AbortWithError(http.StatusInternalServerError, errors.New("no plugin configuration available"))
		return
	}
	// Clone to avoid mutating the store's cached pointer.
	cfg := existing.Clone()

	// Merge by PluginID against the persisted list rather than overwriting
	// with the in-memory snapshot, which would silently drop entries for
	// plugins that are persisted but currently inactive in memory.
	merged := append([]mcp.PluginServerConfig(nil), cfg.MCP.PluginServers...)
	mergedIdx := -1
	for i := range merged {
		if merged[i].PluginID == updated.PluginID {
			mergedIdx = i
			break
		}
	}
	if mergedIdx >= 0 {
		merged[mergedIdx] = updated
	} else {
		merged = append(merged, updated)
	}
	cfg.MCP.PluginServers = merged

	if err := a.configStore.SaveConfig(*cfg); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save plugin-server config: %w", err))
		return
	}

	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of plugin-server config update: %w", err))
		return
	}

	a.mcpClientManager.RegisterPluginServer(updated)
	a.configUpdater.Update(cfg)

	// Rebuild when either old or new state was external so removed tools
	// disappear from the aggregate server.
	if updated.ExposeExternal || live.ExposeExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.JSON(http.StatusOK, updated)
}
