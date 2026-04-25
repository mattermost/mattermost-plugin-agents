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

const externalProxyDiscoveryTimeout = 10 * time.Second

// PluginServerRegistry is the minimal read-side contract NewPluginMCPHandlers
// needs on the plugin-server registry. Declared here (not as a method set on
// *mcp.ClientManager) so mcpserver does not take a hard dependency on the mcp
// package's internal layout; the ClientManager satisfies this interface
// automatically via its ListPluginServers method.
type PluginServerRegistry interface {
	ListPluginServers() []mcppkg.PluginServerConfig
}

// Compile-time check for the API bridge rebuild contract.
var _ interface{ RebuildExternalServer() } = (*PluginMCPHandlers)(nil)

// PluginMCPHandlers contains the HTTP handlers for MCP endpoints
// These handlers are designed to be embedded in a plugin's HTTP router.
type PluginMCPHandlers struct {
	OAuthMetadataHandler http.HandlerFunc

	// MCPHandler is the streamable HTTP handler wired to a factory that reads
	// the currently active underlying *mcp.Server under RLock on every request.
	MCPHandler http.Handler

	siteURL     string
	metadataURL string

	// Rebuild dependencies. All captured at NewPluginMCPHandlers time so
	// RebuildExternalServer can reconstruct the underlying *mcp.Server without
	// re-plumbing anything from the plugin activation path.
	internalURL     string
	logger          loggerlib.Logger
	registry        PluginServerRegistry
	sourcePluginAPI mmapi.Client
	// proxyDiscoveryTimeout bounds each source plugin Connect/ListTools during
	// external server rebuilds so one unhealthy plugin cannot block the swap.
	proxyDiscoveryTimeout time.Duration

	// rebuildMu serializes full rebuild operations while still allowing external
	// MCP requests to use the current server. Without it, two overlapping
	// rebuilds could finish out of order and swap an older registry snapshot over
	// a newer one.
	rebuildMu sync.Mutex

	// mu guards the currently-active *mcp.Server. Write-locked only on
	// RebuildExternalServer's final swap; read-locked on every factory
	// invocation by the streamable HTTP handler.
	// StreamableHTTPOptions{Stateless: true} means
	// there are no long-lived sessions to coordinate, so swaps are safe.
	mu            sync.RWMutex
	currentServer *mcp.Server
}

// NewPluginMCPHandlers creates MCP handlers for use within a Mattermost plugin.
//
// The handlers aggregate:
//   - native agents-plugin tools from tools.NewMattermostToolProvider.
//   - proxy tools for plugin servers with Enabled=true and ExposeExternal=true.
//     See BuildProxyTools for the build semantics and unreachable-plugin behavior.
//
// registry may be nil to disable aggregation entirely (useful in tests).
// sourcePluginAPI must be non-nil if registry is non-nil and any plugin server
// has ExposeExternal=true; NewPluginMCPHandlers does not validate this up
// front because the registry may be empty at construction time and populated
// later via RebuildExternalServer.
//
// The handlers expect requests to have an Authorization Bearer token (or
// equivalent session/user-ID identification) injected by the plugin middleware.
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

	// Build the initial *mcp.Server synchronously. Rebuild path reuses the
	// same private constructor.
	h.currentServer = h.buildServer()

	// Streamable HTTP handler reads the current server under RLock every
	// request. Stateless transport means no session disruption on swap.
	streamableHandler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		h.mu.RLock()
		srv := h.currentServer
		h.mu.RUnlock()
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	// OAuth metadata handler — unchanged.
	resourceURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server", siteURL)
	metadataHandler := CreateOAuthMetadataHandler(resourceURL, siteURL, "Mattermost MCP Server")

	// The metadata URL for WWW-Authenticate headers
	metadataURL := fmt.Sprintf("%s/plugins/mattermost-ai/mcp-server/.well-known/oauth-protected-resource", siteURL)

	h.MCPHandler = streamableHandler
	h.OAuthMetadataHandler = metadataHandler
	h.metadataURL = metadataURL

	return h, nil
}

// buildServer constructs a fresh *mcp.Server with native + proxy tools. It does
// not read or mutate currentServer, so callers do not need to hold h.mu while
// proxy discovery performs source-plugin Connect/ListTools calls.
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

	if h.registry != nil {
		for _, ps := range h.registry.ListPluginServers() {
			if !ps.Enabled || !ps.ExposeExternal {
				continue
			}
			discoveryCtx, cancel := context.WithTimeout(context.Background(), h.proxyDiscoveryTimeout)
			proxyTools, proxyHandlers, buildErr := BuildProxyTools(discoveryCtx, ps, h.sourcePluginAPI)
			cancel()
			if buildErr != nil {
				if errors.Is(buildErr, context.DeadlineExceeded) {
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
				mcpServer.AddTool(proxyTools[i], proxyHandlers[i])
			}
		}
	}

	return mcpServer
}

// RebuildExternalServer reconstructs the underlying *mcp.Server, picking up
// any changes to the plugin-server registry (new registrations, unregistrations,
// Enabled / ExposeExternal flag changes). It is the implementation of the
// API bridge rebuild contract.
func (h *PluginMCPHandlers) RebuildExternalServer() {
	h.rebuildMu.Lock()
	defer h.rebuildMu.Unlock()

	nextServer := h.buildServer()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentServer = nextServer
}
