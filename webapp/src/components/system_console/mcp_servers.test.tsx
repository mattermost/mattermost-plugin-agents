// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor} from '@testing-library/react';

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

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {useIsBasicsLicensed} from '@/license';

import MCPServers, {MCPConfig, MCPServerConfig} from './mcp_servers';
/* eslint-enable import/first, import/order */

const mockUseIsBasicsLicensed = useIsBasicsLicensed as jest.Mock;

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

describe('MCPServers license gating', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('unlicensed: Add Remote MCP Server is disabled and shows the enterprise chip', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(false);

        renderServers(makeMCPConfig());

        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/});
        expect((addButton as HTMLButtonElement).disabled).toBe(true);
        await waitFor(() => {
            expect(screen.getByText('Use remote MCP servers on qualifying Mattermost plans')).not.toBeNull();
        });
    });

    test('unlicensed with configured servers: shows the license notice', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(false);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        await waitFor(() => {
            expect(screen.getByText(/Remote MCP servers require a qualifying Mattermost plan\. Agents cannot run tools/)).not.toBeNull();
        });
    });

    test('unlicensed without configured servers: no license notice, only the chip', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(false);

        renderServers(makeMCPConfig());

        await waitFor(() => {
            expect(screen.queryByText(/Agents cannot run tools/)).toBeNull();
        });
    });

    test('licensed: Add Remote MCP Server is enabled and no license UI is shown', async () => {
        mockUseIsBasicsLicensed.mockReturnValue(true);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/});
        expect((addButton as HTMLButtonElement).disabled).toBe(false);
        await waitFor(() => {
            expect(screen.queryByText('Use remote MCP servers on qualifying Mattermost plans')).toBeNull();
        });
        expect(screen.queryByText(/Agents cannot run tools/)).toBeNull();
    });
});
