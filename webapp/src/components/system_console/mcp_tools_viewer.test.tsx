// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so FormattedMessage needs an id at runtime.
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

jest.mock('../../client', () => ({
    __esModule: true,
    getMCPTools: jest.fn(),
    clearMCPToolsCache: jest.fn(),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
    updatePluginServer: jest.fn().mockResolvedValue({}),
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

// The MCP page reads SiteURL; null falls back to window.location.origin.
jest.mock('react-redux', () => ({
    __esModule: true,
    useSelector: jest.fn(() => null),
}));

jest.mock('../access_control/console_policy_section', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {clearMCPToolsCache, getMCPTools, getVettedToolSeed, updatePluginServer} from '../../client';

import {chooseOption} from '../../../tests/compass_select';

import {notifyMCPConnectionUpdated} from '../../hooks/use_mcp_connection_events';

import MCPServers from './mcp_servers';
import MCPToolsViewer from './mcp_tools_viewer';
import {useMCPToolsList} from './use_mcp_tools_list';
import {MCPConfig, MCPToolsResponse} from './mcp_types';
import {SettingsReadOnlyContext} from './settings_read_only';
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

// ViewerWithList renders the viewer with the tools list owned the way the
// MCP page owns it: initial is shown until the first load, which runs on
// mount when there is no initial list.
function ViewerWithList({initial, mcpConfig, onConfigChange}: {initial: MCPToolsResponse | null; mcpConfig: MCPConfig; onConfigChange: jest.Mock}) {
    const list = useMCPToolsList();
    const {refresh} = list;
    useEffect(() => {
        if (!initial) {
            refresh();
        }
    }, [initial, refresh]);
    return (
        <MCPToolsViewer
            mcpConfig={mcpConfig}
            onConfigChange={onConfigChange}
            toolsData={list.toolsData ?? initial}
            toolsError={list.error}
            onRefreshTools={refresh}
        />
    );
}

function renderViewer(toolsData: MCPToolsResponse | null, mcpConfig: MCPConfig = makeMCPConfig(), readOnly = false) {
    const onConfigChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <SettingsReadOnlyContext.Provider value={readOnly}>
                    <ViewerWithList
                        initial={toolsData}
                        mcpConfig={mcpConfig}
                        onConfigChange={onConfigChange}
                    />
                </SettingsReadOnlyContext.Provider>
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

        fireEvent.click(screen.getByText('Demo Plugin'));

        const selects = screen.getAllByRole('combobox');
        expect(selects.length).toBeGreaterThanOrEqual(1);
        for (const sel of selects) {
            expect((sel as HTMLButtonElement).disabled).toBe(false);
        }
    });

    test('changing a tool policy fires updatePluginServer with tool_configs only', async () => {
        renderViewer(makePluginToolsResponse());

        fireEvent.click(screen.getByText('Demo Plugin'));

        const selects = screen.getAllByRole('combobox');
        chooseOption(selects[0], 'Auto Run (DM)');

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        const [pluginID, update] = mockUpdatePluginServer.mock.calls[0];
        expect(pluginID).toBe('com.example.demo');

        expect(update).not.toHaveProperty('enabled');
        expect(update).toHaveProperty('tool_configs');

        const tcs = update.tool_configs as Array<{name: string; policy: string}>;
        const echoEntry = tcs.find((tc) => tc.name === 'com_example_demo__echo');
        expect(echoEntry?.policy).toBe('auto_run_in_dm');
    });

    test('toggling server-level enabled fires updatePluginServer with enabled only', async () => {
        renderViewer(makePluginToolsResponse());

        // ToggleSwitch renders as role="switch"; row collapsed on mount, so only server toggle is in the DOM.
        const toggles = screen.getAllByRole('switch');
        expect(toggles.length).toBeGreaterThanOrEqual(1);
        fireEvent.click(toggles[0]);

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        const [pluginID, update] = mockUpdatePluginServer.mock.calls[0];
        expect(pluginID).toBe('com.example.demo');

        expect(update).toHaveProperty('enabled');
        expect(update.enabled).toBe(false);
        expect(update).not.toHaveProperty('tool_configs');
    });

    test('plugin branch ignores rows without serverType==="plugin"', async () => {
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

        const toggles = screen.getAllByRole('switch');
        fireEvent.click(toggles[0]);

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

    test('fetchTools rejection after successful update surfaces inline error', async () => {
        // The PUT succeeds but the post-update reload fails: the UI must show
        // the inline error rather than silently going stale.
        mockUpdatePluginServer.mockResolvedValueOnce({});
        mockGetMCPTools.mockRejectedValueOnce(new Error('refresh exploded'));

        renderViewer(makePluginToolsResponse());

        // Toggle the server-level enabled switch on the plugin row.
        const toggles = screen.getAllByRole('switch');
        fireEvent.click(toggles[0]);

        await waitFor(() => {
            expect(mockUpdatePluginServer).toHaveBeenCalledTimes(1);
        });

        await waitFor(() => {
            expect(screen.getByText('refresh exploded')).toBeTruthy();
        });
    });

    test('a reload failing with a non-Error reason still shows the load failure', async () => {
        mockUpdatePluginServer.mockResolvedValueOnce({});
        // eslint-disable-next-line prefer-promise-reject-errors
        mockGetMCPTools.mockRejectedValueOnce('boom');

        renderViewer(makePluginToolsResponse());

        const toggles = screen.getAllByRole('switch');
        fireEvent.click(toggles[0]);

        await waitFor(() => {
            expect(screen.getByText('Failed to load MCP tools')).toBeTruthy();
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

        // The seeds land on the configuration as it is by then, keeping edits
        // made while they loaded.
        const [update] = onConfigChange.mock.calls[0];
        const editedMeanwhile: MCPConfig = {
            ...cfg,
            enablePluginServer: false,
            servers: [...(cfg.servers ?? []), {name: 'Added', enabled: true, baseURL: 'https://added.example/mcp', headers: {}}],
        };
        const updated: MCPConfig = update(editedMeanwhile);
        expect(updated.enablePluginServer).toBe(false);
        expect(updated.servers?.[1]).toEqual(editedMeanwhile.servers?.[1]);
        expect(updated.servers?.[0].tool_configs).toEqual([
            {name: 'existing_tool', policy: 'ask', enabled: false},
            {name: 'seeded_tool', policy: 'auto_run_in_dm', enabled: true},
        ]);
        expect(updated.embeddedServer.tool_configs).toEqual([
            {name: 'embedded_existing_tool', policy: 'ask', enabled: true},
            {name: 'embedded_seeded_tool', policy: 'ask', enabled: true},
        ]);
    });
});

describe('MCPToolsViewer — tools list owned by the MCP page', () => {
    const remote = {id: 'server-id', name: 'Remote', enabled: true, baseURL: 'https://mcp.example.com', headers: {}};
    const toolsList = (needsOAuth: boolean): MCPToolsResponse => ({
        servers: [{
            name: 'Remote',
            url: 'https://mcp.example.com',
            serverType: 'remote',
            enabled: true,
            id: 'server-id',
            tools: needsOAuth ? [] : [{name: 'search', description: 'Search', inputSchema: null, defaultPolicy: 'ask'}],
            needsOAuth,
            oauthURL: needsOAuth ? 'https://auth.example.com' : undefined, // eslint-disable-line no-undefined
            error: null,
        }],
    });

    const renderPage = () => {
        render(
            <IntlProvider locale='en'>
                <MCPServers
                    mcpConfig={makeMCPConfig({servers: [remote]})}
                    savedServers={[remote]}
                    onChange={jest.fn()}
                    onPersistServer={jest.fn()}
                    onDeleteServer={jest.fn()}
                />
            </IntlProvider>,
        );
        fireEvent.click(screen.getByRole('tab', {name: 'Tools'}));
    };

    test('a slower load the page started earlier does not replace a newer refresh', async () => {
        let finishPageLoad: (value: MCPToolsResponse) => void = () => null;
        mockGetMCPTools.mockReturnValueOnce(new Promise((resolve) => {
            finishPageLoad = resolve;
        }));
        mockGetMCPTools.mockResolvedValueOnce(toolsList(false));
        renderPage();

        fireEvent.click(screen.getByRole('button', {name: 'Refresh Tools'}));
        expect(await screen.findByText('1/1 tools enabled')).not.toBeNull();

        await act(async () => {
            finishPageLoad(toolsList(true));
        });
        expect(screen.getByText('1/1 tools enabled')).not.toBeNull();
        expect(screen.queryByText('Needs OAuth')).toBeNull();
    });

    test('a failed action notice clears when the admin reloads the tools', async () => {
        mockGetMCPTools.mockResolvedValue(toolsList(false));
        mockClearMCPToolsCache.mockRejectedValueOnce(new Error('cache unavailable'));
        renderPage();
        expect(await screen.findByText('1/1 tools enabled')).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Clear Cache'}));
        expect(await screen.findByText('MCP tools action failed')).not.toBeNull();
        expect(screen.getByText('cache unavailable')).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Refresh Tools'}));
        await waitFor(() => expect(screen.queryByText('MCP tools action failed')).toBeNull());
        expect(screen.getByText('1/1 tools enabled')).not.toBeNull();
    });

    test('a failed load notice clears once a later load succeeds', async () => {
        mockGetMCPTools.mockRejectedValueOnce(new Error('backend unavailable'));
        mockGetMCPTools.mockResolvedValueOnce(toolsList(false));
        renderPage();
        expect(await screen.findByText('backend unavailable')).not.toBeNull();

        await act(async () => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Remote'});
        });

        expect(await screen.findByText('1/1 tools enabled')).not.toBeNull();
        expect(screen.queryByText('Failed to load MCP tools')).toBeNull();
    });
});

test('lists servers that share a URL, as read-only viewers get them, as separate rows', () => {
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => null);
    try {
        renderViewer({
            servers: ['Tracker', 'Wiki'].map((name) => ({
                name,
                url: 'https://mcp.example.com',
                serverType: 'remote',
                enabled: true,
                tools: [],
                needsOAuth: false,
                error: null,
            })),
        }, makeMCPConfig(), true);

        expect(screen.getByText('Tracker')).toBeTruthy();
        expect(screen.getByText('Wiki')).toBeTruthy();
        expect(consoleError.mock.calls.flat().join(' ')).not.toMatch(/same key/);
    } finally {
        consoleError.mockRestore();
    }
});

test('maps each server on a shared URL to its own configuration by name', () => {
    const shared = 'https://mcp.example.com';
    const servers = ['Tracker', 'Wiki'].map((name) => ({
        name,
        url: shared,
        serverType: 'remote',
        enabled: true,
        tools: [],
        needsOAuth: false,
        error: null,
    }));
    const mcpConfig = makeMCPConfig({
        servers: [
            {name: 'Tracker', enabled: true, baseURL: shared, headers: {}},
            {name: 'Wiki', enabled: false, baseURL: shared, headers: {}},
        ],
    });
    const {onConfigChange} = renderViewer({servers}, mcpConfig);

    const toggles = screen.getAllByRole('switch');
    expect(toggles[0].getAttribute('aria-checked')).toBe('true');
    expect(toggles[1].getAttribute('aria-checked')).toBe('false');

    fireEvent.click(toggles[1]);

    const [updated] = onConfigChange.mock.calls[0];
    expect(updated.servers).toEqual([
        {name: 'Tracker', enabled: true, baseURL: shared, headers: {}},
        {name: 'Wiki', enabled: true, baseURL: shared, headers: {}},
    ]);
});

test('maps servers to their configuration by ID when names were swapped by renames', () => {
    const servers = [
        {id: 'id-a', name: 'A', url: 'https://a.example.com', serverType: 'remote', enabled: true, tools: [], needsOAuth: false, error: null},
        {id: 'id-x', name: 'X', url: 'https://x.example.com', serverType: 'remote', enabled: true, tools: [], needsOAuth: false, error: null},
    ];

    // The tools payload still carries the names from before A->B and X->A.
    const mcpConfig = makeMCPConfig({
        servers: [
            {id: 'id-a', name: 'B', enabled: false, baseURL: 'https://a.example.com', headers: {}},
            {id: 'id-x', name: 'A', enabled: true, baseURL: 'https://x.example.com', headers: {}},
        ],
    });
    const {onConfigChange} = renderViewer({servers}, mcpConfig);

    const toggles = screen.getAllByRole('switch');
    expect(toggles[0].getAttribute('aria-checked')).toBe('false');
    expect(toggles[1].getAttribute('aria-checked')).toBe('true');

    fireEvent.click(toggles[0]);

    const [updated] = onConfigChange.mock.calls[0];
    expect(updated.servers).toEqual([
        {id: 'id-a', name: 'B', enabled: true, baseURL: 'https://a.example.com', headers: {}},
        {id: 'id-x', name: 'A', enabled: true, baseURL: 'https://x.example.com', headers: {}},
    ]);
});

test('read-only viewers get the embedded server seeded but no policy applied to a remote server', async () => {
    mockGetVettedToolSeed.mockImplementation((baseURL: string) => Promise.resolve(
        baseURL === 'embedded://mattermost' ?
            [{name: 'embedded_seeded_tool', policy: 'ask', enabled: true}] :
            [{name: 'seeded_tool', policy: 'auto_run_in_dm', enabled: true}],
    ));
    const cfg = makeMCPConfig({
        servers: [{name: 'Remote', enabled: true, baseURL: 'https://remote.example', headers: {}, tool_configs: []}],
        embeddedServer: {enabled: true, tool_configs: []},
    });

    const {onConfigChange} = renderViewer({
        servers: [{
            name: 'Remote',
            url: 'https://remote.example',
            serverType: 'remote',
            enabled: true,
            tools: [],
            needsOAuth: false,
            error: null,
        }],
    }, cfg, true);

    await waitFor(() => expect(onConfigChange).toHaveBeenCalled());
    const updated: MCPConfig = onConfigChange.mock.calls[0][0](cfg);
    expect(updated.servers?.[0].tool_configs).toEqual([]);
    expect(updated.embeddedServer.tool_configs).toEqual([{name: 'embedded_seeded_tool', policy: 'ask', enabled: true}]);
    expect(mockGetVettedToolSeed).not.toHaveBeenCalledWith('https://remote.example');
});
