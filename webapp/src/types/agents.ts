// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

// Mirrors llm.MaxCustomInstructionsRunes on the backend.
export const MaxCustomInstructionsRunes = 100000;

// Mirrors llm.DefaultMaxToolTurns on the backend.
export const DefaultMaxToolTurns = 30;

// Mirrors llm.MaxAllowedMaxToolTurns on the backend.
export const MaxAllowedMaxToolTurns = 250;

// Counts Unicode code points to match Go's utf8.RuneCountInString.
export const codePointLength = (s: string): number => Array.from(s).length;

// EnabledTool matches llm.EnabledMCPTool (persisted agents and config bots).
// Inner field names stay snake_case to match the backend's json:"server_origin"
// / json:"tool_name" tags; see .planning/phase-1/PLAN.md pitfall P2.
export type EnabledTool = {
    server_origin: string; // MCP server origin URL
    tool_name: string; // tool identifier on that server
}

// Reference-document limits. Mirror the constants of the same meaning on the backend.
export const MaxAgentDocuments = 20;
export const MaxAgentDocumentBytes = 10 * 1024 * 1024;
export const MaxAgentDocumentsTextRunes = 100000;
export const MaxAgentDocumentNameLength = 256;

// Largest agent file the import endpoints accept (preview and import bodies).
export const MaxAgentImportFileBytes = 40 * 1024 * 1024;

// AgentDocument matches llm.AgentDocument: a reference to an immutable, content-addressed
// document blob. Extracted text and bytes are never part of the agent.
export type AgentDocument = {
    id: string;
    name: string;
    mimeType: string;
    size: number;
    sha256: string;
    textRunes: number;
}

// The id/name pair the create and update requests carry for each document.
export type AgentDocumentReference = {
    id: string;
    name: string;
}

// Mirrors config.AgentInactiveReason on the backend.
export type AgentInactiveReason = 'invalid_config' | 'service_unavailable' | 'service_not_licensed' | 'agent_limit';

// UserAgent matches the JSON serialization of *llm.BotConfig from the backend.
// The backend API (GET /agents, GET /agents/:id, POST /agents, PUT /agents/:id)
// returns this shape.
//
// NOTE on `name`: the backend emits the agent's Mattermost username under the
// JSON key "name" (llm.BotConfig.Name). The CreateAgentRequest / UpdateAgentRequest
// DTOs accept the same value under the JSON key "username" — see the asymmetry
// called out in §2.5 of .planning/phase-2/PLAN.md. UI layers typically display
// this value prefixed with "@" as the agent's username.
//
// The admin/lifecycle fields (botUserID, creatorID, adminUserIDs, createAt,
// updateAt, deleteAt) are all `omitempty` on the backend; for config-defined
// bots (returned via /agents only if/when surfaced, and for migrated legacy
// bots with CreatorID == "") they may be absent from the response.
//
// MCP tool access is controlled by two independent fields:
// - autoEnableNewMCPTools=true: agent gets every MCP tool, including ones added later.
// - autoEnableNewMCPTools=false: agent gets only the tools listed in enabledMCPTools.
// - mcpDynamicToolLoading=false: agent uses the full MCP schema list instead of JIT loading.
export type UserAgent = {
    id: string;
    name: string;
    displayName: string;
    customInstructions: string;
    serviceID: string;
    model: string;
    enableVision: boolean;
    disableTools: boolean;
    channelAccessLevel: ChannelAccessLevel;

    // Server sends nil Go slices as JSON null.
    channelIDs: string[] | null;
    userAccessLevel: UserAccessLevel;
    userIDs: string[] | null;
    teamIDs: string[] | null;
    enabledNativeTools: string[] | null;
    enabledMCPTools: EnabledTool[] | null;
    autoEnableNewMCPTools: boolean;
    mcpDynamicToolLoading?: boolean;
    useServiceAccountAuth: boolean;
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    maxToolTurns: number;

    // Server sends nil Go slices as JSON null; absent on responses from older servers.
    documents?: AgentDocument[] | null;

    /**
     * @deprecated Structured output is configured per service
     * (LLMService.structuredOutputPolicy), not per agent. The backend still
     * returns this field for older clients; the UI ignores it.
     */
    structuredOutputEnabled?: boolean;

    // Only on GET /agents; absent when the agent is running.
    inactiveReason?: AgentInactiveReason;

    // Admin / lifecycle metadata (omitempty on backend).
    botUserID?: string;
    creatorID?: string;
    adminUserIDs?: string[];
    createAt?: number;
    updateAt?: number;
    deleteAt?: number;
}

