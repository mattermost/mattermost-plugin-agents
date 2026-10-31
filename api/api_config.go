// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

func normalizeAdminConfig(cfg config.Config) config.Config {
	cfg.MCP.Enabled = true
	cfg.MCP.EmbeddedServer.Enabled = true

	for i := range cfg.Services {
		if cfg.Services[i].Type == llm.ServiceTypeOpenAI {
			cfg.Services[i].UseResponsesAPI = true
		}
	}

	return cfg
}

// validateServiceStructuredOutputPolicies rejects a service whose
// structured-output policy is not a value the runtime understands. Persisting a
// typo would take every agent on that service offline (EnsureBots skips an
// invalid service) and 404 every LLM Bridge call to it, while the admin UI
// still renders the unrecognized value as "Auto".
func validateServiceStructuredOutputPolicies(cfg config.Config) error {
	for _, svc := range cfg.Services {
		if llm.IsValidStructuredOutputPolicy(svc.StructuredOutputPolicy) {
			continue
		}
		return fmt.Errorf("service %q has an unrecognized structuredOutputPolicy %q: accepted values are %q, %q, %q, or empty for the default",
			svc.ID, svc.StructuredOutputPolicy,
			llm.StructuredOutputPolicyAuto, llm.StructuredOutputPolicyNative, llm.StructuredOutputPolicyPromptFallback)
	}
	return nil
}

// mintEmptyAdminIDs assigns a stable ID to every service and MCP server
// (external, embedded, plugin) that arrived without one. Empty IDs are
// creates; this runs on write only so GET cannot invent unpersisted identities.
func mintEmptyAdminIDs(cfg config.Config) config.Config {
	for i := range cfg.Services {
		if cfg.Services[i].ID == "" {
			cfg.Services[i].ID = model.NewId()
		}
	}
	for i := range cfg.MCP.Servers {
		if cfg.MCP.Servers[i].ID == "" {
			cfg.MCP.Servers[i].ID = model.NewId()
		}
	}
	if cfg.MCP.EmbeddedServer.ID == "" {
		cfg.MCP.EmbeddedServer.ID = model.NewId()
	}
	for i := range cfg.MCP.PluginServers {
		if cfg.MCP.PluginServers[i].ID == "" {
			cfg.MCP.PluginServers[i].ID = model.NewId()
		}
	}
	return cfg
}

// handleGetConfig returns the current plugin configuration from the database.
// GET /admin/config
func (a *API) handleGetConfig(c *gin.Context) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get config: %w", err))
		return
	}

	if cfg == nil {
		c.JSON(http.StatusOK, normalizeAdminConfig(config.Config{
			Services: []llm.ServiceConfig{},
			Bots:     []llm.BotConfig{},
			MCP: mcp.Config{
				Enabled: true,
				Servers: []mcp.ServerConfig{},
				EmbeddedServer: mcp.EmbeddedServerConfig{
					Enabled: true,
				},
			},
			WebSearch: config.WebSearchConfig{
				DomainDenylist: []string{},
			},
		}))
		return
	}

	// Clone before normalizeAdminConfig: it mutates Services (e.g. UseResponsesAPI); the store
	// pointer may alias the in-memory cached config, and GET must not mutate shared state.
	c.JSON(http.StatusOK, normalizeAdminConfig(*cfg.Clone()))
}

// handleSaveConfig saves a new plugin configuration to the database,
// updates the in-memory configuration, and notifies other cluster nodes.
// It responds with the normalized saved config so clients can adopt
// server-minted service/MCP server IDs without a refetch.
// PUT /admin/config
func (a *API) handleSaveConfig(c *gin.Context) {
	var cfg config.Config
	if err := c.BindJSON(&cfg); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Names key per-user clients and OAuth grants, while URLs key the shared
	// tools cache, so duplicate names or endpoints cannot be persisted.
	if err := cfg.MCP.Validate(); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid MCP configuration: %w", err))
		return
	}

	if err := validateServiceStructuredOutputPolicies(cfg); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if err := cfg.ValidateAgentReferences(); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if cfg.AgentVersionsToKeep < 0 {
		c.AbortWithError(http.StatusBadRequest, errors.New("agentVersionsToKeep must be 0 (keep every version) or more"))
		return
	}
	resolvedAgentNames := a.resolveAgentReferenceUsernames(cfg)

	// Read-previous → identity checks → mint empty IDs → save runs atomically
	// under the config advisory lock. Duplicate IDs and embedded ID mismatch
	// abort with 409. Empty list-item IDs represent creates and receive fresh
	// IDs; they never reclaim an existing identity by name or origin.
	var changedKeys []string
	var prevServers []config.MCPServerConfig
	saved, err := a.configStore.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		next := config.ReconcileAgentReferences(prev, cfg)
		next, _ = next.MigrateAgentReferences(resolvedAgentNames)
		if err := config.ValidateServiceIDUniqueness(next.Services); err != nil {
			return config.Config{}, err
		}
		var prevMCP config.MCPConfig
		if prev != nil {
			prevMCP = prev.MCP
		}
		prevServers = append([]config.MCPServerConfig(nil), prevMCP.Servers...)
		reconciledMCP, reconcileErr := config.ReconcileMCPConfigIDs(next.MCP, prevMCP)
		if reconcileErr != nil {
			return config.Config{}, reconcileErr
		}
		next.MCP = reconciledMCP
		normalized := mintEmptyAdminIDs(normalizeAdminConfig(next))
		if err := config.ValidateLicenseTransition(prev, normalized, a.licenseChecker, vettedDefaultToolPolicy); err != nil {
			return config.Config{}, err
		}
		changedKeys = audit.ChangedJSONKeys(prev, normalized)
		return normalized, nil
	})
	switch {
	case errors.Is(err, enterprise.ErrNotLicensed):
		abortNotLicensed(c, err)
		return
	case errors.Is(err, config.ErrServiceIDConflict), errors.Is(err, config.ErrMCPServerIDConflict):
		// Duplicate payload IDs, or an embedded server ID that does not match storage.
		c.AbortWithError(http.StatusConflict, fmt.Errorf("configuration payload has duplicate service or MCP server IDs, or an embedded server ID that does not match the stored identity: %w", err))
		return
	case errors.Is(err, store.ErrLegacyUUIDServiceID):
		// After the ABAC ID migration, a dashed UUID is an invalid service ID format.
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid service ID format: %w", err))
		return
	case err != nil:
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save config: %w", err))
		return
	}

	// From here on the config HAS changed in the database. If a later step
	// fails (cluster notify), the audit record's fail status would otherwise
	// hide a real mutation — mark it explicitly.
	audit.AddParam(auditRec(c), "changed_keys", changedKeys)
	audit.AddParam(auditRec(c), "persisted", true)

	if a.bots != nil {
		for _, username := range saved.LegacyAgentReferences() {
			a.pluginAPI.Log.Warn("Saved agent setting selects an agent by username: no agent account holds it", "username", username)
		}
	}

	// Remove saved sign-ins the change orphaned or that were issued for a
	// server's old origin. Grants are only used by the server they were
	// issued for (its origin and configuration ID; see
	// mcp.OAuthManager.loadGrantForServer), so this removes what the change
	// made unusable rather than guarding against misuse.
	a.clearMCPServerOAuthState(c, prevServers, saved.MCP.Servers)

	// Update in-memory config on this node
	a.configUpdater.Update(&saved)

	// Notify other cluster nodes to reload config from DB
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.JSON(http.StatusOK, saved)
}

