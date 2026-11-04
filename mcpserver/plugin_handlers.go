// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	mcppkg "github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	externalProxyDiscoveryTimeout = 10 * time.Second
	maxConcurrentProxyDiscoveries = 32
	nativeMattermostToolOwner     = "mattermost"

	// Bounds of the backoff between rebuilds while a plugin server's tools
	// could not be discovered.
	rebuildRetryBaseDelay = 1 * time.Second
	rebuildRetryMaxDelay  = 1 * time.Minute
)

// errSourcePluginNotRunning marks a discovery skipped because the source
// plugin is registered but not yet (or no longer) serving requests.
var errSourcePluginNotRunning = errors.New("source plugin is not running")

// PluginServerRegistry is the read-side contract for plugin-server aggregation.
type PluginServerRegistry interface {
	ListPluginServers() []mcppkg.PluginServerConfig
}

// Keep this in sync with api/api_bridge_mcp.go's externalServerRebuilder.
var _ interface{ RequestRebuild() } = (*PluginMCPHandlers)(nil)

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

	// The plugin's own search and file-content services, called in-process
	// rather than over an HTTP callback through the public site URL.
	searchService      tools.SemanticSearchService
	fileContentService tools.FileContentService

	accessChecker           mcppkg.ServerAccessChecker
	embeddedServerID        func() string
	allowStateChangingTools func() bool

	// Bounds each source-plugin Connect/ListTools during rebuilds.
	proxyDiscoveryTimeout time.Duration

	// rebuildMu serializes rebuilds so two concurrent rebuilds cannot swap an
	// older registry snapshot over a newer one.
	rebuildMu sync.Mutex

	// The background rebuild loop starts on first use and stops on Close.
	rebuildLoopOnce sync.Once
	rebuildCtx      context.Context
	rebuildCancel   context.CancelFunc
	rebuildNow      chan struct{}
	rebuildDone     chan struct{}
	retryBaseDelay  time.Duration
	retryMaxDelay   time.Duration

	mu            sync.RWMutex
	currentServer *mcp.Server
}

