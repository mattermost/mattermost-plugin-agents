// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

// EnabledTool matches useragents.EnabledTool in Go
export type EnabledTool = {
    type: string;         // "mcp" or "builtin"
    id: string;           // tool identifier
    name: string;         // display name
    serverName?: string;  // MCP server name (for type "mcp")
}

// UserAgent matches the JSON serialization of useragents.UserAgent from the backend.
// The backend API (GET /agents, GET /agents/:id) returns this shape.
export type UserAgent = {
    id: string;
    botUserId: string;
    creatorId: string;
    displayName: string;
    username: string;
    serviceId: string;
    customInstructions: string;
    channelAccessLevel: ChannelAccessLevel;
    channelIds: string[];
    userAccessLevel: UserAccessLevel;
    userIds: string[];
    teamIds: string[];
    adminUserIds: string[];
    enabledTools: EnabledTool[];
    createAt: number;
    updateAt: number;
    deleteAt: number;
}

// CreateAgentRequest matches api.CreateAgentRequest in Go.
export type CreateAgentRequest = {
    display_name: string;
    username: string;
    service_id: string;
    custom_instructions?: string;
    channel_access_level?: number;
    channel_ids?: string[];
    user_access_level?: number;
    user_ids?: string[];
    team_ids?: string[];
    admin_user_ids?: string[];
    enabled_tools?: EnabledTool[];
}

// UpdateAgentRequest matches api.UpdateAgentRequest in Go.
// All fields are optional (pointer fields in Go → undefined in TS).
export type UpdateAgentRequest = {
    display_name?: string;
    username?: string;
    service_id?: string;
    custom_instructions?: string;
    channel_access_level?: number;
    channel_ids?: string[];
    user_access_level?: number;
    user_ids?: string[];
    team_ids?: string[];
    admin_user_ids?: string[];
    enabled_tools?: EnabledTool[];
}

// ServiceInfo matches api.ServiceInfo in Go (safe subset, no secrets).
export type ServiceInfo = {
    id: string;
    name: string;
    type: string;
    default_model: string;
}
