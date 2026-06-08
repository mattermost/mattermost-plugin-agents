// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// UserMCPToolsResponse is the top-level response for GET /mcp/tools.
type UserMCPToolsResponse struct {
	Servers []UserMCPServerInfo `json:"servers"`
}

// UserMCPServerInfo describes a single MCP server and its visible tools.
type UserMCPServerInfo struct {
	Name          string            `json:"name"`
	ServerOrigin  string            `json:"serverOrigin"`
	Authenticated bool              `json:"authenticated"`
	NeedsOAuth    bool              `json:"needsOAuth"`
	AuthEmail     string            `json:"authEmail,omitempty"`
	AuthURL       string            `json:"authURL,omitempty"`
	Tools         []UserMCPToolInfo `json:"tools"`
}

// UserMCPToolInfo describes a single tool within a server response.
type UserMCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Policy      string `json:"policy"`
}

// handleGetUserMCPTools returns the user-visible MCP tools grouped by server.
func (a *API) handleGetUserMCPTools(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	mcpCfg := a.config.MCP()

	tools, mcpErrors := a.mcpClientManager.GetToolsForUser(userID)

	// Group tools by ServerOrigin
	toolsByOrigin := make(map[string][]llm.Tool, len(tools))
	for _, t := range tools {
		toolsByOrigin[t.ServerOrigin] = append(toolsByOrigin[t.ServerOrigin], t)
	}

	authErrorsByOrigin := make(map[string]llm.ToolAuthError)
	if mcpErrors != nil {
		for _, authErr := range mcpErrors.ToolAuthErrors {
			authErrorsByOrigin[authErr.ServerOrigin] = authErr
		}
	}

	oauthManager := a.mcpClientManager.GetOAuthManager()
	servers := make([]UserMCPServerInfo, 0, len(mcpCfg.Servers)+1)

	for i := range mcpCfg.Servers {
		serverConfig := &mcpCfg.Servers[i]
		if !serverConfig.Enabled || serverConfig.BaseURL == "" {
			continue
		}

		servers = append(servers, buildUserMCPServerInfo(
			a,
			userID,
			oauthManager,
			serverConfig,
			toolsByOrigin[serverConfig.BaseURL],
			authErrorsByOrigin,
		))
	}

	if a.mcpClientManager.GetEmbeddedServer() != nil {
		toolConfigs := mcpCfg.EmbeddedServer.ToolConfigs
		if len(toolConfigs) == 0 {
			toolConfigs = mcp.SeedVettedToolConfigs(mcp.EmbeddedClientKey)
		}

		embeddedConfig := &mcp.ServerConfig{
			Name:        mcp.EmbeddedServerName,
			Enabled:     true,
			BaseURL:     mcp.EmbeddedClientKey,
			ToolConfigs: toolConfigs,
		}

		servers = append(servers, buildUserMCPServerInfo(
			a,
			userID,
			oauthManager,
			embeddedConfig,
			toolsByOrigin[mcp.EmbeddedClientKey],
			authErrorsByOrigin,
		))
	}

	// Plugin rows use the same synthetic origin key as filterToolsByConfig.
	for _, cfg := range a.mcpClientManager.ListPluginServers() {
		if !cfg.Enabled {
			continue
		}

		origin := "plugin://" + cfg.PluginID
		pluginConfig := &mcp.ServerConfig{
			Name:        cfg.Name,
			Enabled:     true,
			BaseURL:     origin,
			ToolConfigs: cfg.ToolConfigs,
		}

		servers = append(servers, buildUserMCPServerInfo(
			a,
			userID,
			oauthManager,
			pluginConfig,
			toolsByOrigin[origin],
			authErrorsByOrigin,
		))
	}

	c.JSON(http.StatusOK, UserMCPToolsResponse{Servers: servers})
}

