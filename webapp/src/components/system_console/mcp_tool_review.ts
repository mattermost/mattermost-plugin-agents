// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {MCPServerConfig, MCPToolConfig, MCPToolInfo} from './mcp_types';

// countUnreviewedTools counts discovered tools that have no chosen policy.
export const countUnreviewedTools = (tools: MCPToolInfo[], toolConfigs?: MCPToolConfig[]): number => {
    const chosen = new Set((toolConfigs ?? []).filter((tc) => tc.policy).map((tc) => tc.name));
    return tools.filter((tool) => !chosen.has(tool.name)).length;
};

// reviewToolConfigs gives every discovered tool an explicit policy: the one an
// admin already chose, else the server-computed suggestion. Stored entries for
// tools the server no longer lists are kept.
export const reviewToolConfigs = (server: MCPServerConfig, tools: MCPToolInfo[]): MCPToolConfig[] => {
    const stored = server.tool_configs ?? [];
    const discovered = new Set(tools.map((tool) => tool.name));
    const reviewed = tools.map((tool): MCPToolConfig => {
        const existing = stored.find((tc) => tc.name === tool.name);
        if (existing?.policy) {
            return existing;
        }
        return {
            ...existing,
            name: tool.name,
            policy: tool.suggestedPolicy || tool.defaultPolicy || 'ask',
            enabled: existing?.enabled ?? true,
        };
    });
    return [...reviewed, ...stored.filter((tc) => !discovered.has(tc.name))];
};