// NewPluginMCPHandlers creates MCP handlers for the Mattermost plugin.
// registry may be nil to disable plugin-server aggregation. searchService and
// fileContentService are the plugin's in-process services; either may be nil
// when the capability is unavailable. Callers must
// inject auth (bearer token or user-ID) via plugin middleware.
// allowStateChangingTools is a runtime predicate evaluated per request; a nil
// predicate means state-changing tools are not available.
// Runtime services, when supplied, are the access checker followed by the
// embedded-server ID lookup. Omitting both preserves compatibility for
// standalone servers without ABAC.
func NewPluginMCPHandlers(
	siteURL, internalURL string,
	logger loggerlib.Logger,
	registry PluginServerRegistry,
	sourcePluginAPI mmapi.Client,
	searchService tools.SemanticSearchService,
	fileContentService tools.FileContentService,
	allowStateChangingTools func() bool,
	runtimeServices ...any,
) (*PluginMCPHandlers, error) {
	if siteURL == "" {
		return nil, fmt.Errorf("site URL cannot be empty")
	}
	if len(runtimeServices) != 0 && len(runtimeServices) != 2 {
		return nil, fmt.Errorf("access checker and embedded server ID lookup must be provided together")
	}

	var accessChecker mcppkg.ServerAccessChecker
	var embeddedServerID func() string
	if len(runtimeServices) == 2 {
		if runtimeServices[0] != nil {
			var ok bool
			accessChecker, ok = runtimeServices[0].(mcppkg.ServerAccessChecker)
			if !ok {
				return nil, fmt.Errorf("invalid access checker")
			}
		}
		if runtimeServices[1] != nil {
			var ok bool
			embeddedServerID, ok = runtimeServices[1].(func() string)
			if !ok {
				return nil, fmt.Errorf("invalid embedded server ID lookup")
			}
		}
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	h := &PluginMCPHandlers{
		siteURL:                 siteURL,
		internalURL:             internalURL,
		logger:                  logger,
		registry:                registry,
		sourcePluginAPI:         sourcePluginAPI,
		searchService:           searchService,
		fileContentService:      fileContentService,
		accessChecker:           accessChecker,
		embeddedServerID:        embeddedServerID,
		allowStateChangingTools: allowStateChangingTools,
		proxyDiscoveryTimeout:   externalProxyDiscoveryTimeout,
		rebuildNow:              make(chan struct{}, 1),
		rebuildDone:             make(chan struct{}),
		retryBaseDelay:          rebuildRetryBaseDelay,
		retryMaxDelay:           rebuildRetryMaxDelay,
	}
	h.rebuildCtx, h.rebuildCancel = context.WithCancel(context.Background())

	var failures map[string]error
	h.currentServer, failures = h.buildServer()
	if len(failures) > 0 {
		h.logDiscoveryFailures(failures, false)
		h.RequestRebuild()
	}

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

// buildServer constructs a fresh *mcp.Server with native + proxy tools, and
// returns the discovery failure of each exposed plugin server it skipped.
// Does not touch currentServer, so callers can drop h.mu during the
// source-plugin Connect/ListTools calls.
func (h *PluginMCPHandlers) buildServer() (*mcp.Server, map[string]error) {
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
	toolProvider := tools.NewMattermostToolProvider(
		authProvider,
		h.logger,
		config,
		tools.AccessModeRemote,
		h.searchService,
		h.fileContentService,
		h.allowStateChangingTools,
	)
	toolProvider.ProvideTools(mcpServer)

	toolOwners := make(map[string]toolOwner, len(toolProvider.ToolNames()))
	for _, toolName := range toolProvider.ToolNames() {
		toolOwners[toolName] = toolOwner{ownerKey: nativeMattermostToolOwner}
	}

	// Disabled plugin tools are not registered on the external server.
	var failures map[string]error
	if h.registry != nil {
		failures = h.addProxyTools(mcpServer, toolOwners)
	}

	mcpServer.AddReceivingMiddleware(h.accessFilterMiddleware(toolOwners))
	return mcpServer, failures
}

// toolOwner is the catalog snapshot for one registered tool.
type toolOwner struct {
	ownerKey string // nativeMattermostToolOwner or source plugin ID
	serverID string // ABAC identity; unused for native tools
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
// always win, and among plugins the first entry in the snapshot wins. It
// returns the discovery failure of each plugin server it skipped.
func (h *PluginMCPHandlers) addProxyTools(mcpServer *mcp.Server, toolOwners map[string]toolOwner) map[string]error {
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

	failures := make(map[string]error)
	for index, ps := range exposed {
		if err := discovered[index].err; err != nil {
			failures[ps.PluginID] = err
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
				if existing.ownerKey == nativeMattermostToolOwner {
					h.logger.Error("proxy tool name conflicts with native Mattermost tool; skipping",
						"tool_name", proxyTool.Name,
						"plugin_id", ps.PluginID)
				} else {
					h.logger.Error("duplicate proxy tool name across plugin MCP servers; skipping",
						"tool_name", proxyTool.Name,
						"plugin_id", ps.PluginID,
						"existing_plugin_id", existing.ownerKey)
				}
				continue
			}
			toolOwners[proxyTool.Name] = toolOwner{ownerKey: ps.PluginID, serverID: ps.ID}
			mcpServer.AddTool(proxyTool, proxyHandlers[i])
		}
	}
	return failures
}

// logDiscoveryFailures logs discovery failures at Warn after a requested
// rebuild and at Debug on timed retries, so a plugin that keeps failing does
// not warn every minute. A source plugin that is not running yet is expected
// during startup and only logged at Debug.
func (h *PluginMCPHandlers) logDiscoveryFailures(failures map[string]error, retrying bool) {
	for pluginID, err := range failures {
		switch {
		case errors.Is(err, errSourcePluginNotRunning):
			h.logger.Debug("plugin MCP server is not running yet; will retry discovery", "plugin_id", pluginID)
		case retrying:
			h.logger.Debug("still unable to build proxy tools for plugin server; will retry", "plugin_id", pluginID, "error", err.Error())
		default:
			h.logger.Warn("failed to build proxy tools for plugin server; will retry", "plugin_id", pluginID, "error", err.Error())
		}
	}
}

func (h *PluginMCPHandlers) currentEmbeddedServerID() string {
	if h.embeddedServerID == nil {
		return ""
	}
	return h.embeddedServerID()
}

func toolUnavailableRPCError() error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: "tool not available",
	}
}

func callToolName(req mcp.Request) string {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return ""
	}
	return params.Name
}

// accessFilterMiddleware hides and rejects tools the caller may not use.
// Decisions are recomputed every request; owners is the build-time snapshot.
func (h *PluginMCPHandlers) accessFilterMiddleware(owners map[string]toolOwner) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if h.accessChecker == nil {
				return next(ctx, method, req)
			}
			if method != "tools/list" && method != "tools/call" {
				return next(ctx, method, req)
			}

			userID, _ := ctx.Value(auth.UserIDContextKey).(string)
			if userID == "" {
				if method == "tools/call" {
					return nil, toolUnavailableRPCError()
				}
				result, err := next(ctx, method, req)
				if err != nil {
					return result, err
				}
				if list, ok := result.(*mcp.ListToolsResult); ok {
					list.Tools = []*mcp.Tool{}
				}
				return result, nil
			}

			cache := map[string]bool{}
			if method == "tools/call" {
				name := callToolName(req)
				if name == "" || h.toolAccessDenied(ctx, userID, name, owners, cache) {
					return nil, toolUnavailableRPCError()
				}
				return next(ctx, method, req)
			}

			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			list, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			filtered := make([]*mcp.Tool, 0, len(list.Tools))
			for _, tool := range list.Tools {
				if h.toolAccessDenied(ctx, userID, tool.Name, owners, cache) {
					continue
				}
				filtered = append(filtered, tool)
			}
			list.Tools = filtered
			return list, nil
		}
	}
}

