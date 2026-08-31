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
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	maxConcurrentConnections = 32
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

	// admission is the manager-wide network-connection gate. Nil in tests that
	// construct a UserClients directly, which then skip the aggregate cap.
	admission *connectionAdmission
	// connectTimeout overrides the production remote/plugin connect budget.
	// Zero means the production defaults. Tests use a short value so queue
	// time can be distinguished from the admitted timeout.
	connectTimeout time.Duration
}

// originAttempt is one user's connect attempt against a single MCP server
// origin. done is closed once the attempt finishes, at which point err is
// safe to read; concurrent callers wait on it instead of dialing the same
// server twice for the same user.
type originAttempt struct {
	done chan struct{}
	err  error
	// identity is the connection identity used when this attempt was created.
	// ReInit compares it to the new config so a still-valid session is kept.
	identity string
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
	// An already-finished attempt reports its own outcome even to a canceled
	// caller, so a remembered failure is reported the same way every time.
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
	// identity is the connection identity recorded on the attempt so ReInit
	// can tell a still-valid session from a stale one.
	identity string
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
	client  *Client
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

// ensureConnections plans then executes tasks. Direct test callers use this
// wrapper; ClientManager plans under lifecycleMu and executes after unlock.
func (c *UserClients) ensureConnections(ctx context.Context, tasks []connectTask) *Errors {
	plans, discarded := c.planConnections(tasks)
	c.closeDiscardedClients(discarded)
	return c.executeConnections(ctx, plans)
}

// executeConnections dials every planned origin concurrently and merges
// results in task order, so the returned errors do not depend on which server
// answered first.
func (c *UserClients) executeConnections(ctx context.Context, plans []connectPlan) *Errors {
	if len(plans) == 0 {
		return nil
	}

	dialers := make([]*connectPlan, 0, len(plans))
	for i := range plans {
		if plans[i].dialing {
			dialers = append(dialers, &plans[i])
		}
	}

	slots := make(chan struct{}, maxConcurrentConnections)
	var wg sync.WaitGroup
	for _, plan := range dialers {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			plan.client, plan.err = plan.task.dial()
		})
	}
	wg.Wait()

	c.commitDials(dialers)

	var mcpErrors *Errors
	for i := range plans {
		if !plans[i].dialing {
			// An origin another caller is already dialing resolves to that
			// caller's result, so a user ends up with one session per server.
			plans[i].err = plans[i].attempt.wait(ctx)
		}
		mcpErrors = c.recordConnectFailure(mcpErrors, plans[i])
	}
	return mcpErrors
}

// planConnections decides, for each task, whether this caller dials the origin,
// waits on an in-flight attempt, or reuses a remembered outcome. An existing
// attempt whose identity does not match the task is detached so a later commit
// cannot resurrect the old session. Callers must close the returned clients
// after releasing any manager lifecycle lock.
func (c *UserClients) planConnections(tasks []connectTask) ([]connectPlan, []*Client) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	if c.closed {
		return nil, nil
	}

	if c.attempts == nil {
		c.attempts = make(map[string]*originAttempt, len(tasks))
	}

	var discarded []*Client
	plans := make([]connectPlan, 0, len(tasks))
	for _, task := range tasks {
		if existing := c.attempts[task.origin]; existing != nil && existing.identity != task.identity {
			delete(c.attempts, task.origin)
			discarded = append(discarded, c.takeClientsForOriginLocked(task.origin)...)
		}

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

		attempt := &originAttempt{done: make(chan struct{}), identity: task.identity}
		c.attempts[task.origin] = attempt
		plans = append(plans, connectPlan{task: task, attempt: attempt, dialing: true})
	}

	return plans, discarded
}

