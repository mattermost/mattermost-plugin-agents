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

	// ServerType discriminates the row source: "embedded" | "remote" | "plugin".
	// The webapp branches on this in findServerConfig (mcp_tools_viewer.tsx) to
	// render the correct toggle/config surface for each category.
	ServerType string `json:"serverType"`
	// Enabled reflects the admin's on/off state for the server. Always true for
	// embedded (which is non-toggleable). For remote it mirrors MCPServerConfig.Enabled.
	// For plugin entries it mirrors PluginServerConfig.Enabled — plugin state lives
	// server-side (not in mcpConfig), so the webapp reads it from here.
	Enabled bool `json:"enabled"`
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
			Enabled:    true, // embedded is non-toggleable
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
			Enabled:    serverConfig.Enabled, // always true after the Enabled guard above; explicit for clarity
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

	// Discover tools from each plugin-registered MCP server.
	// Unlike the remote branch, we render disabled plugin entries (with an empty
	// tool list) so the admin UI can re-enable them via PUT /admin/mcp/plugin-servers/:pluginID.
	for _, cfg := range a.mcpClientManager.ListPluginServers() {
		serverInfo := MCPServerInfo{
			Name:       cfg.Name,
			URL:        fmt.Sprintf("plugin://%s%s", cfg.PluginID, cfg.Path),
			Tools:      []MCPToolInfo{},
			Error:      nil,
			ServerType: "plugin",
			Enabled:    cfg.Enabled,
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

// discoverPluginServerTools performs an ephemeral probe of a plugin-registered
// MCP server and normalizes its tool list into the admin-API shape. Mirrors
// discoverRemoteServerTools; delegates all transport setup to the mcp package
// (PluginHTTPRoundTripper chain). Fresh probe every call — no caching in Phase 1F.
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
type UpdatePluginServerRequest struct {
	Enabled bool `json:"enabled"`
}

// handleUpdatePluginServer lets an admin flip the Enabled flag on a registered
// plugin MCP server. Does NOT mutate PluginID/Name/Path/ExposeExternal — those
// fields remain owned by the source plugin (set via the bridge register endpoint).
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

	// Locate the existing registration. Plugin-registered servers register
	// themselves; the admin cannot create a row from thin air here.
	var found *mcp.PluginServerConfig
	for _, cfg := range a.mcpClientManager.ListPluginServers() {
		cfg := cfg // capture loop variable for pointer safety
		if cfg.PluginID == pluginID {
			found = &cfg
			break
		}
	}
	if found == nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("plugin MCP server %q is not registered", pluginID))
		return
	}

	// Mutate Enabled only. Preserve everything else.
	updated := *found
	updated.Enabled = req.Enabled
	a.mcpClientManager.RegisterPluginServer(updated)

	// If this plugin opted into external aggregation, rebuild the shared
	// *mcp.Server so external clients see the Enabled state change. This
	// reuses the Step 4 test-seam; if Step 5b's RebuildExternalServer method
	// isn't landed yet, resolveExternalServerRebuilder returns nil and this
	// is a no-op — external aggregation will reconcile on the next rebuild
	// trigger. Safe either way.
	if updated.ExposeExternal {
		if rb := a.resolveExternalServerRebuilder(); rb != nil {
			rb.RebuildExternalServer()
		}
	}

	c.JSON(http.StatusOK, updated)
}
