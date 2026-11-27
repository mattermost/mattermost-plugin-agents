// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {MCPServerConfig} from '@/components/system_console/mcp_servers';
import {PersistMCPServerScope} from '@/components/system_console/mcp_types';
import {PluginConfig} from '@/components/system_console/plugin_config_types';

// withSavedMCPServer reflects a server saved on its own in the page's local
// configuration. Only a newly added server is appended: saving an existing
// server's settings or tool policies does not bring back a server removed
// here but not yet saved, and a tool policy save changes only tool_configs.
export const withSavedMCPServer = (prev: PluginConfig, savedServer: MCPServerConfig, scope: PersistMCPServerScope, fallbackMCP: PluginConfig['mcp']): PluginConfig => {
    const localMCP = prev.mcp || fallbackMCP;
    const localServers: MCPServerConfig[] = localMCP.servers ?? [];
    const present = localServers.some((s) => s.id === savedServer.id);
    if (!present && scope !== 'server') {
        return prev;
    }
    const merged = present ? localServers.map((s) => {
        if (s.id !== savedServer.id) {
            return s;
        }
        if (scope === 'tool_configs') {
            return {...s, tool_configs: savedServer.tool_configs};
        }

        // A settings save keeps tool policy edits still pending on this page,
        // unless the server moved to another URL, which drops its policies.
        if (scope === 'settings' && s.baseURL === savedServer.baseURL) {
            return {...savedServer, tool_configs: s.tool_configs};
        }
        return savedServer;
    }) : [...localServers, savedServer];
    return {...prev, mcp: {...localMCP, servers: merged}};
};

// withoutMCPServer removes a server from the page's local configuration.
export const withoutMCPServer = (prev: PluginConfig, serverID: string, fallbackMCP: PluginConfig['mcp']): PluginConfig => {
    const localMCP = prev.mcp || fallbackMCP;
    return {...prev, mcp: {...localMCP, servers: (localMCP.servers ?? []).filter((s: MCPServerConfig) => s.id !== serverID)}};
};
