// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

// EnabledTool matches llm.EnabledMCPTool (persisted agents and config bots).
// Inner field names stay snake_case to match the backend's json:"server_origin"
// / json:"tool_name" tags; see .planning/phase-1/PLAN.md pitfall P2.
export type EnabledTool = {
    server_origin: string; // MCP server origin URL
    tool_name: string; // tool identifier on that server
}

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
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    structuredOutputEnabled: boolean;
    maxToolTurns: number;

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
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    enabledNativeTools?: string[];
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    structuredOutputEnabled?: boolean;
    maxToolTurns?: number;
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
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    enabledNativeTools?: string[];
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    structuredOutputEnabled?: boolean;
    maxToolTurns?: number;
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
