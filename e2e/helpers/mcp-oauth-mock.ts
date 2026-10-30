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

export const MOCK_OAUTH_ACCESS_TOKEN = 'mock-mcp-access-token';
export const MOCK_OAUTH_AUTHORIZE_URL = `${MOCK_MCP_BASE}/authorize`;
export const MOCK_CONNECTED_TOOL_NAME = 'list_my_tickets';
export const MOCK_SERVER_SELF_DESCRIPTION = 'Look up and update your support tickets.';

type SmockerDynamicMock = {
    request: {
        method: string;
        path: string;
        headers?: Record<string, string>;
        body?: { matcher: string; value: string };
    };
    context?: { times?: number };
    dynamic_response: { engine: 'go_template_json'; script: string };
};

// rpcMethod matches the JSON-RPC method exactly; a trailing "/" matches every method under that prefix.
function authorizedMCPRequest(method: string, rpcMethod?: string): SmockerDynamicMock['request'] {
    const methodMatch = rpcMethod?.endsWith('/') ? `"method":"${rpcMethod}` : `"method":"${rpcMethod}"`;
    return {
        method,
        path: MOCK_MCP_PATH,
        headers: { Authorization: `Bearer ${MOCK_OAUTH_ACCESS_TOKEN}` },
        ...(rpcMethod ? { body: { matcher: 'ShouldContainSubstring', value: methodMatch } } : {}),
    };
}

// jsonRPCResponse echoes the request's JSON-RPC id, which the MCP client matches responses on.
function jsonRPCResponse(payload: string): SmockerDynamicMock['dynamic_response'] {
    return {
        engine: 'go_template_json',
        script: `{"status":200,"headers":{"Content-Type":"application/json"},"body":{"jsonrpc":"2.0","id":{{ toJson .Request.Body.id }},${payload}}}`,
    };
}

/**
 * Mocks that let the OAuth flow from buildMCPOAuthMocks complete: the token
 * endpoint issues MOCK_OAUTH_ACCESS_TOKEN, and MCP requests carrying it get a
 * working session with one tool, from a server that describes itself as
 * MOCK_SERVER_SELF_DESCRIPTION. Servers configured with that token as a static
 * Authorization header reach the same session without OAuth. Register them after buildMCPOAuthMocks so they
 * take priority over its 401 responses. The authorization endpoint itself is
 * not mocked: it lives on the Docker network, so tests intercept
 * MOCK_OAUTH_AUTHORIZE_URL in the browser instead.
 */
export function buildMCPOAuthConnectedMocks(
    tools: Array<{ name: string; description: string; readOnlyHint?: boolean }> = [
        { name: MOCK_CONNECTED_TOOL_NAME, description: "List the user's open tickets" },
    ],
): Array<SmockerMock | SmockerDynamicMock> {
    const toolList = JSON.stringify(tools.map((t) => ({
        name: t.name,
        description: t.description,
        inputSchema: { type: 'object', properties: {} },
        ...(t.readOnlyHint === undefined ? {} : { annotations: { readOnlyHint: t.readOnlyHint } }),
    })));
    return [
        {
            request: { method: 'POST', path: '/token' },
            context: { times: 100 },
            response: {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ access_token: MOCK_OAUTH_ACCESS_TOKEN, token_type: 'Bearer', expires_in: 3600 }),
            },
        },
        {
            request: authorizedMCPRequest('GET'),
            context: { times: 100 },
            response: { status: 405, headers: {}, body: '' },
        },
        {
            request: authorizedMCPRequest('DELETE'),
            context: { times: 100 },
            response: { status: 200, headers: {}, body: '' },
        },
        {
            request: authorizedMCPRequest('POST', 'notifications/'),
            context: { times: 100 },
            response: { status: 202, headers: {}, body: '' },
        },
        {
            // The client falls back to the initialize handshake when discovery is unsupported.
            request: authorizedMCPRequest('POST', 'server/discover'),
            context: { times: 100 },
            dynamic_response: jsonRPCResponse('"error":{"code":-32601,"message":"Method not found"}'),
        },
        {
            request: authorizedMCPRequest('POST', 'initialize'),
            context: { times: 100 },
            dynamic_response: jsonRPCResponse(
                `"result":{"protocolVersion":{{ toJson .Request.Body.params.protocolVersion }},"capabilities":{"tools":{}},"serverInfo":{"name":"mock-tickets","version":"1.0.0","description":"${MOCK_SERVER_SELF_DESCRIPTION}"}}`,
            ),
        },
        {
            request: authorizedMCPRequest('POST', 'tools/list'),
            context: { times: 100 },
            dynamic_response: jsonRPCResponse(`"result":{"tools":${toolList}}`),
        },
    ];
}

export async function registerMCPOAuthMocks(
    openAIMock: OpenAIMockContainer,
    completionResponse?: string,
): Promise<void> {
    const mocks = buildMCPOAuthMocks(completionResponse);
    await openAIMock.addMocks(mocks);
}