func (h *PluginMCPHandlers) toolAccessDenied(ctx context.Context, userID, toolName string, owners map[string]toolOwner, cache map[string]bool) bool {
	owner, ok := owners[toolName]
	if !ok {
		return true
	}
	serverID := owner.serverID
	if owner.ownerKey == nativeMattermostToolOwner {
		serverID = h.currentEmbeddedServerID()
	}
	if serverID == "" {
		return false
	}
	if denied, cached := cache[serverID]; cached {
		return denied
	}
	denied := h.accessChecker.CanUseMCPServer(accesscontrol.WithResourceName(ctx, owner.ownerKey), userID, serverID) != nil
	cache[serverID] = denied
	if denied {
		h.logger.Debug("Omitting MCP server for user by access policy", "userID", userID, "serverID", serverID)
	}
	return denied
}

// discoverProxyTools lists one source plugin's tools under the per-plugin
// discovery timeout. A plugin that is not running, fails, or times out is
// skipped; healthy plugins are unaffected.
func (h *PluginMCPHandlers) discoverProxyTools(ps mcppkg.PluginServerConfig) proxyDiscovery {
	running, err := h.sourcePluginRunning(ps.PluginID)
	if err != nil {
		return proxyDiscovery{err: err}
	}
	if !running {
		return proxyDiscovery{err: errSourcePluginNotRunning}
	}

	discoveryCtx, cancel := context.WithTimeout(h.rebuildCtx, h.proxyDiscoveryTimeout)
	defer cancel()

	proxyTools, proxyHandlers, err := BuildProxyTools(discoveryCtx, ps, h.sourcePluginAPI)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
			return proxyDiscovery{err: fmt.Errorf("timed out after %s: %w", h.proxyDiscoveryTimeout, err)}
		}
		return proxyDiscovery{err: err}
	}

	return proxyDiscovery{tools: proxyTools, handlers: proxyHandlers}
}

// sourcePluginRunning reports whether the server routes requests to pluginID.
// A registration can arrive while the source plugin is still in OnActivate,
// and calling it then fails and logs an error on the Mattermost server.
func (h *PluginMCPHandlers) sourcePluginRunning(pluginID string) (bool, error) {
	if h.sourcePluginAPI == nil {
		return false, errors.New("no plugin API to reach source plugins")
	}
	status, err := h.sourcePluginAPI.GetPluginStatus(pluginID)
	if err != nil {
		return false, fmt.Errorf("get plugin status: %w", err)
	}
	return status != nil && status.State == model.PluginStateRunning, nil
}

// RequestRebuild rebuilds the external server on the background loop and
// returns immediately, retrying with backoff while a plugin server cannot be
// discovered. Discovery must not block request handlers, or call back into a
// plugin that is still starting.
func (h *PluginMCPHandlers) RequestRebuild() {
	if h.rebuildCtx.Err() != nil {
		return
	}
	h.rebuildLoopOnce.Do(func() { go h.rebuildLoop() })
	select {
	case h.rebuildNow <- struct{}{}:
	default:
	}
}

// Close stops the background rebuild loop and waits for it to exit.
func (h *PluginMCPHandlers) Close() {
	h.rebuildCancel()
	h.rebuildLoopOnce.Do(func() { close(h.rebuildDone) })
	<-h.rebuildDone
}

func (h *PluginMCPHandlers) rebuild() map[string]error {
	h.rebuildMu.Lock()
	defer h.rebuildMu.Unlock()

	nextServer, failures := h.buildServer()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentServer = nextServer
	return failures
}

// rebuildLoop runs requested rebuilds one at a time, coalescing requests that
// arrive during a rebuild, and retries with backoff while discovery fails. A
// new request resets the backoff.
func (h *PluginMCPHandlers) rebuildLoop() {
	defer close(h.rebuildDone)

	delay := h.retryBaseDelay
	var retry <-chan time.Time
	for {
		retrying := false
		select {
		case <-h.rebuildCtx.Done():
			return
		case <-h.rebuildNow:
			delay = h.retryBaseDelay
		case <-retry:
			retrying = true
		}
		retry = nil

		failures := h.rebuild()
		h.logDiscoveryFailures(failures, retrying)
		if len(failures) > 0 {
			retry = time.After(delay)
			delay = min(delay*2, h.retryMaxDelay)
		}
	}
}
