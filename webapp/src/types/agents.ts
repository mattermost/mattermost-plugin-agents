// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

// EnabledTool matches useragents.EnabledTool in Go
export type EnabledTool = {
    server_origin: string;  // MCP server origin URL
    tool_name: string;      // tool identifier on that server
}

// UserAgent matches the JSON serialization of useragents.UserAgent from the backend.
// The backend API (GET /agents, GET /agents/:id) returns this shape.
export type UserAgent = {
    id: string;
    bot_user_id: string;
    creator_id: string;
    display_name: string;
    username: string;
    service_id: string;
    custom_instructions: string;
    channel_access_level: ChannelAccessLevel;
    channel_ids: string[];
    user_access_level: UserAccessLevel;
    user_ids: string[];
    team_ids: string[];
    admin_user_ids: string[];
    enabled_tools: EnabledTool[];
    model: string;
    enable_vision: boolean;
    disable_tools: boolean;
    enabled_native_tools: string[];
    reasoning_enabled: boolean;
    reasoning_effort: string;
    thinking_budget: number;
    structured_output_enabled: boolean;
    create_at: number;
    update_at: number;
    delete_at: number;
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
    model?: string;
    enable_vision?: boolean;
    disable_tools?: boolean;
    enabled_native_tools?: string[];
    reasoning_enabled?: boolean;
    reasoning_effort?: string;
    thinking_budget?: number;
    structured_output_enabled?: boolean;
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
    model?: string;
    enable_vision?: boolean;
    disable_tools?: boolean;
    enabled_native_tools?: string[];
    reasoning_enabled?: boolean;
    reasoning_effort?: string;
    thinking_budget?: number;
    structured_output_enabled?: boolean;
}

// ServiceInfo matches api.ServiceInfo in Go (safe subset, no secrets).
export type ServiceInfo = {
    id: string;
    name: string;
    type: string;
    default_model: string;
    output_token_limit: number;
    use_responses_api: boolean;
}
