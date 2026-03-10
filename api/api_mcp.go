// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
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
	AuthEmail     string            `json:"authEmail,omitempty"`
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
	if !mcpCfg.Enabled {
		c.JSON(http.StatusOK, UserMCPToolsResponse{Servers: []UserMCPServerInfo{}})
		return
	}

	tools, _ := a.mcpClientManager.GetToolsForUser(userID)

	// Group tools by ServerOrigin
	toolsByOrigin := make(map[string][]llm.Tool, len(tools))
	for _, t := range tools {
		toolsByOrigin[t.ServerOrigin] = append(toolsByOrigin[t.ServerOrigin], t)
	}

	// Build server lookup from config
	type serverMeta struct {
		name    string
		baseURL string
		order   int
	}
	serverMetas := make(map[string]serverMeta, len(mcpCfg.Servers)+1)
	for i, s := range mcpCfg.Servers {
		serverMetas[s.BaseURL] = serverMeta{name: s.Name, baseURL: s.BaseURL, order: i}
	}
	if mcpCfg.EmbeddedServer.Enabled {
		serverMetas[mcp.EmbeddedClientKey] = serverMeta{
			name:    mcp.EmbeddedServerName,
			baseURL: mcp.EmbeddedClientKey,
			order:   len(mcpCfg.Servers),
		}
	}

	// Build response
	var servers []UserMCPServerInfo
	for origin, originTools := range toolsByOrigin {
		meta, ok := serverMetas[origin]
		if !ok {
			continue
		}

		// Get tool config for this server
		var sc *mcp.ServerConfig
		if origin == mcp.EmbeddedClientKey {
			sc = &mcp.ServerConfig{ToolConfigs: mcp.SeedVettedToolConfigs(mcp.EmbeddedClientKey)}
		} else {
			for i := range mcpCfg.Servers {
				if mcpCfg.Servers[i].BaseURL == origin {
					sc = &mcpCfg.Servers[i]
					break
				}
			}
		}

		// Check auth status
		authenticated := false
		if origin == mcp.EmbeddedClientKey {
			authenticated = true
		} else if a.mcpClientManager.GetOAuthManager() != nil {
			hasToken, err := a.mcpClientManager.GetOAuthManager().HasStoredToken(userID, meta.name)
			if err == nil && hasToken {
				authenticated = true
			}
		}

		// If tools were returned by GetToolsForUser, the connection succeeded
		// (even without OAuth token storage), so mark as authenticated.
		if len(originTools) > 0 {
			authenticated = true
		}

		var toolInfos []UserMCPToolInfo
		for _, t := range originTools {
			policy := "ask"
			enabled := true
			if sc != nil {
				p, e := sc.GetToolPolicy(t.Name)
				policy = p
				enabled = e
			}
			toolInfos = append(toolInfos, UserMCPToolInfo{
				Name:        t.Name,
				Description: t.Description,
				Enabled:     enabled,
				Policy:      policy,
			})
		}

		// Sort tools by name for deterministic output
		sort.Slice(toolInfos, func(i, j int) bool {
			return toolInfos[i].Name < toolInfos[j].Name
		})

		servers = append(servers, UserMCPServerInfo{
			Name:          meta.name,
			ServerOrigin:  origin,
			Authenticated: authenticated,
			Tools:         toolInfos,
		})
	}

	// Sort servers by config order for deterministic output
	sort.Slice(servers, func(i, j int) bool {
		mi := serverMetas[servers[i].ServerOrigin]
		mj := serverMetas[servers[j].ServerOrigin]
		return mi.order < mj.order
	})

	c.JSON(http.StatusOK, UserMCPToolsResponse{Servers: servers})
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

	var prefs mcp.UserToolProviderPreferences
	if err := c.BindJSON(&prefs); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	saved, err := mcp.SaveUserPreferences(a.mmClient, userID, &prefs)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save preferences: %w", err))
		return
	}

	c.JSON(http.StatusOK, saved)
}
