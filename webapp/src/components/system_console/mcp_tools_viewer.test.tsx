// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim. ts-jest bypasses babel, so babel-plugin-formatjs
// never runs to inject ids from defaultMessage — FormattedMessage throws on
// render without an id. These stubs render defaultMessage verbatim (or the
// Intl method's input) so the component tree mounts, which is all these
// tests need. ICU token interpolation is preserved via values prop.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const interpolate = (msg: string, values?: Record<string, unknown>) => {
        if (!values) {
            return msg;
        }
        return msg.replace(/{(\w+)}/g, (_, k) => String(values[k] ?? ''));
    };

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage?: string; values?: Record<string, unknown>}) =>
            React.createElement(React.Fragment, null, interpolate(defaultMessage ?? '', values)),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}, values?: Record<string, unknown>) =>
                interpolate(defaultMessage ?? '', values),
        }),
    };
});

// Mock the client module so tests can assert on PUT bodies.
jest.mock('../../client', () => ({
    __esModule: true,
    getMCPTools: jest.fn(),
    clearMCPToolsCache: jest.fn(),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
    updatePluginServer: jest.fn().mockResolvedValue({}),
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {clearMCPToolsCache, getMCPTools, getVettedToolSeed, updatePluginServer} from '../../client';

import MCPToolsViewer, {MCPToolsResponse} from './mcp_tools_viewer';
import {MCPConfig} from './mcp_servers';
/* eslint-enable import/first, import/order */

const mockGetMCPTools = getMCPTools as jest.Mock;
const mockClearMCPToolsCache = clearMCPToolsCache as jest.Mock;
const mockGetVettedToolSeed = getVettedToolSeed as jest.Mock;
const mockUpdatePluginServer = updatePluginServer as jest.Mock;

function makeMCPConfig(overrides: Partial<MCPConfig> = {}): MCPConfig {
    return {
        enabled: true,
        enablePluginServer: true,
        servers: [],
        embeddedServer: {enabled: false, tool_configs: []},
        ...overrides,
    };
}

function makePluginToolsResponse(): MCPToolsResponse {
    return {
        servers: [
            {
                name: 'Demo Plugin',
                url: 'plugin://com.example.demo/mcp',
                serverType: 'plugin',
                enabled: true,
                tools: [
                    {name: 'com_example_demo__echo', description: 'Echo', inputSchema: null},
                    {name: 'com_example_demo__sum', description: 'Sum', inputSchema: null},
                ],
                needsOAuth: false,
                error: null,
                toolConfigs: [
                    {name: 'com_example_demo__echo', policy: 'ask', enabled: true},
                ],
            },
        ],
    };
}

function renderViewer(toolsData: MCPToolsResponse | null, mcpConfig: MCPConfig = makeMCPConfig()) {
    const onConfigChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <MCPToolsViewer
                    mcpConfig={mcpConfig}
                    onConfigChange={onConfigChange}
                    initialToolsData={toolsData}
                />
            </IntlProvider>,
        ),
        onConfigChange,
    };
}

beforeEach(() => {
    mockGetMCPTools.mockReset();
    mockGetMCPTools.mockResolvedValue({servers: []});
    mockClearMCPToolsCache.mockReset();
    mockClearMCPToolsCache.mockResolvedValue({message: 'cache cleared'});
    mockGetVettedToolSeed.mockReset();
    mockGetVettedToolSeed.mockResolvedValue([]);
    mockUpdatePluginServer.mockReset();
    mockUpdatePluginServer.mockResolvedValue({});
});

describe('MCPToolsViewer — plugin branch', () => {
    test('renders plugin row with toolConfigs from server response (policy dropdown enabled)', () => {
        renderViewer(makePluginToolsResponse());

        // Expand the server row so the tool list renders.
        fireEvent.click(screen.getByText('Demo Plugin'));

        // The policy dropdowns are rendered for the plugin row's tools.
        // None should be disabled.
        const selects = screen.getAllByRole('combobox');
        expect(selects.length).toBeGreaterThanOrEqual(1);
        for (const sel of selects) {
            expect((sel as HTMLSelectElement).disabled).toBe(false);
        }
    });

    test('changing a tool policy fires updatePluginServer with tool_configs only', async () => {
        renderViewer(makePluginToolsResponse());

        // Expand the row.
        fireEvent.click(screen.getByText('Demo Plugin'));

        // Find the first policy select and change it.
        const selects = screen.getAllByRole('combobox');
        fireEvent.change(selects[0], {target: {value: 'auto_run_in_dm'}});

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        const [pluginID, update] = mockUpdatePluginServer.mock.calls[0];
        expect(pluginID).toBe('com.example.demo');

        // Diff: only tool_configs changed; enabled stayed true. Body must
        // omit `enabled`.
        expect(update).not.toHaveProperty('enabled');
        expect(update).toHaveProperty('tool_configs');

        // The new tool_configs must contain the changed entry.
        const tcs = update.tool_configs as Array<{name: string; policy: string}>;
        const echoEntry = tcs.find((tc) => tc.name === 'com_example_demo__echo');
        expect(echoEntry?.policy).toBe('auto_run_in_dm');
    });

    test('toggling server-level enabled fires updatePluginServer with enabled only', async () => {
        renderViewer(makePluginToolsResponse());

        // ToggleSwitch renders as a native checkbox input. The row is
        // collapsed on mount — only the server-level toggle is in the DOM.
        const toggles = screen.getAllByRole('checkbox');
        expect(toggles.length).toBeGreaterThanOrEqual(1);
        fireEvent.click(toggles[0]);

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        const [pluginID, update] = mockUpdatePluginServer.mock.calls[0];
        expect(pluginID).toBe('com.example.demo');

        // Diff: only enabled changed. Body must omit `tool_configs`.
        expect(update).toHaveProperty('enabled');
        expect(update.enabled).toBe(false);
        expect(update).not.toHaveProperty('tool_configs');
    });

    test('plugin branch ignores rows without serverType==="plugin"', async () => {
        // Remote server, not a plugin — its mutations should NOT call updatePluginServer.
        const remoteResponse: MCPToolsResponse = {
            servers: [{
                name: 'Remote',
                url: 'https://remote.example/mcp',
                serverType: 'remote',
                tools: [{name: 'do_thing', description: '', inputSchema: null}],
                needsOAuth: false,
                error: null,
            }],
        };
        const cfg = makeMCPConfig({
            servers: [{
                name: 'Remote',
                enabled: true,
                baseURL: 'https://remote.example/mcp',
                headers: {},
                tool_configs: [],
            }],
        });

        const {onConfigChange} = renderViewer(remoteResponse, cfg);
        fireEvent.click(screen.getByText('Remote'));

        // Server-level toggle (native checkbox) is the first checkbox in the DOM.
        const toggles = screen.getAllByRole('checkbox');
        fireEvent.click(toggles[0]);

        // Remote rows route through onConfigChange (mcpConfig path), not the plugin endpoint.
        expect(onConfigChange).toHaveBeenCalled();
        expect(mockUpdatePluginServer).not.toHaveBeenCalled();
    });

    test('clear cache success refreshes tools and renders refreshed data', async () => {
        mockClearMCPToolsCache.mockResolvedValue({message: 'cleared'});
        mockGetMCPTools.mockResolvedValue({
            servers: [{
                name: 'Refreshed Remote',
                url: 'https://remote.example/mcp',
                serverType: 'remote',
                enabled: true,
                tools: [{name: 'refreshed_tool', description: '', inputSchema: null}],
                needsOAuth: false,
                error: null,
            }],
        });

        renderViewer({servers: []});

        fireEvent.click(screen.getByText('Clear Cache'));

        await waitFor(() => {
            expect(mockClearMCPToolsCache).toHaveBeenCalledTimes(1);
            expect(mockGetMCPTools).toHaveBeenCalledTimes(1);
        });

        expect(screen.getByText('Cache cleared successfully')).toBeTruthy();
        expect(screen.getByText('Refreshed Remote')).toBeTruthy();
    });

    test('getMCPTools failure renders error UI', async () => {
        mockGetMCPTools.mockRejectedValue(new Error('backend unavailable'));

        renderViewer(null);

        await waitFor(() => {
            expect(screen.getByText('Failed to load MCP tools')).toBeTruthy();
            expect(screen.getByText('backend unavailable')).toBeTruthy();
        });
    });

    test('vetted tool seed merges missing configs without replacing existing entries', async () => {
        mockGetVettedToolSeed.mockImplementation((baseURL: string) => {
            if (baseURL === 'https://remote.example/mcp') {
                return Promise.resolve([
                    {name: 'existing_tool', policy: 'auto_run_everywhere', enabled: true},
                    {name: 'seeded_tool', policy: 'auto_run_in_dm', enabled: true},
                ]);
            }
            if (baseURL === 'embedded://mattermost') {
                return Promise.resolve([
                    {name: 'embedded_seeded_tool', policy: 'ask', enabled: true},
                ]);
            }
            return Promise.resolve([]);
        });
        const cfg = makeMCPConfig({
            servers: [{
                name: 'Remote',
                enabled: true,
                baseURL: 'https://remote.example/mcp',
                headers: {},
                tool_configs: [{name: 'existing_tool', policy: 'ask', enabled: false}],
            }],
            embeddedServer: {
                enabled: true,
                tool_configs: [{name: 'embedded_existing_tool', policy: 'ask', enabled: true}],
            },
        });

        const {onConfigChange} = renderViewer({
            servers: [{
                name: 'Remote',
                url: 'https://remote.example/mcp',
                serverType: 'remote',
                enabled: true,
                tools: [{name: 'remote_tool', description: '', inputSchema: null}],
                needsOAuth: false,
                error: null,
            }],
        }, cfg);

        await waitFor(() => {
            expect(onConfigChange).toHaveBeenCalledTimes(1);
        });

        const [updated] = onConfigChange.mock.calls[0];
        expect(updated.servers[0].tool_configs).toEqual([
            {name: 'existing_tool', policy: 'ask', enabled: false},
            {name: 'seeded_tool', policy: 'auto_run_in_dm', enabled: true},
        ]);
        expect(updated.embeddedServer.tool_configs).toEqual([
            {name: 'embedded_existing_tool', policy: 'ask', enabled: true},
            {name: 'embedded_seeded_tool', policy: 'ask', enabled: true},
        ]);
    });
});

