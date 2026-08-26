// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/utils"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	// embeddedConnectTimeout bounds one in-memory embedded MCP handshake.
	embeddedConnectTimeout = 10 * time.Second
	// pluginConnectTimeout bounds one plugin MCP handshake and initial tools
	// listing. PluginHTTP has no transport-level timeout of its own.
	pluginConnectTimeout = 30 * time.Second
)

// ToolInfo represents a tool's metadata for discovery purposes
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// UserClients represents a per-user MCP client with multiple server connections
type UserClients struct {
	clientsMu    sync.RWMutex
	clients      map[string]*Client // serverID -> client (both remote and embedded)
	userID       string
	log          pluginapi.LogService
	oauthManager *OAuthManager
	httpClient   *http.Client
	toolsCache   *ToolsCache
	// attempts records one connect attempt per server origin. It is what makes
	// this cache incremental: a newly eligible origin is dialed on the request
	// that first needs it, an origin already dialed is not dialed again, and a
	// remembered failure keeps being reported so callers do not lose stable
	// auth-required state on later requests.
	attempts map[string]*originAttempt
	// closed marks the client as torn down so a dial that finishes after
	// Close does not resurrect a session nobody will ever close.
	closed bool
}

// originAttempt is one user's connect attempt against a single MCP server
// origin. done is closed once the attempt finishes, at which point err is
// safe to read; concurrent callers wait on it instead of dialing the same
// server twice for the same user.
type originAttempt struct {
	done chan struct{}
	err  error
}

func newOriginAttempt() *originAttempt {
	return &originAttempt{done: make(chan struct{})}
}