// clearMCPServerOAuthState deletes users' stored OAuth state for remote MCP
// servers that a configuration change deleted, renamed, moved to another
// origin, or added under a name an earlier server used (see
// mcp.OAuthStateToClear). The audit record carries the IDs of those servers.
//
// The configuration is already saved, so the sweep is not tied to the request:
// an admin closing the page must not stop it halfway. A failure is logged and
// marked in the audit record. It leaves unusable grants behind rather than
// letting one reach another server: a grant is only used by a server with the
// origin and configuration ID it was issued for. The exception is a grant
// saved before grants recorded a configuration ID: it is bound to the ID of
// whichever server holds its name when it is first used, so deleting a server
// and adding another under the same name at the same origin relies on this
// sweep to remove such grants.
func (a *API) clearMCPServerOAuthState(c *gin.Context, prev, next []config.MCPServerConfig) {
	clears := mcp.OAuthStateToClear(prev, next)
	if len(clears) == 0 {
		return
	}
	names := make([]string, 0, len(clears))
	serverIDs := make([]string, 0, len(clears))
	for _, entry := range clears {
		names = append(names, entry.Name)
		if entry.ServerID != "" && !slices.Contains(serverIDs, entry.ServerID) {
			serverIDs = append(serverIDs, entry.ServerID)
		}
	}
	audit.AddParam(auditRec(c), audit.KeyMCPSignInsClearedServerIDs, audit.TruncateIDs(serverIDs))

	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), mcpSignInClearTimeout)
	defer cancel()
	deleted, err := mcp.ClearServerOAuthState(ctx, &a.pluginAPI.KV, names)
	if err != nil {
		a.pluginAPI.Log.Warn("Failed to remove saved MCP sign-ins after a configuration change; they can no longer be used with the changed servers", "server_ids", serverIDs, "deleted", deleted, "error", err)
		audit.AddParam(auditRec(c), audit.KeyMCPSignInsClearFailed, true)
		return
	}
	if deleted > 0 {
		a.pluginAPI.Log.Info("Removed saved MCP sign-ins after a configuration change", "server_ids", serverIDs, "deleted", deleted)
	}
}

// mcpSignInClearTimeout bounds the sweep of saved MCP sign-ins after a
// configuration change. It is a variable only so tests can shorten it.
var mcpSignInClearTimeout = 2 * time.Minute

// vettedDefaultToolPolicy returns the vetted default policy the System Console
// seeds for a tool on a server, so seeded defaults are not treated as an
// admin-authored tool approval policy by the license gate.
func vettedDefaultToolPolicy(serverBaseURL, toolName string) string {
	baseURL := serverBaseURL
	if baseURL == config.MCPEmbeddedServerOrigin {
		baseURL = mcp.EmbeddedClientKey
	}
	for _, tc := range mcp.SeedVettedToolConfigs(baseURL) {
		if tc.Name == toolName {
			return tc.Policy
		}
	}
	return ""
}

// resolveAgentReferenceUsernames resolves the legacy usernames cfg sets to bot
// user IDs, outside the config transaction. A name set beside an ID is
// resolved too: reconciling with the stored config can keep the name instead.
func (a *API) resolveAgentReferenceUsernames(cfg config.Config) map[string]string {
	if a.bots == nil {
		return nil
	}
	var usernames []string
	for _, username := range []string{cfg.DefaultBotName, cfg.TranscriptGenerator} {
		if username != "" {
			usernames = append(usernames, username)
		}
	}
	resolved, _ := bots.ResolveAgentUsernames(a.bots, usernames)
	return resolved
}
