// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {mcpServerStatus, type MCPAvailabilityServer, type MCPServerStatus} from './mcp_availability';

function server(overrides: Partial<MCPAvailabilityServer> & Pick<MCPAvailabilityServer, 'authenticated'>): MCPAvailabilityServer {
    return {
        needsOAuth: false,
        serviceAccountConfigured: false,
        tools: [],
        ...overrides,
    };
}

type StatusCase = {
    name: string;
    server: MCPAvailabilityServer;
    useServiceAccountAuth: boolean;
    expected: MCPServerStatus;
};

describe('mcpServerStatus', () => {
    test.each<StatusCase>([
        {
            name: 'authenticated is connected in user mode',
            server: server({authenticated: true, serviceAccountConfigured: true}),
            useServiceAccountAuth: false,
            expected: 'connected',
        },
        {
            name: 'authenticated is connected in SA mode',
            server: server({authenticated: true, serviceAccountConfigured: false}),
            useServiceAccountAuth: true,
            expected: 'connected',
        },
        {
            name: 'authenticated Mattermost embedded server',
            server: server({authenticated: true, serviceAccountConfigured: true}),
            useServiceAccountAuth: false,
            expected: 'connected',
        },
        {
            name: 'SA mode without credentials',
            server: server({authenticated: false, serviceAccountConfigured: false}),
            useServiceAccountAuth: true,
            expected: 'no-sa-credentials',
        },
        {
            name: 'SA mode with credentials but not authenticated',
            server: server({authenticated: false, serviceAccountConfigured: true}),
            useServiceAccountAuth: true,
            expected: 'sa-connect-failed',
        },
        {
            name: 'user mode SA-only server is unavailable',
            server: server({authenticated: false, needsOAuth: false, serviceAccountConfigured: true}),
            useServiceAccountAuth: false,
            expected: 'sa-only-unavailable',
        },
        {
            name: 'user mode OAuth-needed is not unavailable',
            server: server({
                authenticated: false,
                needsOAuth: true,
                serviceAccountConfigured: true,
                authURL: 'http://localhost/oauth/start',
            }),
            useServiceAccountAuth: false,
            expected: 'none',
        },
        {
            name: 'user mode unauthenticated without SA headers',
            server: server({authenticated: false, serviceAccountConfigured: false}),
            useServiceAccountAuth: false,
            expected: 'not-connected',
        },
        {
            name: 'user mode OAuth-needed with authURL is none (Connect handles it)',
            server: server({
                authenticated: false,
                needsOAuth: true,
                serviceAccountConfigured: false,
                authURL: 'http://localhost/oauth/start',
            }),
            useServiceAccountAuth: false,
            expected: 'none',
        },
        {
            name: 'user mode unauthenticated with auth email is none',
            server: server({authenticated: false, authEmail: 'user@example.com'}),
            useServiceAccountAuth: false,
            expected: 'none',
        },
        {
            name: 'user mode unauthenticated with discovered tools is none',
            server: server({authenticated: false, tools: [{name: 'a'}]}),
            useServiceAccountAuth: false,
            expected: 'none',
        },
    ])('$name', ({server: mcpServer, useServiceAccountAuth, expected}) => {
        expect(mcpServerStatus(mcpServer, useServiceAccountAuth)).toBe(expected);
    });
});