// CreateAgentRequest matches api.CreateAgentRequest in Go.
//
// Create is an explicit full-object request: the UI is the sole source of truth for
// create-time defaults, so clients send every field they want persisted. There are no
// hidden server-side defaults layered on top.
//
// MCP tool access is controlled by two independent fields:
//   - `autoEnableNewMCPTools: true`  = agent gets every MCP tool, including ones added later.
//   - `autoEnableNewMCPTools: false` = agent gets only the tools listed in `enabledMCPTools`.
//
// NOTE: this DTO still uses the JSON key "username" (json:"username" in
// api/api_agents.go) even though the response emits the same value as "name".
export type CreateAgentRequest = {
    displayName: string;
    username: string;
    serviceID: string;
    customInstructions?: string;
    channelAccessLevel?: number;
    channelIDs?: string[];
    userAccessLevel?: number;
    userIDs?: string[];
    teamIDs?: string[];
    adminUserIDs?: string[];
    enabledMCPTools?: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    mcpDynamicToolLoading: boolean;

    // Required so payload builders can't silently drop it on full-replace PUT.
    useServiceAccountAuth: boolean;
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    enabledNativeTools?: string[];
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    maxToolTurns?: number;
    documents?: AgentDocumentReference[];
}

// UpdateAgentRequest matches api.UpdateAgentRequest in Go.
//
// Update is a full-object replacement, not a patch: every mutable field the caller wants
// to keep must be sent on every save. Fields omitted here are overwritten with their
// JSON zero values.
export type UpdateAgentRequest = {
    displayName: string;
    username?: string;
    serviceID: string;
    customInstructions?: string;
    channelAccessLevel?: number;
    channelIDs?: string[];
    userAccessLevel?: number;
    userIDs?: string[];
    teamIDs?: string[];
    adminUserIDs?: string[];
    enabledMCPTools?: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    mcpDynamicToolLoading: boolean;
    useServiceAccountAuth: boolean;
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    enabledNativeTools?: string[];
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    maxToolTurns?: number;

    // Full replace: an omitted or empty list removes every document.
    documents?: AgentDocumentReference[];
}

// ServiceInfo matches api.ServiceInfo in Go (safe subset, no secrets).
export type ServiceInfo = {
    id: string;
    name: string;
    type: string;
    defaultModel: string;
    outputTokenLimit: number;
    useResponsesAPI: boolean;
}

// Mirrors the Source column of the agent versions table on the backend.
export type AgentVersionSource = 'initial' | 'create' | 'update' | 'restore' | 'import' | 'system';

// AgentVersionSummary is one entry of GET /agents/:id/versions (newest first).
// changedFields holds BotConfig JSON keys (e.g. "customInstructions"); names only.
export type AgentVersionSummary = {
    version: number;
    createdBy: string; // user ID, '' for system writes
    createAt: number;
    source: AgentVersionSource;
    restoredFromVersion: number;
    changedFields: string[] | null;
}

export type AgentVersionList = {
    currentVersion: number;
    versions: AgentVersionSummary[];
}

// AgentVersionDetail is GET /agents/:id/versions/:version. config is the stored
// llm.BotConfig snapshot, so any field may be missing in older snapshots.
export type AgentVersionDetail = AgentVersionSummary & {
    config: Partial<UserAgent>;
}

export const AgentExportKind = 'mattermost-agent';

// MCP tool reference inside an export document. Matching on import is by serverOrigin.
export type AgentExportMCPTool = {
    serverOrigin: string;
    serverName: string;
    toolName: string;
}

// A reference document inside a schemaVersion 2 export document; content is the
// base64 of the original file bytes.
export type AgentExportDocumentFile = {
    name: string;
    mimeType: string;
    size: number;
    sha256: string;
    content: string;
}

// The `agent` section of an export document (schemaVersion 1 has no documents).
export type AgentExportAgent = {
    name: string;
    displayName: string;
    customInstructions: string;
    disableTools: boolean;
    maxToolTurns: number;
    mcpDynamicToolLoading: boolean;
    autoEnableNewMCPTools: boolean;
    mcpTools: AgentExportMCPTool[];
    documents?: AgentExportDocumentFile[] | null;
}

export type AgentExportDocument = {
    kind: string;
    schemaVersion: 1 | 2;
    exportedAt: number;
    agentVersion: number;
    agent: AgentExportAgent;
}

export type AgentImportMCPServer = {
    sourceOrigin: string;
    sourceName: string;
    toolNames: string[];
    autoTargetOrigin: string; // '' when no server on this instance matches
}

export type AgentImportAvailableMCPServer = {
    origin: string;
    name: string;
}

export type AgentImportExistingAgent = {
    id: string;
    displayName: string;
    username: string;
    canManage: boolean;
}

// Summary of one document in an import preview (no content).
export type AgentImportPreviewDocument = {
    name: string;
    mimeType: string;
    size: number;
}

export type AgentImportPreview = {

    // Normalized echo of the file with document content stripped. Never send this to
    // /agents/import; send the locally parsed file instead.
    document: AgentExportDocument;
    documents?: AgentImportPreviewDocument[] | null;
    mcpServers: AgentImportMCPServer[] | null;
    availableMCPServers: AgentImportAvailableMCPServer[] | null;
    existingAgent?: AgentImportExistingAgent | null;
}

// targetOrigin '' means "remove these tools".
export type AgentImportMCPServerMapping = {
    sourceOrigin: string;
    targetOrigin: string;
}

export type AgentImportMode = 'create' | 'update';

export type AgentImportRequest = {
    document: AgentExportDocument;
    mode: AgentImportMode;
    agentID?: string;
    username?: string;
    displayName?: string;
    serviceID?: string;
    model?: string;
    mcpServerMappings: AgentImportMCPServerMapping[];
}
