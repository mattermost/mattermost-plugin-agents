// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MCPServerKind} from '@/client';

export type MCPServerStatus =
    | 'connected'
    | 'no-sa-credentials'
    | 'sa-connect-failed'
    | 'sa-only-unavailable'
    | 'not-connected'
    | 'none';

export type MCPAvailabilityServer = {
    kind: MCPServerKind;
    authenticated: boolean;
    needsOAuth: boolean;
    serviceAccountConfigured: boolean;
    authEmail?: string;
    authURL?: string;
    tools: readonly unknown[];
};

function isLocalMCPServer(kind: MCPServerKind): boolean {
    return kind === 'embedded' || kind === 'plugin';
}

// Mutually exclusive UI status for one MCP server. Compute once per row.
export function mcpServerStatus(
    server: MCPAvailabilityServer,
    useServiceAccountAuth: boolean,
): MCPServerStatus {
    if (server.authenticated) {
        return 'connected';
    }
    if (isLocalMCPServer(server.kind)) {
        if (!server.authEmail && server.tools.length === 0 && !server.authURL) {
            return 'not-connected';
        }
        return 'none';
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
