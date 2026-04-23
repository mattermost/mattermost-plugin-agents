// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { type OpenAIMockContainer } from './openai-mock';

/**
 * Registers Smocker mocks that simulate an MCP server requiring OAuth authentication.
 *
 * The mock server:
 * 1. Returns 401 with WWW-Authenticate header on any MCP connection attempt
 * 2. Serves OAuth protected-resource metadata at /.well-known/oauth-protected-resource
 * 3. Serves authorization-server metadata at /.well-known/oauth-authorization-server
 *
 * The plugin's MCP client will detect the 401, discover OAuth endpoints,
 * and populate authURL / needsOAuth in the GET /mcp/tools response.
 */

const MOCK_MCP_BASE = 'http://openai:8080';
const MOCK_MCP_PATH = '/mock-mcp';

export const MOCK_OAUTH_SERVER_NAME = 'Mock OAuth Server';
export const MOCK_OAUTH_SERVER_URL = `${MOCK_MCP_BASE}${MOCK_MCP_PATH}`;

/** Smocker mock rule shape used by OpenAIMockContainer.addMocks */
export type SmockerMock = {
    request: {
        method: string;
        path: string | { matcher: string; value: string };
    };
    context?: { times?: number };
    response: {
        status: number;
        headers: Record<string, string>;
        body: string;
    };
};

export function buildMCPOAuthMocks(completionResponse?: string): SmockerMock[] {
    const mocks: SmockerMock[] = [];

    if (completionResponse) {
        mocks.push({
            request: {
                method: 'POST',
                path: {
                    matcher: 'ShouldMatch',
                    value: '^(/v1)?/chat/completions$',
                },
            },
            context: { times: 100 },
            response: {
                status: 200,
                headers: { 'Content-Type': 'text/event-stream' },
                body: completionResponse,
            },
        });
    }

    mocks.push(
        {
            request: {
                method: 'POST',
                path: MOCK_MCP_PATH,
            },
            context: { times: 100 },
            response: {
                status: 401,
                headers: {
                    'WWW-Authenticate': `Bearer resource_metadata="${MOCK_MCP_BASE}/.well-known/oauth-protected-resource"`,
                },
                body: 'Unauthorized',
            },
        },
        {
            request: {
                method: 'GET',
                path: MOCK_MCP_PATH,
            },
            context: { times: 100 },
            response: {
                status: 401,
                headers: {
                    'WWW-Authenticate': `Bearer resource_metadata="${MOCK_MCP_BASE}/.well-known/oauth-protected-resource"`,
                },
                body: 'Unauthorized',
            },
        },
        {
            request: {
                method: 'GET',
                path: '/.well-known/oauth-protected-resource',
            },
            context: { times: 100 },
            response: {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    resource: MOCK_OAUTH_SERVER_URL,
                    authorization_servers: [MOCK_MCP_BASE],
                }),
            },
        },
        {
            request: {
                method: 'GET',
                path: '/.well-known/oauth-authorization-server',
            },
            context: { times: 100 },
            response: {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    issuer: MOCK_MCP_BASE,
                    authorization_endpoint: `${MOCK_MCP_BASE}/authorize`,
                    token_endpoint: `${MOCK_MCP_BASE}/token`,
                    response_types_supported: ['code'],
                    grant_types_supported: ['authorization_code'],
                    code_challenge_methods_supported: ['S256'],
                }),
            },
        },
    );

    return mocks;
}

export async function registerMCPOAuthMocks(
    openAIMock: OpenAIMockContainer,
    completionResponse?: string,
): Promise<void> {
    const mocks = buildMCPOAuthMocks(completionResponse);
    await openAIMock.addMocks(mocks);
}
