// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { type OpenAIMockContainer } from './openai-mock';

/**
 * Smocker mocks for a minimal, unauthenticated Streamable HTTP MCP server
 * that lists a fixed set of tools. Responses echo the JSON-RPC id (and the
 * negotiated protocol version) from the request, so the go-sdk client accepts
 * them. The server/discover probe is answered with "method not found" so the
 * client falls back to the legacy initialize handshake.
 */

export type MockMCPTool = { name: string; description: string };

const jsonRPCResponse = (payload: string): string => JSON.stringify({
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    body: '__PAYLOAD__',
}).replace('"__PAYLOAD__"', `{"jsonrpc": "2.0", "id": {{ .Request.Body.id }}, ${payload}}`);

function methodRequest(path: string, method: string, { prefix = false } = {}) {
    return {
        method: 'POST',
        path,
        body: { matcher: 'ShouldContainSubstring', value: `"method":"${method}${prefix ? '' : '"'}` },
    };
}

/**
 * Builds the mocks for an MCP server served by the Smocker container at
 * `path` (exact match, so include any trailing slash the configured BaseURL has).
 */
export function buildMCPToolsServerMocks(path: string, tools: MockMCPTool[]): unknown[] {
    const toolList = JSON.stringify(tools.map((t) => ({
        name: t.name,
        description: t.description,
        inputSchema: { type: 'object', properties: {} },
    })));

    return [
        {
            request: methodRequest(path, 'server/discover'),
            dynamic_response: {
                engine: 'go_template_json',
                script: jsonRPCResponse('"error": {"code": -32601, "message": "Method not found"}'),
            },
        },
        {
            request: methodRequest(path, 'initialize'),
            dynamic_response: {
                engine: 'go_template_json',
                script: jsonRPCResponse(
                    '"result": {"protocolVersion": "{{ .Request.Body.params.protocolVersion }}", ' +
                    '"capabilities": {"tools": {}}, "serverInfo": {"name": "mock-mcp-tools", "version": "1.0.0"}}',
                ),
            },
        },
        {
            request: methodRequest(path, 'tools/list'),
            dynamic_response: {
                engine: 'go_template_json',
                script: jsonRPCResponse(`"result": {"tools": ${toolList}}`),
            },
        },
        {
            request: methodRequest(path, 'notifications/', { prefix: true }),
            response: { status: 202, headers: {}, body: '' },
        },
        {
            request: { method: 'GET', path },
            response: { status: 405, headers: {}, body: '' },
        },
        {
            request: { method: 'DELETE', path },
            response: { status: 405, headers: {}, body: '' },
        },
    ];
}

/** Adds the MCP server mocks without replacing mocks already registered. */
export async function registerMCPToolsServerMocks(
    openAIMock: OpenAIMockContainer,
    path: string,
    tools: MockMCPTool[],
): Promise<void> {
    await openAIMock.appendMocks(buildMCPToolsServerMocks(path, tools));
}