// commitDials publishes every dial outcome under one lock so a waiter cannot
// observe a finished attempt before its session is reachable.
func (c *UserClients) commitDials(dialers []*connectPlan) {
	if len(dialers) == 0 {
		return
	}

	var discarded []*Client

	c.clientsMu.Lock()
	for _, plan := range dialers {
		current := c.attempts[plan.task.origin]
		stale := current != plan.attempt
		admissionFailure := isConnectionAdmissionError(plan.err)

		if admissionFailure && !stale {
			// A local gate/shutdown error is not an upstream failure. Drop the
			// attempt so a later request can retry instead of remembering it.
			delete(c.attempts, plan.task.origin)
		}

		if plan.client != nil && (c.closed || stale || admissionFailure) {
			discarded = append(discarded, plan.client)
			plan.client = nil
		}

		if !stale && !admissionFailure && plan.err == nil && plan.client != nil && !c.closed {
			if previous := c.clients[plan.task.serverID]; previous != nil {
				discarded = append(discarded, previous)
			}
			c.clients[plan.task.serverID] = plan.client
		}

		if !plan.attempt.finished() {
			plan.attempt.err = plan.err
			close(plan.attempt.done)
		}
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

	if oauthErr, ok := errors.AsType[*OAuthNeededError](plan.err); ok {
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
		identity:   remoteOriginIdentity(serverConfig, false),
		dial: func() (*Client, error) {
			if err := c.acquireNetworkAdmission(cacheableContext(baseCtx)); err != nil {
				return nil, err
			}
			defer c.releaseNetworkAdmission()
			return newClientWithTimeout(baseCtx, c.networkConnectTimeout(RemoteConnectTimeout), c.userID, serverConfig, c.log, c.oauthManager, c.httpClient, c.toolsCache, forceRefresh)
		},
	}
}

// embeddedConnectTask builds the task for the embedded Mattermost MCP server.
// It replaces any cached session because the caller only schedules it when the
// user's embedded session ID changed.
func (c *UserClients) embeddedConnectTask(ctx context.Context, sessionID string, embeddedClient *EmbeddedServerClient) connectTask {
	var server EmbeddedMCPServer
	if embeddedClient != nil {
		server = embeddedClient.server
	}
	return connectTask{
		origin:     EmbeddedClientKey,
		serverID:   EmbeddedClientKey,
		serverName: EmbeddedServerName,
		retryable:  true,
		silent:     true,
		replaces:   true,
		identity:   embeddedOriginIdentity(server, true),
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
func (c *UserClients) pluginConnectTask(ctx context.Context, cfg PluginServerConfig, sourcePluginAPI mmapi.Client, registered bool) connectTask {
	origin := pluginServerOriginKey(cfg.PluginID)
	return connectTask{
		origin:     origin,
		serverID:   origin,
		serverName: cfg.Name,
		retryable:  true,
		identity:   pluginOriginIdentity(cfg, registered),
		dial: func() (*Client, error) {
			if err := c.acquireNetworkAdmission(ctx); err != nil {
				return nil, err
			}
			defer c.releaseNetworkAdmission()
			return connectWithDeadline(ctx, c.networkConnectTimeout(pluginConnectTimeout), "plugin MCP server "+cfg.PluginID,
				func(connectCtx context.Context) (*Client, error) {
					return NewPluginClient(connectCtx, c.userID, cfg, sourcePluginAPI, c.log)
				})
		},
	}
}

func (c *UserClients) acquireNetworkAdmission(ctx context.Context) error {
	if c.admission == nil {
		return nil
	}
	return c.admission.acquire(ctx)
}

func (c *UserClients) releaseNetworkAdmission() {
	if c.admission == nil {
		return
	}
	c.admission.release()
}

func (c *UserClients) networkConnectTimeout(fallback time.Duration) time.Duration {
	if c.connectTimeout > 0 {
		return c.connectTimeout
	}
	return fallback
}

// applyOriginIdentities detaches then closes sessions whose recorded
// connection identity no longer matches the live configuration. Manager
// ReInit uses detachInvalidIdentities under lifecycleMu and closes after
// unlock; this wrapper remains for direct callers.
func (c *UserClients) applyOriginIdentities(valid map[string]string) {
	c.closeDiscardedClients(c.detachInvalidIdentities(valid))
}

// detachInvalidIdentities drops attempts and clients whose identity is no
// longer live. The returned clients must be closed after any manager
// lifecycle lock is released. A raw BaseURL spelling change is treated as a
// new origin even when both spellings canonicalize to the same endpoint.
func (c *UserClients) detachInvalidIdentities(valid map[string]string) []*Client {
	if c == nil {
		return nil
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if c.closed {
		return nil
	}

	var discarded []*Client
	for origin, attempt := range c.attempts {
		if attempt != nil && valid[origin] == attempt.identity {
			continue
		}
		delete(c.attempts, origin)
		discarded = append(discarded, c.takeClientsForOriginLocked(origin)...)
	}

	for serverID, client := range c.clients {
		origin := clientOrigin(client, serverID)
		attempt := c.attempts[origin]
		if attempt != nil && valid[origin] == attempt.identity {
			continue
		}
		delete(c.clients, serverID)
		discarded = append(discarded, client)
	}
	return discarded
}

// forgetOrigins drops cached sessions and in-flight attempts for specific
// origins, then closes the detached clients.
func (c *UserClients) forgetOrigins(origins ...string) {
	c.closeDiscardedClients(c.detachOrigins(origins...))
}

// detachOrigins removes attempts and clients for the given origins. Callers
// close the returned clients after releasing manager lifecycle/plugin locks.
func (c *UserClients) detachOrigins(origins ...string) []*Client {
	if c == nil || len(origins) == 0 {
		return nil
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if c.closed {
		return nil
	}

	var discarded []*Client
	for _, origin := range origins {
		delete(c.attempts, origin)
		discarded = append(discarded, c.takeClientsForOriginLocked(origin)...)
	}
	return discarded
}

func (c *UserClients) detachAll() []*Client {
	if c == nil {
		return nil
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	discarded := make([]*Client, 0, len(c.clients))
	for _, client := range c.clients {
		if client != nil {
			discarded = append(discarded, client)
		}
	}
	c.clients = make(map[string]*Client)
	c.attempts = make(map[string]*originAttempt)
	c.closed = true
	return discarded
}

func (c *UserClients) closeDiscardedClients(discarded []*Client) {
	for _, client := range discarded {
		if client == nil {
			continue
		}
		if err := client.Close(); err != nil {
			c.log.Error("Failed to close invalidated MCP client", "userID", c.userID, "error", err)
		}
	}
}

func (c *UserClients) takeClientsForOriginLocked(origin string) []*Client {
	var discarded []*Client
	for serverID, client := range c.clients {
		if clientOrigin(client, serverID) == origin {
			discarded = append(discarded, client)
			delete(c.clients, serverID)
		}
	}
	return discarded
}

func clientOrigin(client *Client, serverID string) string {
	if client != nil && client.config.BaseURL != "" {
		return client.config.BaseURL
	}
	return serverID
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

// Close marks the user client torn down and closes detached sessions after
// releasing the user-client lock.
func (c *UserClients) Close() {
	c.closeDiscardedClients(c.detachAll())
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
