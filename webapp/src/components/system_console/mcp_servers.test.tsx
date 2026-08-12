// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so FormattedMessage needs an id at runtime.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const formatMessage = (
        {defaultMessage}: {defaultMessage?: string},
        values?: Record<string, unknown>,
    ) => {
        let message = defaultMessage ?? '';
        if (values) {
            for (const [key, value] of Object.entries(values)) {
                message = message.replace(new RegExp(`\\{${key}\\}`, 'g'), String(value));
            }
        }
        return message;
    };

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage?: string; values?: Record<string, unknown>}) =>
            React.createElement(React.Fragment, null, formatMessage({defaultMessage}, values)),
        useIntl: () => ({
            formatMessage,
        }),
    };
});

// OverlayTrigger renders the overlay alongside children so tests can assert the tooltip text.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

// The component reads SiteURL via useSelector; null falls back to window.location.origin.
jest.mock('react-redux', () => ({
    __esModule: true,
    useSelector: jest.fn(() => null),
}));

jest.mock('@/license', () => ({
    useIsBasicsLicensed: jest.fn(),
}));

jest.mock('../../client', () => ({
    __esModule: true,
    getMCPTools: jest.fn().mockResolvedValue({servers: []}),
    clearMCPToolsCache: jest.fn(),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
    updatePluginServer: jest.fn().mockResolvedValue({}),
}));

jest.mock('./mcp_tools_viewer', () => ({
    __esModule: true,
    default: () => null,
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {useIsBasicsLicensed} from '@/license';

import {getMCPTools} from '../../client';

import MCPServers from './mcp_servers';
import {MCPConfig, MCPServerConfig, PluginServerConfig} from './mcp_types';
/* eslint-enable import/first, import/order */

const mockUseIsBasicsLicensed = useIsBasicsLicensed as jest.Mock;
const mockGetMCPTools = getMCPTools as jest.Mock;

const STABLE_ID = 'abcdefghijklmnopqrstuvwxyz';

function makeMCPConfig(servers: MCPServerConfig[] = [], embeddedId?: string): MCPConfig {
    return {
        enabled: true,
        enablePluginServer: false,
        servers,
        embeddedServer: {
            ...(embeddedId ? {id: embeddedId} : {}),
            enabled: true,
            tool_configs: [],
        },
    };
}

function makeRemoteServer(): MCPServerConfig {
    return {
        name: 'Jira',
        enabled: true,
        baseURL: 'https://mcp.example.com',
        headers: {},
    };
}

function renderServers(mcpConfig: MCPConfig) {
    const onChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <MCPServers
                    mcpConfig={mcpConfig}
                    onChange={onChange}
                />
            </IntlProvider>,
        ),
        onChange,
    };
}

function lastChangedServers(onChange: jest.Mock): MCPServerConfig[] {
    expect(onChange).toHaveBeenCalled();
    const config: MCPConfig = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    return config.servers ?? [];
}

const existingServer: MCPServerConfig = {
    id: STABLE_ID,
    name: 'Jira',
    enabled: true,
    baseURL: 'https://jira.example.com',
    headers: {},
};

describe('MCPServers stable ID handling', () => {
    beforeEach(() => {
        jest.clearAllMocks();

        // The remote-server UI these tests drive is behind the license gate.
        mockUseIsBasicsLicensed.mockReturnValue(true);

        // Never resolves: these assertions are synchronous, so a resolving
        // prefetch would update state outside act().
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    it('preserves the server id through a name edit', () => {
        const {onChange} = renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByText('Jira'));
        const nameInput = screen.getByPlaceholderText('Server name');
        fireEvent.change(nameInput, {target: {value: 'Jira Cloud'}});
        fireEvent.blur(nameInput);

        const servers = lastChangedServers(onChange);
        expect(servers[0].name).toBe('Jira Cloud');
        expect(servers[0].id).toBe(STABLE_ID);
    });

    it('preserves the server id through a URL edit', () => {
        const {onChange} = renderServers(makeMCPConfig([existingServer]));

        const urlInput = screen.getByPlaceholderText('https://mcp.example.com');
        fireEvent.change(urlInput, {target: {value: 'https://jira2.example.com'}});

        const servers = lastChangedServers(onChange);
        expect(servers[0].baseURL).toBe('https://jira2.example.com');
        expect(servers[0].id).toBe(STABLE_ID);
    });

    it('adds a new server without an id so the backend mints the stable ID on save', () => {
        const {onChange} = renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByText('Add Remote MCP Server'));

        const servers = lastChangedServers(onChange);
        expect(servers).toHaveLength(2);
        expect(servers[1].id).toBeUndefined();
        expect(servers[0].id).toBe(STABLE_ID);
    });
});

describe('MCPServers license gating', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetMCPTools.mockResolvedValue({servers: []});
    });

    test('built-in section is read-only: no delete or URL inputs', async () => {
        mockGetMCPTools.mockResolvedValue({
            servers: [{
                name: 'Demo Plugin',
                url: 'plugin://com.mattermost.demo/mcp',
                tools: [],
                needsOAuth: false,
                error: null,
                serverType: 'plugin',
                enabled: true,
                id: 'abcdefghijklmnopqrstuvwxpl',
            }],
        });

        // Unlicensed so remote editable cards are hidden.
        mockUseIsBasicsLicensed.mockReturnValue(false);
        renderServers(makeMCPConfig());

        await waitFor(() => {
            expect(screen.getByText('Demo Plugin')).not.toBeNull();
        });

        const section = screen.getByTestId('built-in-plugin-servers-section');
        expect(section.querySelector('input')).toBeNull();
        expect(screen.queryByText('Delete Server')).toBeNull();
        expect(screen.queryByPlaceholderText('https://mcp.example.com')).toBeNull();
    });

    test('preserves embeddedServer.id through enablePluginServer toggle', () => {
        const {onChange} = renderServers(makeMCPConfig([], STABLE_ID));

        // BooleanItem exposes true/false radios under the enablePluginServer row.
        const trueRadios = screen.getAllByDisplayValue('true');
        fireEvent.click(trueRadios[0]);

        expect(onChange).toHaveBeenCalled();
        const config: MCPConfig = onChange.mock.calls[onChange.mock.calls.length - 1][0];
        expect(config.embeddedServer.id).toBe(STABLE_ID);
        expect(config.embeddedServer.enabled).toBe(true);
    });

    test('preserves plugin_servers through enablePluginServer toggle', () => {
        const pluginServers: PluginServerConfig[] = [{
            id: 'pluginstableidabcdefghijklm',
            plugin_id: 'com.example.demo',
            name: 'Demo Plugin',
            path: '/mcp',
            enabled: true,
            expose_external: false,
            tool_configs: [{name: 'echo', policy: 'ask', enabled: true}],
        }];
        const {onChange} = renderServers({
            ...makeMCPConfig([], STABLE_ID),
            plugin_servers: pluginServers,
        });

        const trueRadios = screen.getAllByDisplayValue('true');
        fireEvent.click(trueRadios[0]);

        expect(onChange).toHaveBeenCalled();
        const config: MCPConfig = onChange.mock.calls[onChange.mock.calls.length - 1][0];
        expect(config.plugin_servers).toEqual(pluginServers);
        expect(config.enablePluginServer).toBe(true);
    });
});
