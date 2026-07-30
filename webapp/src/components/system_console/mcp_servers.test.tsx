// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so FormattedMessage needs an id at runtime.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage?: string}) =>
            React.createElement(React.Fragment, null, defaultMessage ?? ''),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}) => defaultMessage ?? '',
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

import MCPServers, {MCPConfig, MCPServerConfig} from './mcp_servers';
/* eslint-enable import/first, import/order */

const mockUseIsBasicsLicensed = useIsBasicsLicensed as jest.Mock;
const mockGetMCPTools = getMCPTools as jest.Mock;

const STABLE_ID = 'abcdefghijklmnopqrstuvwxyz';

function makeMCPConfig(servers: MCPServerConfig[] = []): MCPConfig {
    return {
        enabled: true,
        enablePluginServer: false,
        servers,
        embeddedServer: {enabled: true, tool_configs: []},
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

    test('unlicensed: remote server UI is hidden and the enterprise chip is shown', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(false);

        renderServers(makeMCPConfig());

        expect(screen.queryByRole('button', {name: /Add Remote MCP Server/})).toBeNull();
        expect(screen.queryByText(/No remote MCP servers configured/)).toBeNull();
        expect(screen.queryByText('MCP OAuth Callback URL')).toBeNull();
        await waitFor(() => {
            expect(screen.getByText('Use remote MCP servers on qualifying Mattermost plans')).not.toBeNull();
        });
    });

    test('unlicensed with configured servers: server rows are hidden too', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(false);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        expect(screen.queryByText('Jira')).toBeNull();
        expect(screen.queryByRole('button', {name: /Add Remote MCP Server/})).toBeNull();
        await waitFor(() => {
            expect(screen.getByText('Use remote MCP servers on qualifying Mattermost plans')).not.toBeNull();
        });
    });

    test('licensed: remote server UI is shown and no license UI appears', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(true);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/});
        expect((addButton as HTMLButtonElement).disabled).toBe(false);
        expect(screen.getByText('Jira')).not.toBeNull();
        expect(screen.getByText('MCP OAuth Callback URL')).not.toBeNull();
        await waitFor(() => {
            expect(screen.queryByText('Use remote MCP servers on qualifying Mattermost plans')).toBeNull();
        });
    });
});