func (a *originAttempt) finished() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// wait blocks until the attempt finishes and returns its outcome. A caller
// whose request is canceled while waiting gets the cancellation error instead;
// the attempt itself keeps running because its result warms a shared cache.
func (a *originAttempt) wait(ctx context.Context) error {
	if a.finished() {
		return a.err
	}
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// connectTask is one pending MCP connection attempt. dial does the network
// work and must not touch shared state: the batch commits the returned clients
// after every dial has finished, so worker completion order cannot decide
// which session ends up cached.
type connectTask struct {
	// origin identifies the server for eligibility and singleflight purposes.
	origin string
	// serverID keys the resulting client in the clients map.
	serverID string
	// serverName labels errors surfaced to callers.
	serverName string
	// retryable re-dials on a later request instead of remembering a failure
	// until the user client is invalidated.
	retryable bool
	// silent keeps failures out of the returned Errors. The embedded server
	// degrades to "no embedded tools" rather than surfacing a user-visible
	// error.
	silent bool
	// replaces commits over an already-connected client for this origin.
	replaces bool
	dial     func() (*Client, error)
}

// connectPlan is a connectTask bound to the attempt that will produce (or has
// already produced) its outcome.
type connectPlan struct {
	task    connectTask
	attempt *originAttempt
	// dialing reports whether this caller owns the attempt. When false the
	// attempt belongs to an earlier or concurrent caller and is only waited on.
	dialing bool
	err     error
}

type userClientSnapshot struct {
	serverID string
	client   *Client
}

func NewUserClients(userID string, log pluginapi.LogService, oauthManager *OAuthManager, httpClient *http.Client, toolsCache *ToolsCache) *UserClients {
	return &UserClients{
		log:          log,
		clients:      make(map[string]*Client),
		attempts:     make(map[string]*originAttempt),
		userID:       userID,
		oauthManager: oauthManager,
		httpClient:   httpClient,
		toolsCache:   toolsCache,
	}
}

// ensureConnections dials every task whose origin has not been connected for
// this user yet. Dials run concurrently and their results are merged in task
// order, so the returned errors do not depend on which server answered first.
func (c *UserClients) ensureConnections(ctx context.Context, tasks []connectTask) *Errors {
	if len(tasks) == 0 {
		return nil
	}

	plans := c.planConnections(tasks)

	dialing := make([]int, 0, len(plans))
	for i := range plans {
		if plans[i].dialing {
			dialing = append(dialing, i)
		}
	}

	dialed := utils.RunParallel(len(dialing), func(i int) (*Client, error) {
		return plans[dialing[i]].task.dial()
	})
	c.commitDials(plans, dialing, dialed)

	// Origins another caller is already dialing resolve to that caller's
	// result, so a user ends up with exactly one session per server.
	for i := range plans {
		if plans[i].dialing {
			continue
		}
		plans[i].err = plans[i].attempt.wait(ctx)
	}

	var mcpErrors *Errors
	for i := range plans {
		mcpErrors = c.recordConnectFailure(mcpErrors, plans[i])
	}
	return mcpErrors
}

// planConnections decides, for each task, whether this caller dials the origin,
// waits on an in-flight attempt, or reuses a remembered outcome.
func (c *UserClients) planConnections(tasks []connectTask) []connectPlan {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	if c.attempts == nil {
		c.attempts = make(map[string]*originAttempt, len(tasks))
	}

	plans := make([]connectPlan, 0, len(tasks))
	for _, task := range tasks {
		if existing := c.attempts[task.origin]; existing != nil {
			switch {
			case !existing.finished():
				plans = append(plans, connectPlan{task: task, attempt: existing})
				continue
			case existing.err == nil && !task.replaces:
				continue
			case existing.err != nil && !task.retryable:
				plans = append(plans, connectPlan{task: task, attempt: existing})
				continue
			}
		}

		attempt := newOriginAttempt()
		c.attempts[task.origin] = attempt
		plans = append(plans, connectPlan{task: task, attempt: attempt, dialing: true})
	}

	return plans
}

// commitDials publishes every dial outcome under one lock so a waiter cannot
// observe a finished attempt before its session is reachable.
func (c *UserClients) commitDials(plans []connectPlan, dialing []int, dialed []utils.ParallelResult[*Client]) {
	if len(dialing) == 0 {
		return
	}

	var discarded []*Client

	c.clientsMu.Lock()
	for i, index := range dialing {
		plan := &plans[index]
		client, err := dialed[i].Value, dialed[i].Err

		plan.err = err
		plan.attempt.err = err

		switch {
		case err != nil || client == nil:
		case c.closed:
			discarded = append(discarded, client)
		default:
			if previous := c.clients[plan.task.serverID]; previous != nil {
				discarded = append(discarded, previous)
			}
			c.clients[plan.task.serverID] = client
		}

		close(plan.attempt.done)
	}
	c.clientsMu.Unlock()

	for _, client := range discarded {
		if err := client.Close(); err != nil {
			c.log.Error("Failed to close superseded MCP client", "userID", c.userID, "error", err)
		}
	}
}

func (c *UserClients) recordConnectFailure(mcpErrors *Errors, plan connectPlan) *Errors {
	if plan.err == nil {
		return mcpErrors
	}

	if plan.task.silent {
		c.log.Debug("MCP server unavailable for user",
			"userID", c.userID, "serverID", plan.task.serverID, "error", plan.err)
		return mcpErrors
	}

	if mcpErrors == nil {
		mcpErrors = &Errors{}
	}

	var oauthErr *OAuthNeededError
	if errors.As(plan.err, &oauthErr) {
		mcpErrors.ToolAuthErrors = append(mcpErrors.ToolAuthErrors, llm.ToolAuthError{
			ServerName:   plan.task.serverName,
			ServerOrigin: plan.task.origin,
			AuthURL:      oauthErr.AuthURL(),
			Error:        plan.err,
		})
		return mcpErrors
	}

	// Only the caller that performed the dial logs it; a remembered failure is
	// replayed on every later request and would otherwise flood the log.
	if plan.dialing {
		c.log.Error("Failed to connect to MCP server",
			"userID", c.userID, "serverID", plan.task.serverID, "serverOrigin", plan.task.origin, "error", plan.err)
	}
	mcpErrors.Errors = append(mcpErrors.Errors, plan.err)
	return mcpErrors
}

// remoteConnectTask builds the task for one admin-configured remote MCP server.
// baseCtx is deliberately uncancelable: the resulting session warms a cache
// shared by later requests, so a closed tab must not poison it. The whole
// connection sequence is bounded inside NewClient instead.
func (c *UserClients) remoteConnectTask(baseCtx context.Context, serverConfig ServerConfig, forceRefresh bool) connectTask {
	return connectTask{
		origin:     serverConfig.BaseURL,
		serverID:   serverConfig.Name,
		serverName: serverConfig.Name,
		dial: func() (*Client, error) {
			return NewClient(baseCtx, c.userID, serverConfig, c.log, c.oauthManager, c.httpClient, c.toolsCache, forceRefresh)
		},
	}
}

// embeddedConnectTask builds the task for the embedded Mattermost MCP server.
// It replaces any cached session because the caller only schedules it when the
// user's embedded session ID changed.
func (c *UserClients) embeddedConnectTask(ctx context.Context, sessionID string, embeddedClient *EmbeddedServerClient) connectTask {
	return connectTask{
		origin:     EmbeddedClientKey,
		serverID:   EmbeddedClientKey,
		serverName: EmbeddedServerName,
		retryable:  true,
		silent:     true,
		replaces:   true,
		dial: func() (*Client, error) {
			dialCtx, cancel := context.WithTimeout(ctx, embeddedConnectTimeout)
			defer cancel()

			client, err := embeddedClient.CreateClient(dialCtx, c.userID, sessionID)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to embedded server: %w", err)
			}
			return client, nil
		},
	}
}

