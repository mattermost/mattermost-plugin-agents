// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	externalProxyDiscoveryTimeout = 10 * time.Second
	nativeMattermostToolOwner     = "mattermost"
)

// PluginServerRegistry is the read-side contract for plugin-server aggregation.
type PluginServerRegistry interface {
	ListPluginServers() []mcppkg.PluginServerConfig
}

// Keep this in sync with api/api_bridge_mcp.go's externalServerRebuilder.
var _ interface{ RebuildExternalServer() } = (*PluginMCPHandlers)(nil)

// PluginMCPHandlers wires the MCP HTTP handlers used by the Agents plugin's
// external MCP endpoint.
type PluginMCPHandlers struct {
	OAuthMetadataHandler http.HandlerFunc

	// MCPHandler reads the active *mcp.Server on every request.
	MCPHandler http.Handler

	siteURL     string
	metadataURL string

	internalURL     string
	logger          loggerlib.Logger
	registry        PluginServerRegistry
	sourcePluginAPI mmapi.Client

	// Bounds each source-plugin Connect/ListTools during rebuilds.
	proxyDiscoveryTimeout time.Duration

	// rebuildMu serializes rebuilds so two concurrent rebuilds cannot swap an
	// older registry snapshot over a newer one.
	rebuildMu sync.Mutex

	mu            sync.RWMutex
	currentServer *mcp.Server
}

// NewPluginMCPHandlers creates MCP handlers for the Mattermost plugin.
// registry may be nil to disable plugin-server aggregation. Callers must
// inject auth (bearer token or user-ID) via plugin middleware.
func NewPluginMCPHandlers(
	siteURL, internalURL string,
	logger loggerlib.Logger,
	registry PluginServerRegistry,
	sourcePluginAPI mmapi.Client,
) (*PluginMCPHandlers, error) {
	if siteURL == "" {
		return nil, fmt.Errorf("site URL cannot be empty")
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	h := &PluginMCPHandlers{
		siteURL:               siteURL,
		internalURL:           internalURL,
		logger:                logger,
		registry:              registry,
		sourcePluginAPI:       sourcePluginAPI,
		proxyDiscoveryTimeout: externalProxyDiscoveryTimeout,
	}

	h.currentServer = h.buildServer()

	streamableHandler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		h.mu.RLock()
		srv := h.currentServer
		h.mu.RUnlock()
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	resourceURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server", siteURL)
	metadataHandler := CreateOAuthMetadataHandler(resourceURL, siteURL, "Mattermost MCP Server")

	metadataURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource", siteURL)

	h.MCPHandler = streamableHandler
	h.OAuthMetadataHandler = metadataHandler
	h.metadataURL = metadataURL

	return h, nil
}

// buildServer constructs a fresh *mcp.Server with native + proxy tools.
// Does not touch currentServer, so callers can drop h.mu during the
// source-plugin Connect/ListTools calls.
func (h *PluginMCPHandlers) buildServer() *mcp.Server {
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    "mattermost-mcp-server",
			Version: "0.1.0",
		},
		nil,
	)

	trackAIGenerated := true
	config := BaseConfig{
		MMServerURL:         h.siteURL,
		MMInternalServerURL: h.internalURL,
		DevMode:             false,
		TrackAIGenerated:    &trackAIGenerated,
	}

	authProvider := auth.NewSessionAuthenticationProvider(h.siteURL, h.internalURL, h.logger)
	pluginURL := strings.TrimRight(h.siteURL, "/") + "/plugins/mattermost-ai"
	searchService := tools.NewHTTPSemanticSearchService(pluginURL)

	toolProvider := tools.NewMattermostToolProvider(
		authProvider,
		h.logger,
		config,
		tools.AccessModeRemote,
		searchService,
	)
	toolProvider.ProvideTools(mcpServer)

	// Disabled plugin tools are not registered on the external server.
	if h.registry != nil {
		toolOwners := map[string]string{}
		for _, toolName := range toolProvider.ToolNames() {
			toolOwners[toolName] = nativeMattermostToolOwner
		}
		for _, ps := range h.registry.ListPluginServers() {
			if !ps.Enabled || !ps.ExposeExternal {
				continue
			}
			discoveryCtx, cancel := context.WithTimeout(context.Background(), h.proxyDiscoveryTimeout)
			proxyTools, proxyHandlers, buildErr := BuildProxyTools(discoveryCtx, ps, h.sourcePluginAPI)
			cancel()
			if buildErr != nil {
				if errors.Is(buildErr, context.DeadlineExceeded) || errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
					h.logger.Warn("timed out building proxy tools for plugin server; skipping",
						"plugin_id", ps.PluginID, "timeout", h.proxyDiscoveryTimeout.String())
					continue
				}
				h.logger.Error("failed to build proxy tools for plugin server; skipping",
					"plugin_id", ps.PluginID, "error", buildErr.Error())
				continue
			}
			policyConfig := &mcppkg.ServerConfig{
				Name:        ps.Name,
				Enabled:     true,
				BaseURL:     "plugin://" + ps.PluginID,
				ToolConfigs: ps.ToolConfigs,
			}
			for i := range proxyTools {
				if _, enabled := policyConfig.GetToolPolicy(proxyTools[i].Name); !enabled {
					continue
				}
				if existing, ok := toolOwners[proxyTools[i].Name]; ok {
					if existing == nativeMattermostToolOwner {
						h.logger.Error("proxy tool name conflicts with native Mattermost tool; skipping",
							"tool_name", proxyTools[i].Name,
							"plugin_id", ps.PluginID)
						continue
					}
					h.logger.Error("duplicate proxy tool name across plugin MCP servers; skipping",
						"tool_name", proxyTools[i].Name,
						"plugin_id", ps.PluginID,
						"existing_plugin_id", existing)
					continue
				}
				toolOwners[proxyTools[i].Name] = ps.PluginID
				mcpServer.AddTool(proxyTools[i], proxyHandlers[i])
			}
		}
	}

	return mcpServer
}

// RebuildExternalServer reconstructs the underlying *mcp.Server from the
// current plugin-server registry.
func (h *PluginMCPHandlers) RebuildExternalServer() {
	h.rebuildMu.Lock()
	defer h.rebuildMu.Unlock()

	nextServer := h.buildServer()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentServer = nextServer
}
