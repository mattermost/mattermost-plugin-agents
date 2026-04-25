// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	mcppkg "github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PluginServerRegistry is the minimal read-side contract NewPluginMCPHandlers
// needs on the plugin-server registry. Declared here (not as a method set on
// *mcp.ClientManager) so mcpserver does not take a hard dependency on the mcp
// package's internal layout; the ClientManager satisfies this interface
// automatically via its ListPluginServers method.
type PluginServerRegistry interface {
	ListPluginServers() []mcppkg.PluginServerConfig
}

// Structural compile-time check that *PluginMCPHandlers satisfies the
// externalServerRebuilder interface declared (unexported) in
// api/api_bridge_mcp.go. Referenced via `any(h).(externalServerRebuilder)`
// at the bridge handler call site; if this assertion ever fails the rebuild
// trigger silently becomes a no-op (not a compile error), so keep it.
var _ interface{ RebuildExternalServer() } = (*PluginMCPHandlers)(nil)

// PluginMCPHandlers contains the HTTP handlers for MCP endpoints
// These handlers are designed to be embedded in a plugin's HTTP router.
//
// Phase 1G (cross-plugin MCP): the underlying *mcp.Server is rebuildable so
// that registrations/unregistrations from first-party plugins (via the
// /bridge/v1/mcp/register|unregister handlers) can alter the tool set served
// at /plugins/mattermost-ai/mcp-server/mcp without restarting the plugin.
type PluginMCPHandlers struct {
	// OAuthMetadataHandler is unchanged — static, no rebuild required.
	OAuthMetadataHandler http.HandlerFunc

	// MCPHandler is the streamable HTTP handler wired to a factory that reads
	// the currently active underlying *mcp.Server under RLock on every request.
	// Callers (api/api.go) call MCPHandler.ServeHTTP — same surface as before.
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

	// mu guards the currently-active *mcp.Server. Write-locked only on
	// RebuildExternalServer; read-locked on every factory invocation by the
	// streamable HTTP handler. StreamableHTTPOptions{Stateless: true} means
	// there are no long-lived sessions to coordinate, so swaps are safe.
	mu            sync.RWMutex
	currentServer *mcp.Server
}

// NewPluginMCPHandlers creates MCP handlers for use within a Mattermost plugin.
//
// The handlers aggregate:
//   - Tools provided by tools.NewMattermostToolProvider (native agents-plugin
//     tools; unchanged from pre-Phase-1G behavior).
//   - Proxy tools for every first-party plugin server in
//     registry.ListPluginServers() with Enabled=true AND ExposeExternal=true
//     (Phase 1G aggregation). See BuildProxyTools (mcpserver/proxy_tools.go)
//     for the build semantics and unreachable-plugin behavior.
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
		siteURL:         siteURL,
		internalURL:     internalURL,
		logger:          logger,
		registry:        registry,
		sourcePluginAPI: sourcePluginAPI,
	}

	// Build the initial *mcp.Server synchronously. Rebuild path reuses the
	// same private constructor.
	h.currentServer = h.buildServerLocked()

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

// buildServerLocked constructs a fresh *mcp.Server with native + proxy tools.
// Must be called either (a) during NewPluginMCPHandlers before any concurrent
// use of h, or (b) under h.mu.Lock() from RebuildExternalServer.
func (h *PluginMCPHandlers) buildServerLocked() *mcp.Server {
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

	// Phase 1G: aggregate first-party plugin tools.
	// M2 Phase 3: per-tool admin policy is enforced here. Tools with
	// ToolConfigs[i].Enabled == false are dropped before they reach
	// mcpServer.AddTool, so they never appear in ListTools responses to
	// external MCP clients.
	//
	// Scope: this is server-wide, not per-user. The aggregated *mcp.Server is
	// built once (and rebuilt only on registry changes via
	// RebuildExternalServer) and serves all authenticated callers. Per-user
	// scoping would require per-request server rebuilds — out of scope for M2.
	// Admin "deny" therefore means the tool is hidden from every external MCP
	// caller.
	if h.registry != nil {
		for _, ps := range h.registry.ListPluginServers() {
			if !ps.Enabled || !ps.ExposeExternal {
				continue
			}
			proxyTools, proxyHandlers, buildErr := BuildProxyTools(context.Background(), ps, h.sourcePluginAPI)
			if buildErr != nil {
				h.logger.Error("failed to build proxy tools for plugin server; skipping",
					"plugin_id", ps.PluginID, "error", buildErr.Error())
				continue
			}
			// Per-tool policy filter. Construct a synthetic *ServerConfig
			// whose ToolConfigs come from the registered PluginServerConfig,
			// then call GetToolPolicy(toolName) per tool. Empty ToolConfigs
			// → default-allow ("ask", true) for every tool, matching the
			// pre-M2 behavior. Mirrors the internal-path pattern in
			// mcp/client_manager.go:filterToolsByConfig (Phase 1, task 1.6).
			//
			// Enabled is hardcoded true on the synthetic config: server-level
			// enable is already enforced above via ps.Enabled, and
			// GetToolPolicy short-circuits to ("ask", false) when
			// s.Enabled == false — propagating ps.Enabled here would falsely
			// hide every tool of an enabled plugin.
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
// `externalServerRebuilder` interface contract declared (unexported) in
// api/api_bridge_mcp.go as
//
//	type externalServerRebuilder interface { RebuildExternalServer() }
//
// and activated by a nil-safe type assertion on *PluginMCPHandlers at the
// bridge register/unregister handler call sites.
//
// StreamableHTTPOptions{Stateless: true} means no external client sessions are
// disrupted by the swap — each HTTP request opens, serves, and closes.
// BuildProxyTools ephemeral connects run under the write lock; if a plugin MCP
// server hangs on ListTools the rebuild hangs with it, surfacing the failure
// to the admin who triggered the register/unregister.
func (h *PluginMCPHandlers) RebuildExternalServer() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentServer = h.buildServerLocked()
}
