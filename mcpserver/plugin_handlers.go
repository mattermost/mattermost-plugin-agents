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

	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	externalProxyDiscoveryTimeout = 10 * time.Second
	maxConcurrentProxyDiscoveries = 32
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
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// Explicitly the SDK default (introduced in go-sdk v1.7.0, previously
		// unlimited): requests are LLM-generated tool calls, so 4 MiB is ample.
		MaxRequestBodyBytes: mcp.DefaultMaxRequestBodyBytes,
	})

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
	fileContentService := tools.NewHTTPFileContentService(pluginURL)

	toolProvider := tools.NewMattermostToolProvider(
		authProvider,
		h.logger,
		config,
		tools.AccessModeRemote,
		searchService,
		fileContentService,
	)
	toolProvider.ProvideTools(mcpServer)

	// Disabled plugin tools are not registered on the external server.
	if h.registry != nil {
		h.addProxyTools(mcpServer, toolProvider.ToolNames())
	}

	return mcpServer
}

// proxyDiscovery is one source plugin's discovered proxy tools, or the error
// that prevented discovery.
type proxyDiscovery struct {
	tools    []*mcp.Tool
	handlers []mcp.ToolHandler
	err      error
}

// addProxyTools discovers every externally-exposed plugin server concurrently
// and then registers the results sequentially, in registry-snapshot order.
// Splitting discovery from registration is what keeps collision winners
// independent of which source plugin answers first: native Mattermost tools
// always win, and among plugins the first entry in the snapshot wins.
func (h *PluginMCPHandlers) addProxyTools(mcpServer *mcp.Server, nativeToolNames []string) {
	var exposed []mcppkg.PluginServerConfig
	for _, ps := range h.registry.ListPluginServers() {
		if ps.Enabled && ps.ExposeExternal {
			exposed = append(exposed, ps)
		}
	}

	discovered := make([]proxyDiscovery, len(exposed))
	slots := make(chan struct{}, maxConcurrentProxyDiscoveries)
	var wg sync.WaitGroup
	for i := range exposed {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			discovered[i] = h.discoverProxyTools(exposed[i])
		})
	}
	wg.Wait()

	toolOwners := make(map[string]string, len(nativeToolNames))
	for _, toolName := range nativeToolNames {
		toolOwners[toolName] = nativeMattermostToolOwner
	}

	for index, ps := range exposed {
		if discovered[index].err != nil {
			continue
		}

		policyConfig := &mcppkg.ServerConfig{
			Name:        ps.Name,
			Enabled:     true,
			BaseURL:     "plugin://" + ps.PluginID,
			ToolConfigs: ps.ToolConfigs,
		}
		proxyTools := discovered[index].tools
		proxyHandlers := discovered[index].handlers
		for i, proxyTool := range proxyTools {
			if _, enabled := policyConfig.GetToolPolicy(proxyTool.Name); !enabled {
				continue
			}
			if existing, taken := toolOwners[proxyTool.Name]; taken {
				if existing == nativeMattermostToolOwner {
					h.logger.Error("proxy tool name conflicts with native Mattermost tool; skipping",
						"tool_name", proxyTool.Name,
						"plugin_id", ps.PluginID)
				} else {
					h.logger.Error("duplicate proxy tool name across plugin MCP servers; skipping",
						"tool_name", proxyTool.Name,
						"plugin_id", ps.PluginID,
						"existing_plugin_id", existing)
				}
				continue
			}
			toolOwners[proxyTool.Name] = ps.PluginID
			mcpServer.AddTool(proxyTool, proxyHandlers[i])
		}
	}
}

// discoverProxyTools lists one source plugin's tools under the per-plugin
// discovery timeout. A plugin that fails or times out is skipped; healthy
// plugins are unaffected.
func (h *PluginMCPHandlers) discoverProxyTools(ps mcppkg.PluginServerConfig) proxyDiscovery {
	discoveryCtx, cancel := context.WithTimeout(context.Background(), h.proxyDiscoveryTimeout)
	defer cancel()

	proxyTools, proxyHandlers, err := BuildProxyTools(discoveryCtx, ps, h.sourcePluginAPI)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
			h.logger.Warn("timed out building proxy tools for plugin server; skipping",
				"plugin_id", ps.PluginID, "timeout", h.proxyDiscoveryTimeout.String())
		} else {
			h.logger.Error("failed to build proxy tools for plugin server; skipping",
				"plugin_id", ps.PluginID, "error", err.Error())
		}
		return proxyDiscovery{err: err}
	}

	return proxyDiscovery{tools: proxyTools, handlers: proxyHandlers}
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