// pluginConnectTask builds the task for a plugin-registered MCP server.
// Failures stay retryable: a source plugin that is briefly unreachable must
// come back on the next request without waiting for cache invalidation.
func (c *UserClients) pluginConnectTask(ctx context.Context, cfg PluginServerConfig, sourcePluginAPI mmapi.Client) connectTask {
	origin := pluginServerOriginKey(cfg.PluginID)
	return connectTask{
		origin:     origin,
		serverID:   origin,
		serverName: cfg.Name,
		retryable:  true,
		dial: func() (*Client, error) {
			deadline := newConnectDeadline(ctx, pluginConnectTimeout)
			client, err := NewPluginClient(deadline.context(), c.userID, cfg, sourcePluginAPI, c.log)
			if err != nil {
				deadline.abandon()
				return nil, err
			}
			if !deadline.keep() {
				_ = client.Close()
				return nil, fmt.Errorf("timed out connecting to plugin MCP server %s after %s", cfg.PluginID, pluginConnectTimeout)
			}
			return client, nil
		},
	}
}

// needsEmbeddedReconnect reports whether the user's embedded session differs
// from the one backing the cached embedded client.
func (c *UserClients) needsEmbeddedReconnect(sessionID string) bool {
	if sessionID == "" {
		return false
	}

	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	existing := c.clients[EmbeddedClientKey]
	return existing == nil || existing.sessionID != sessionID
}

func (c *UserClients) hasClient(serverID string) bool {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	_, exists := c.clients[serverID]
	return exists
}

func (c *UserClients) snapshotClients() []userClientSnapshot {
	c.clientsMu.RLock()
	defer c.clientsMu.RUnlock()
	if len(c.clients) == 0 {
		return nil
	}

	serverIDs := make([]string, 0, len(c.clients))
	for serverID := range c.clients {
		serverIDs = append(serverIDs, serverID)
	}
	sort.Strings(serverIDs)

	snapshot := make([]userClientSnapshot, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		snapshot = append(snapshot, userClientSnapshot{
			serverID: serverID,
			client:   c.clients[serverID],
		})
	}
	return snapshot
}

// Close closes all server connections for a user client
func (c *UserClients) Close() {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Close all MCP server clients (both remote and embedded)
	for serverID, client := range c.clients {
		if err := client.Close(); err != nil {
			c.log.Error("Failed to close MCP client", "userID", c.userID, "serverID", serverID, "error", err)
		}
	}

	// Clear clients
	c.clients = make(map[string]*Client)
	// Drop remembered attempts too: a reused instance must re-dial rather than
	// keep replaying failures for sessions that no longer exist.
	c.attempts = make(map[string]*originAttempt)
	c.closed = true
}

// GetTools returns the tools available from the clients
func (c *UserClients) GetTools(ctx context.Context) []llm.Tool {
	clientSnapshot := c.snapshotClients()
	if len(clientSnapshot) == 0 {
		return nil
	}

	var tools []llm.Tool
	seenTools := make(map[string]string) // runtime toolName -> serverID for conflict detection
	usedSlugs := make(map[string]string) // slug -> server origin for collision suffixing

	// Iterate over a snapshot so callers do not hold clientsMu during network work.
	for _, entry := range clientSnapshot {
		serverID := entry.serverID
		client := entry.client
		clientTools := client.Tools()
		serverSlug := dedupeMCPServerSlug(mcpServerSlug(serverID, client), client.config.BaseURL, serverID, usedSlugs)
		toolNames := make([]string, 0, len(clientTools))
		for toolName := range clientTools {
			toolNames = append(toolNames, toolName)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			tool := clientTools[toolName]
			runtimeToolName := llm.NamespaceMCPToolName(serverSlug, toolName)
			// Namespacing should make cross-server duplicate bare names safe. A
			// final collision means the slug de-dupe or upstream catalog is broken.
			if existingServerID, exists := seenTools[runtimeToolName]; exists {
				c.log.Warn("Namespaced MCP tool name conflict detected",
					"userID", c.userID,
					"tool", runtimeToolName,
					"server1", existingServerID,
					"server2", serverID)
				continue
			}
			seenTools[runtimeToolName] = serverID

			tools = append(tools, llm.Tool{
				Name:         runtimeToolName,
				Description:  tool.Description,
				Schema:       tool.InputSchema,
				Resolver:     c.createToolResolver(client, toolName),
				ServerOrigin: client.config.BaseURL,
			})
		}
	}

	return tools
}

