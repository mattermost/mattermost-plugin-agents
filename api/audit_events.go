// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"reflect"
	"runtime"

	"github.com/gin-gonic/gin"
)

// Audit event names for every state-changing operation in the plugin: 21
// routed events plus the non-gin MCP session grant. All are declared here,
// including ones whose instrumentation lands in later changes, so call sites
// never use inline string literals and parallel work never edits this file.
//
// The server stamps the plugin ID into every record's parameters when it is
// logged, so the names stay unprefixed.
const (
	// Config.
	AuditEventSaveConfig = "saveConfig"

	// Admin operations.
	AuditEventReindexPosts          = "reindexPosts"
	AuditEventCancelReindexJob      = "cancelReindexJob"
	AuditEventCatchUpReindex        = "catchUpReindex"
	AuditEventClearMCPToolsCache    = "clearMCPToolsCache"
	AuditEventUpdateMCPPluginServer = "updateMCPPluginServer"

	// Agent CRUD.
	AuditEventCreateAgent       = "createAgent"
	AuditEventUpdateAgent       = "updateAgent"
	AuditEventDeleteAgent       = "deleteAgent"
	AuditEventUpdateAgentAvatar = "updateAgentAvatar"

	// Custom prompts.
	AuditEventCreateCustomPrompt = "createCustomPrompt"
	AuditEventUpdateCustomPrompt = "updateCustomPrompt"
	AuditEventDeleteCustomPrompt = "deleteCustomPrompt"

	// Credentials: third-party MCP OAuth grant/revocation and per-user tool
	// provider preferences.
	AuditEventMCPOAuthStart            = "mcpOAuthStart"
	AuditEventMCPOAuthCallback         = "mcpOAuthCallback"
	AuditEventMCPOAuthDisconnect       = "mcpOAuthDisconnect"
	AuditEventUpdateMCPUserPreferences = "updateMCPUserPreferences"

	// Inter-plugin MCP server registration (bridge routes).
	AuditEventRegisterMCPPluginServer   = "registerMCPPluginServer"
	AuditEventUnregisterMCPPluginServer = "unregisterMCPPluginServer"

	// In-channel tool approval: the human decision the server cannot see.
	AuditEventToolCallApproval   = "toolCallApproval"
	AuditEventToolResultApproval = "toolResultApproval"

	// MCP session grant: an external MCP client obtained a dedicated session
	// holding API access as the user. Not a gin route — emitted directly by
	// delegateToMCPHandler when a session is newly created, so it has no
	// registry entry below.
	AuditEventMCPSessionGrant = "mcpSessionGrant"
)

// handlerFuncName returns the fully-qualified function name of h, which is
// exactly what gin's Context.HandlerName() reports for the route's final
// handler (both use runtime.FuncForPC). Method values produce per-method
// "-fm" thunks whose names are independent of the receiver instance.
func handlerFuncName(h gin.HandlerFunc) string {
	return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
}

// buildAuditEventRegistry maps gin handler names to audit event names for
// every audited route. Routes absent from this registry are not audited:
// auditing is an explicit per-route opt-in, never inferred from the HTTP
// method (two of the most security-relevant operations are mutating GETs).
//
// This is the complete agreed scope. Adding a route here without
// instrumentation is intentional: the middleware already emits actor,
// outcome, path, and trace ID for it; handler enrichment lands separately.
func buildAuditEventRegistry(a *API) map[string]string {
	return map[string]string{
		// Config.
		handlerFuncName(a.handleSaveConfig): AuditEventSaveConfig,

		// Admin operations.
		handlerFuncName(a.handleReindexPosts):       AuditEventReindexPosts,
		handlerFuncName(a.handleCancelJob):          AuditEventCancelReindexJob,
		handlerFuncName(a.handleCatchUpIndex):       AuditEventCatchUpReindex,
		handlerFuncName(a.handleClearMCPToolsCache): AuditEventClearMCPToolsCache,
		handlerFuncName(a.handleUpdatePluginServer): AuditEventUpdateMCPPluginServer,

		// Agent CRUD.
		handlerFuncName(a.handleCreateAgent):       AuditEventCreateAgent,
		handlerFuncName(a.handleUpdateAgent):       AuditEventUpdateAgent,
		handlerFuncName(a.handleDeleteAgent):       AuditEventDeleteAgent,
		handlerFuncName(a.handleUploadAgentAvatar): AuditEventUpdateAgentAvatar,

		// Custom prompts.
		handlerFuncName(a.handleCreateCustomPrompt): AuditEventCreateCustomPrompt,
		handlerFuncName(a.handleUpdateCustomPrompt): AuditEventUpdateCustomPrompt,
		handlerFuncName(a.handleDeleteCustomPrompt): AuditEventDeleteCustomPrompt,

		// Credentials.
		handlerFuncName(a.handleOAuthStart):         AuditEventMCPOAuthStart,
		handlerFuncName(a.handleOAuthCallback):      AuditEventMCPOAuthCallback,
		handlerFuncName(a.handleDeleteUserMCPOAuth): AuditEventMCPOAuthDisconnect,
		handlerFuncName(a.handlePutUserPreferences): AuditEventUpdateMCPUserPreferences,

		// Inter-plugin MCP server registration.
		handlerFuncName(a.handleMCPRegister):   AuditEventRegisterMCPPluginServer,
		handlerFuncName(a.handleMCPUnregister): AuditEventUnregisterMCPPluginServer,

		// Tool approval.
		handlerFuncName(a.handleToolCall):   AuditEventToolCallApproval,
		handlerFuncName(a.handleToolResult): AuditEventToolResultApproval,
	}
}
