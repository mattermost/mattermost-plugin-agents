// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type MCPServerStatus =
    | 'connected'
    | 'no-sa-credentials'
    | 'sa-connect-failed'
    | 'sa-only-unavailable'
    | 'not-connected'
    | 'none';

export type MCPAvailabilityServer = {
    authenticated: boolean;
    needsOAuth: boolean;
    serviceAccountConfigured: boolean;
    authEmail?: string;
    authURL?: string;
    tools: readonly unknown[];
};

// Mutually exclusive UI status for one MCP server. Compute once per row.
export function mcpServerStatus(
    server: MCPAvailabilityServer,
    useServiceAccountAuth: boolean,
): MCPServerStatus {
    if (server.authenticated) {
        return 'connected';
    }
    if (useServiceAccountAuth) {
        if (!server.serviceAccountConfigured) {
            return 'no-sa-credentials';
        }
        return 'sa-connect-failed';
    }
    if (!server.needsOAuth && server.serviceAccountConfigured) {
        return 'sa-only-unavailable';
    }

    // OAuth-needed rows with an authURL are 'none': Connect handles them.
    if (!server.authEmail && server.tools.length === 0 && !server.authURL) {
        return 'not-connected';
    }
    return 'none';
}
