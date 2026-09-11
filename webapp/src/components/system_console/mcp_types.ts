// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Types mirror config/mcp_config.go JSON tags.

export type MCPToolConfig = {
    name: string;
    policy: 'auto_run_in_dm' | 'auto_run_everywhere' | 'ask';
    enabled: boolean;
    retrieval_description_override?: string;
};

export type MCPServerConfig = {
    id?: string; // stable ABAC policy identity; may be absent until the server-side ID migration runs
    name: string;
    enabled: boolean;
    baseURL: string;
    headers: {[key: string]: string};
    tool_configs?: MCPToolConfig[];
    clientID?: string;
    clientSecret?: string;
};

export type MCPEmbeddedServerConfig = {
    id?: string; // stable ABAC policy identity; may be absent until the server-side ID migration runs
    enabled: boolean;
    tool_configs?: MCPToolConfig[];
};

// Mirrors config.PluginServerConfig (json:"plugin_servers").
export type PluginServerConfig = {
    id?: string;
    plugin_id: string;
    name: string;
    path: string;
    enabled: boolean;
    expose_external: boolean;
    tool_configs?: MCPToolConfig[];
};

export type MCPConfig = {
    enabled: boolean;
    enablePluginServer: boolean;
    servers: MCPServerConfig[] | null; // server sends nil Go slice as JSON null
    plugin_servers?: PluginServerConfig[] | null;
    embeddedServer: MCPEmbeddedServerConfig;
    idleTimeoutMinutes?: number;
};

export type MCPToolInfo = {
    name: string;
    description: string;
    inputSchema: Record<string, unknown> | null;
};

export type MCPServerInfo = {
    name: string;
    url: string;
    tools: MCPToolInfo[];
    needsOAuth: boolean;
    oauthURL?: string;
    error: string | null;

    // Plugin-server fields; remote and embedded rows read state from mcpConfig.
    serverType?: string;
    enabled?: boolean;
    toolConfigs?: MCPToolConfig[];

    // Stable ABAC policy identity when present.
    id?: string;
};

export type MCPToolsResponse = {
    servers: MCPServerInfo[];
};
