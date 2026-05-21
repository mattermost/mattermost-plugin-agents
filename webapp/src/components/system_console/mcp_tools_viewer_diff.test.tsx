// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, waitFor} from '@testing-library/react';

// Minimal react-intl shim — see mcp_tools_viewer.test.tsx for rationale.
jest.mock('react-intl', () => {
    const ReactLocal = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const interpolate = (msg: string, values?: Record<string, unknown>) => {
        if (!values) {
            return msg;
        }
        return msg.replace(/{(\w+)}/g, (_, k) => String(values[k] ?? ''));
    };

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactLocal.createElement(ReactLocal.Fragment, null, children),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage?: string; values?: Record<string, unknown>}) =>
            ReactLocal.createElement(ReactLocal.Fragment, null, interpolate(defaultMessage ?? '', values)),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}, values?: Record<string, unknown>) =>
                interpolate(defaultMessage ?? '', values),
        }),
    };
});

jest.mock('../../client', () => ({
    __esModule: true,
    getMCPTools: jest.fn().mockResolvedValue({servers: []}),
    clearMCPToolsCache: jest.fn(),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
    updatePluginServer: jest.fn().mockResolvedValue({}),
}));

type ServerConfigChangeCb = (cfg: {
    name: string;
    enabled: boolean;
    baseURL: string;
    headers: Record<string, string>;
    tool_configs?: Array<{name: string; policy: string; enabled: boolean}>;
}) => void;

const capturedHandlers: Array<{cb: ServerConfigChangeCb; serverName: string}> = [];

jest.mock('./mcp_server_tool_row', () => ({
    __esModule: true,
    default: (props: any) => { // eslint-disable-line @typescript-eslint/no-explicit-any
        capturedHandlers.push({cb: props.onServerConfigChange, serverName: props.server?.name ?? ''});
        return React.createElement('div', {'data-testid': 'row-stub'}, null);
    },
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {updatePluginServer} from '../../client';

import MCPToolsViewer, {MCPToolsResponse} from './mcp_tools_viewer';
import {MCPConfig} from './mcp_servers';
/* eslint-enable import/first, import/order */

const mockUpdatePluginServer = updatePluginServer as jest.Mock;

function makeMCPConfig(): MCPConfig {
    return {
        enabled: true,
        enablePluginServer: true,
        servers: [],
        embeddedServer: {enabled: false, tool_configs: []},
    };
}

function makePluginToolsResponse(overrides: Partial<MCPToolsResponse['servers'][number]> = {}): MCPToolsResponse {
    return {
        servers: [{
            name: 'Demo Plugin',
            url: 'plugin://com.example.demo/mcp',
            serverType: 'plugin',
            enabled: true,
            tools: [
                {name: 'com_example_demo__echo', description: 'Echo', inputSchema: null},
            ],
            needsOAuth: false,
            error: null,
            toolConfigs: [{name: 'com_example_demo__echo', policy: 'ask', enabled: true}],
            ...overrides,
        }],
    };
}

function renderViewer(toolsData: MCPToolsResponse = makePluginToolsResponse()) {
    return render(
        <IntlProvider locale='en'>
            <MCPToolsViewer
                mcpConfig={makeMCPConfig()}
                onConfigChange={jest.fn()}
                initialToolsData={toolsData}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    capturedHandlers.length = 0;
    mockUpdatePluginServer.mockClear();
    mockUpdatePluginServer.mockResolvedValue({});
});

describe('MCPToolsViewer — plugin branch diff edge cases', () => {
    test('clearing all tool_configs sends {tool_configs: []} (load-bearing CLEAR)', async () => {
        renderViewer();
        expect(capturedHandlers.length).toBeGreaterThanOrEqual(1);

        // Pointer semantics: non-nil empty slice clears policy; nil preserves it.
        capturedHandlers[0].cb({
            name: 'Demo Plugin',
            enabled: true,
            baseURL: 'plugin://com.example.demo/mcp',
            headers: {},
            tool_configs: [],
        });

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        const [pluginID, update] = mockUpdatePluginServer.mock.calls[0];
        expect(pluginID).toBe('com.example.demo');

        expect(update).toHaveProperty('tool_configs');
        expect(Array.isArray(update.tool_configs)).toBe(true);
        expect(update.tool_configs).toHaveLength(0);
        expect(update).not.toHaveProperty('enabled');
    });

    test('no-op save (prev == next) short-circuits — no PUT fired', () => {
        renderViewer();
        expect(capturedHandlers.length).toBeGreaterThanOrEqual(1);

        capturedHandlers[0].cb({
            name: 'Demo Plugin',
            enabled: true,
            baseURL: 'plugin://com.example.demo/mcp',
            headers: {},
            tool_configs: [{name: 'com_example_demo__echo', policy: 'ask', enabled: true}],
        });

        expect(mockUpdatePluginServer).not.toHaveBeenCalled();
    });
});