// prepareToolCallMetadata prepares metadata to be sent with MCP tool calls.
// Per-call metadata is sourced from the tool itself (set at scope-time via
// llm.Tool.WithCallMetadata) so callers can plumb runtime info — like before-hook
// keys — without leaking it into the LLM-visible schema or onto llm.Context.
// bot_user_id is sourced from llm.Context because it is identity, not per-call config.
func (c *UserClients) prepareToolCallMetadata(client *Client, toolName string, llmContext *llm.Context) map[string]any {
	if llmContext == nil {
		return nil
	}

	// Only inject metadata for the embedded server.
	if client.config.Name != EmbeddedClientKey {
		return nil
	}

	var metadata map[string]any
	if llmContext.Tools != nil {
		if tool := llmContext.Tools.GetTool(toolName); tool != nil && len(tool.CallMetadata) > 0 {
			metadata = make(map[string]any, len(tool.CallMetadata)+1)
			for k, v := range tool.CallMetadata {
				metadata[k] = v
			}
		}
	}

	if llmContext.BotUserID != "" {
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		metadata["bot_user_id"] = llmContext.BotUserID
	}

	return metadata
}

func (c *UserClients) clearOAuthNeededForServer(client *Client) {
	if c.oauthManager == nil || client == nil || client.config.Name == "" {
		return
	}
	if err := c.oauthManager.DeleteAuthNeededState(c.userID, client.config.Name); err != nil {
		c.log.Debug("Failed to clear MCP OAuth-needed state after successful tool call",
			"userID", c.userID,
			"serverID", client.config.Name,
			"error", err)
	}
}

func (c *UserClients) rememberOAuthNeededForToolCall(client *Client, err error) {
	if c.oauthManager == nil || client == nil || client.config.Name == "" || err == nil {
		return
	}

	oauthErr := client.oauthNeededError(err)
	if oauthErr == nil {
		return
	}

	var needed *OAuthNeededError
	if !errors.As(oauthErr, &needed) {
		return
	}

	authURL := needed.AuthURL()
	if authURL == "" {
		authURL = c.oauthManager.StartURL(client.config.Name)
	}
	if authURL == "" {
		return
	}

	if storeErr := c.oauthManager.StoreAuthNeededState(c.userID, client.config.Name, authURL); storeErr != nil {
		c.log.Warn("Failed to persist MCP OAuth-needed state after tool call",
			"userID", c.userID,
			"serverID", client.config.Name,
			"error", storeErr)
	}
}

// createToolResolver creates a resolver function for the given tool
func (c *UserClients) createToolResolver(client *Client, toolName string) llm.ToolResolver {
	return func(ctx context.Context, llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args map[string]any
		if err := argsGetter(&args); err != nil {
			return "", fmt.Errorf("failed to get arguments for tool %s: %w", toolName, err)
		}

		metadata := c.prepareToolCallMetadata(client, toolName, llmContext)

		result, err := client.CallToolWithMetadata(ctx, toolName, args, metadata)
		if err != nil {
			c.rememberOAuthNeededForToolCall(client, err)
			return result, err
		}

		c.clearOAuthNeededForServer(client)
		return result, nil
	}
}

func mcpServerSlug(serverID string, client *Client) string {
	if client != nil && (client.config.BaseURL == EmbeddedClientKey || client.config.Name == EmbeddedClientKey || serverID == EmbeddedClientKey) {
		return "mattermost"
	}

	candidates := []string{}
	if client != nil {
		candidates = append(candidates, client.config.Name)
	}
	candidates = append(candidates, serverID)
	if client != nil && client.config.BaseURL != "" {
		if parsed, err := url.Parse(client.config.BaseURL); err == nil {
			baseURLName := strings.Trim(strings.Trim(parsed.Host+parsed.Path, "/"), "_")
			candidates = append(candidates, baseURLName)
		}
	}
	candidates = append(candidates, "mcp")

	for _, candidate := range candidates {
		if slug := sanitizeMCPServerSlug(candidate); slug != "" {
			return slug
		}
	}
	return "mcp"
}

func dedupeMCPServerSlug(slug, serverOrigin, serverID string, usedSlugs map[string]string) string {
	if slug == "" {
		slug = "mcp"
	}
	if existingOrigin, exists := usedSlugs[slug]; !exists || existingOrigin == serverOrigin {
		usedSlugs[slug] = serverOrigin
		return slug
	}

	hashInput := serverOrigin
	if hashInput == "" {
		hashInput = serverID
	}
	if hashInput == "" {
		hashInput = slug
	}
	dedupedSlug := slug + "_" + shortSlugHash(hashInput)
	usedSlugs[dedupedSlug] = serverOrigin
	return dedupedSlug
}

func sanitizeMCPServerSlug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastWasSeparator := false
	for _, r := range value {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAllowed {
			b.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		if b.Len() > 0 && !lastWasSeparator {
			b.WriteByte('_')
			lastWasSeparator = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func shortSlugHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

// pluginServerOriginKey returns the synthetic origin string for plugin-server
// tools. Must match the key used by filterToolsByConfig.
func pluginServerOriginKey(pluginID string) string {
	return "plugin://" + pluginID
}