func buildUserMCPServerInfo(
	api *API,
	userID string,
	oauthManager *mcp.OAuthManager,
	serverConfig *mcp.ServerConfig,
	originTools []llm.Tool,
	authErrorsByOrigin map[string]llm.ToolAuthError,
) UserMCPServerInfo {
	toolInfos := make([]UserMCPToolInfo, 0, len(originTools))
	for _, t := range originTools {
		policy, enabled := serverConfig.GetToolPolicy(t.Name)
		toolInfos = append(toolInfos, UserMCPToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Enabled:     enabled,
			Policy:      policy,
		})
	}

	sort.Slice(toolInfos, func(i, j int) bool {
		return toolInfos[i].Name < toolInfos[j].Name
	})

	authError, hasAuthError := authErrorsByOrigin[serverConfig.BaseURL]

	hasStoredToken := false
	if oauthManager != nil {
		var err error
		hasStoredToken, err = oauthManager.HasStoredToken(userID, serverConfig.Name)
		if err != nil {
			hasStoredToken = false
			if api != nil {
				api.pluginAPI.Log.Debug("Failed to check MCP OAuth token presence", "userID", userID, "serverName", serverConfig.Name, "serverOrigin", serverConfig.BaseURL, "error", err)
			}
		}
	}

	var authNeededState *mcp.OAuthNeededState
	if oauthManager != nil {
		var err error
		authNeededState, err = oauthManager.LoadAuthNeededState(userID, serverConfig.Name)
		if err != nil {
			authNeededState = nil
			if api != nil {
				api.pluginAPI.Log.Debug("Failed to load MCP OAuth-needed state", "userID", userID, "serverName", serverConfig.Name, "serverOrigin", serverConfig.BaseURL, "error", err)
			}
		}
	}
	hasPersistedAuthNeeded := authNeededState != nil && authNeededState.AuthURL != ""

	authenticated := isUserMCPServerAuthenticated(serverConfig, len(originTools) > 0, hasAuthError, hasStoredToken, hasPersistedAuthNeeded)
	staticOAuthConfigured := serverConfig.ClientID != ""
	needsOAuth := hasAuthError || hasStoredToken || hasPersistedAuthNeeded || (!authenticated && staticOAuthConfigured)

	info := UserMCPServerInfo{
		Name:          serverConfig.Name,
		ServerOrigin:  serverConfig.BaseURL,
		Authenticated: authenticated,
		NeedsOAuth:    needsOAuth,
		Tools:         toolInfos,
	}
	switch {
	case hasAuthError && !info.Authenticated && authError.AuthURL != "":
		info.AuthURL = authError.AuthURL
	case hasPersistedAuthNeeded && !info.Authenticated:
		info.AuthURL = authNeededState.AuthURL
	case !info.Authenticated && oauthManager != nil && staticOAuthConfigured:
		info.AuthURL = oauthManager.StartURL(serverConfig.Name)
	}
	return info
}

func isUserMCPServerAuthenticated(
	serverConfig *mcp.ServerConfig,
	hasDiscoveredTools bool,
	hasAuthError bool,
	hasStoredToken bool,
	hasPersistedAuthNeeded bool,
) bool {
	if serverConfig.BaseURL == mcp.EmbeddedClientKey {
		return true
	}

	if hasAuthError || hasPersistedAuthNeeded {
		return false
	}

	if hasDiscoveredTools {
		return true
	}

	return hasStoredToken
}

// handleGetUserPreferences returns the user's MCP tool provider preferences.
func (a *API) handleGetUserPreferences(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	prefs, err := mcp.LoadUserPreferences(a.mmClient, userID)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to load preferences: %w", err))
		return
	}

	c.JSON(http.StatusOK, prefs)
}

// handlePutUserPreferences replaces the user's MCP tool provider preferences.
func (a *API) handlePutUserPreferences(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, mcp.UserPreferencesMaxRequestBodyBytes)

	var prefs mcp.UserToolProviderPreferences
	if err := c.ShouldBindJSON(&prefs); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.AbortWithError(http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large: %w", err))
			return
		}
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	saved, err := mcp.SaveUserPreferences(a.mmClient, userID, &prefs)
	if err != nil {
		if errors.Is(err, mcp.ErrUserPreferencesInvalid) {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid preferences: %w", err))
			return
		}
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save preferences: %w", err))
		return
	}

	c.JSON(http.StatusOK, saved)
}

// handleDeleteUserMCPOAuth disconnects the current user from an MCP server
// by removing their stored OAuth token.
func (a *API) handleDeleteUserMCPOAuth(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	serverName := c.Param("serverName")

	if serverName == "" {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("serverName is required"))
		return
	}

	if err := a.mcpClientManager.DisconnectUserOAuth(userID, serverName); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to disconnect: %w", err))
		return
	}

	a.publishMCPOAuthClusterInvalidation(userID)
	a.publishMCPDisconnected(userID, serverName)
	c.Status(http.StatusOK)
}

// publishMCPDisconnected notifies the webapp that the user disconnected an MCP server.
func (a *API) publishMCPDisconnected(userID, serverName string) {
	if a.mmClient == nil || userID == "" {
		return
	}

	payload := map[string]interface{}{
		"status":     "disconnected",
		"serverName": serverName,
	}
	if sc, ok := a.getMCPServerConfig(serverName); ok && sc.BaseURL != "" {
		payload["serverOrigin"] = sc.BaseURL
	}

	a.mmClient.PublishWebSocketEvent(
		WebsocketEventMCPConnectionUpdated,
		payload,
		&model.WebsocketBroadcast{UserId: userID},
	)
}

// handleGetVettedToolSeed returns authoritative vetted default tool_configs for a base URL (admin).
func (a *API) handleGetVettedToolSeed(c *gin.Context) {
	baseURL := c.Query("base_url")
	if baseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url is required"})
		return
	}

	configs := mcp.SeedVettedToolConfigs(baseURL)
	if configs == nil {
		configs = []mcp.ToolConfig{}
	}

	c.JSON(http.StatusOK, gin.H{"tool_configs": configs})
}
