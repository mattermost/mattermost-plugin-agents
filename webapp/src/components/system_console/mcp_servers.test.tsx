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

function makeRemoteServer(overrides: Partial<MCPServerConfig> = {}): MCPServerConfig {
    return {
        name: 'Jira',
        enabled: true,
        baseURL: 'https://mcp.example.com',
        headers: {},
        ...overrides,
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

describe('MCPServers service account headers', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsBasicsLicensed.mockReturnValue(true);
    });

    // Rows render in DOM order: base Headers first, then Service Account headers.
    const baseHeaderValueInput = () => screen.getAllByPlaceholderText('Value')[0];
    const serviceAccountHeaderValueInput = () => screen.getAllByPlaceholderText('Value')[1];

    async function renderOneServer(server: MCPServerConfig) {
        const rendered = renderServers(makeMCPConfig([server]));
        await screen.findByText('Service Account Authentication');
        return rendered;
    }

    function savedServer(onChange: jest.Mock): MCPServerConfig {
        expect(onChange).toHaveBeenCalledTimes(1);
        const saved = (onChange.mock.calls[0][0] as MCPConfig).servers;
        expect(saved).not.toBeNull();
        return saved![0];
    }

    test('renders the Service Account Authentication section with its own header editor', async () => {
        await renderOneServer(makeRemoteServer());

        expect(screen.getByText('Service Account Authentication')).not.toBeNull();
        expect(screen.getAllByRole('button', {name: 'Add Header'})).toHaveLength(2);
    });

    test('adding a service account header emits serviceAccountHeaders and leaves headers untouched', async () => {
        const {onChange} = await renderOneServer(makeRemoteServer({headers: {'X-Base': 'b'}}));

        fireEvent.click(screen.getAllByRole('button', {name: 'Add Header'})[1]);

        expect(savedServer(onChange)).toEqual(expect.objectContaining({
            headers: {'X-Base': 'b'},
            serviceAccountHeaders: {'': ''},
        }));
    });

    // Regression: the field-by-field config rebuild used to drop serviceAccountHeaders.
    test('editing an unrelated field preserves serviceAccountHeaders', async () => {
        const {onChange} = await renderOneServer(makeRemoteServer({
            serviceAccountHeaders: {Authorization: 'Bearer pat'},
        }));

        fireEvent.change(screen.getByPlaceholderText('https://mcp.example.com'), {
            target: {value: 'https://mcp.example.com/v2'},
        });

        expect(savedServer(onChange)).toEqual(expect.objectContaining({
            baseURL: 'https://mcp.example.com/v2',
            serviceAccountHeaders: {Authorization: 'Bearer pat'},
        }));
    });

    test('editing one header map leaves the other map untouched', async () => {
        const server = makeRemoteServer({
            headers: {'X-Base': 'base-value'},
            serviceAccountHeaders: {Authorization: 'Bearer pat'},
        });

        const base = await renderOneServer(server);
        fireEvent.change(baseHeaderValueInput(), {target: {value: 'base-value-2'}});
        expect(savedServer(base.onChange)).toEqual(expect.objectContaining({
            headers: {'X-Base': 'base-value-2'},
            serviceAccountHeaders: {Authorization: 'Bearer pat'},
        }));
        base.unmount();

        const serviceAccount = await renderOneServer(server);
        fireEvent.change(serviceAccountHeaderValueInput(), {target: {value: 'Bearer rotated'}});
        expect(savedServer(serviceAccount.onChange)).toEqual(expect.objectContaining({
            headers: {'X-Base': 'base-value'},
            serviceAccountHeaders: {Authorization: 'Bearer rotated'},
        }));
    });
});
