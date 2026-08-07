// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// Parameter keys for object identifiers in audit records. They reuse the
// attribute key strings from telemetry/attributes.go so audit records and
// traces share one vocabulary and join without a translation table.
//
// Values must be identifiers only — never prompt content, conversation or
// channel content, tool arguments/results, tokens, or configuration values.
var (
	KeyUserID           = string(telemetry.UserID)
	KeyChannelID        = string(telemetry.ChannelID)
	KeyPostID           = string(telemetry.PostID)
	KeyThreadRootPostID = string(telemetry.ThreadRootPostID)
	KeyAgentID          = string(telemetry.AgentID)
	KeyAgentName        = string(telemetry.AgentName)
	KeyToolName         = string(telemetry.ToolName)
	KeyToolID           = string(telemetry.ToolID)
	KeyMCPServer        = string(telemetry.MCPServer)
	KeyMCPTool          = string(telemetry.MCPTool)
)

const (
	// KeyCallerPluginID identifies the calling plugin on inter-plugin bridge
	// routes. It must not be named "plugin_id": the server unconditionally
	// stamps that parameter with this plugin's own ID when the record is
	// logged (see PluginAPI.LogAuditRecWithLevel), which would overwrite it.
	KeyCallerPluginID = "agents.caller_plugin.id"

	// KeyMCPPluginID identifies the target plugin of an admin operation on a
	// plugin-backed MCP server. Distinct from KeyCallerPluginID (the actor).
	KeyMCPPluginID = "agents.mcp_plugin.id"

	// MetaTraceID is the meta key carrying the OpenTelemetry trace ID of the
	// request, letting an auditor pivot from an audit record to the full
	// trace of what happened.
	MetaTraceID = "trace_id"
)
