// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so FormattedMessage needs an id at runtime.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const formatMessage = (
        {defaultMessage}: {defaultMessage?: string},
        values?: Record<string, unknown>,
    ) => {
        let message = defaultMessage ?? '';
        if (values) {
            // Simple ICU plurals: {count, plural, one {...} other {...}}.
            message = message.replace(/\{(\w+), plural, one \{([^}]*)\} other \{([^}]*)\}\}/g, (_, key, one, other) => (
                (values[key] === 1 ? one : other).replace(/#/g, String(values[key]))
            ));
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

// WithTooltip renders its title alongside children so tests can assert the tooltip text.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({
        children,
        title,
        hint,
    }: {
        children: React.ReactNode;
        title?: React.ReactNode;
        hint?: React.ReactNode;
    }) => (
        <>
            {children}
            <div>{title}</div>
            <div>{hint}</div>
        </>
    ),
}));

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
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

jest.mock('../../client', () => ({
    __esModule: true,
    getMCPTools: jest.fn().mockResolvedValue({servers: []}),
    clearMCPToolsCache: jest.fn(),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
    updatePluginServer: jest.fn().mockResolvedValue({}),
    getMCPSuggestedDescription: jest.fn(),
    getMCPServerTools: jest.fn(),
    getPluginConfig: jest.fn(),
}));

jest.mock('./mcp_tools_viewer', () => ({
    __esModule: true,
    default: () => <div data-testid='mcp-tools-viewer'/>,
}));

jest.mock('../access_control/console_policy_section', () => ({
    __esModule: true,
    default: ({resourceId}: {resourceId: string}) => (
        <div data-testid='console-policy-section'>{resourceId}</div>
    ),
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {useIsLicensedFor} from '@/license';

import {getMCPTools, getPluginConfig} from '../../client';

import MCPServers, {type MCPConfig, type MCPServerConfig} from './mcp_servers';
import type {PluginServerConfig} from './mcp_types';
/* eslint-enable import/first, import/order */

const mockUseIsLicensedFor = useIsLicensedFor as jest.Mock;
const mockGetMCPTools = getMCPTools as jest.Mock;
const mockGetPluginConfig = getPluginConfig as jest.Mock;

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

function makeRemoteServer(overrides: Partial<MCPServerConfig> = {}): MCPServerConfig {
    return {
        name: 'Jira',
        enabled: true,
        baseURL: 'https://mcp.example.com',
        headers: {},
        ...overrides,
    };
}

function renderServers(mcpConfig: MCPConfig, savedServers?: MCPServerConfig[]) {
    const onChange = jest.fn();
    const onDeleteServer = jest.fn(async () => undefined); // eslint-disable-line no-undefined
    return {
        ...render(
            <IntlProvider locale='en'>
                <MCPServers
                    onPersistServer={jest.fn()}
                    onDeleteServer={onDeleteServer}
                    mcpConfig={mcpConfig}
                    savedServers={savedServers}
                    onChange={onChange}
                />
            </IntlProvider>,
        ),
        onChange,
        onDeleteServer,
    };
}

const existingServer: MCPServerConfig = {
    id: STABLE_ID,
    name: 'Jira',
    enabled: true,
    baseURL: 'https://jira.example.com',
    headers: {},
};

describe('MCPServers remote server list', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);

        // Never resolves unless a test sets tools: a resolving prefetch would
        // update state outside act().
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    test('lists each server compactly instead of an inline editor', () => {
        renderServers(makeMCPConfig([existingServer, makeRemoteServer({id: 'zyxwvutsrqponmlkjihgfedcba', name: 'Docs', enabled: false})]));

        const list = screen.getByRole('list', {name: 'Remote MCP servers'});
        expect(within(list).getByText('Jira')).not.toBeNull();
        expect(within(list).getByText('https://jira.example.com')).not.toBeNull();
        expect(within(list).getAllByText('Disabled')).toHaveLength(1);
        expect(screen.queryByPlaceholderText('https://mcp.example.com')).toBeNull();
        expect(screen.queryByText('Service Account Authentication')).toBeNull();
    });

    test('counts the tools of a server that have no chosen policy', async () => {
        mockGetMCPTools.mockResolvedValue({servers: [{
            name: 'Jira',
            url: 'https://jira.example.com',
            serverType: 'remote',
            enabled: true,
            id: STABLE_ID,
            needsOAuth: false,
            error: null,
            tools: [
                {name: 'search', description: '', inputSchema: null},
                {name: 'create', description: '', inputSchema: null},
                {name: 'delete', description: '', inputSchema: null},
            ],
        }]});
        renderServers(makeMCPConfig([{...existingServer,
            tool_configs: [
                {name: 'search', policy: 'ask', enabled: true},
                {name: 'create', policy: '', enabled: false},
            ]}]));

        expect(await screen.findByText('2 tools not reviewed')).not.toBeNull();
    });

    test('opens a server in the dialog from its row, by keyboard too', async () => {
        mockGetPluginConfig.mockResolvedValue({mcp: {servers: [existingServer]}});
        renderServers(makeMCPConfig([existingServer]));

        const row = screen.getByRole('button', {name: 'Edit Jira'});
        expect(row.getAttribute('aria-describedby')).toBeTruthy();
        const description = (row.getAttribute('aria-describedby') ?? '').split(' ').map((id) => document.getElementById(id)?.textContent ?? '').join(' ');
        expect(description).toContain('https://jira.example.com');
        row.focus();
        expect(document.activeElement).toBe(row);
        fireEvent.click(row);

        expect(screen.getByRole('dialog', {name: 'Edit Jira'})).not.toBeNull();
        expect(await screen.findByDisplayValue('https://jira.example.com')).not.toBeNull();
    });

    test('deletes a saved server after confirming', async () => {
        const {onDeleteServer, onChange} = renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByRole('button', {name: 'Delete Jira'}));
        expect(onDeleteServer).not.toHaveBeenCalled();
        expect(await screen.findByText(/users’ saved sign-ins to it are removed/)).not.toBeNull();
        fireEvent.click(await screen.findByRole('button', {name: 'Delete'}));

        await waitFor(() => expect(onDeleteServer).toHaveBeenCalledWith(STABLE_ID));
        expect(onChange).not.toHaveBeenCalled();
    });

    test('adds a server through the dialog instead of an unsaved blank entry', () => {
        const {onChange} = renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByText('Add Remote MCP Server'));

        expect(screen.getByRole('dialog', {name: 'Add remote MCP server'})).not.toBeNull();
        expect(onChange).not.toHaveBeenCalled();
    });
});

describe('MCPServers tabs', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    test('switches between the configuration and tools views', () => {
        renderServers(makeMCPConfig([existingServer]));

        const configTab = screen.getByRole('tab', {name: 'Configuration'});
        const toolsTab = screen.getByRole('tab', {name: 'Tools'});
        expect(configTab.getAttribute('aria-selected')).toBe('true');
        expect(screen.getByText('Jira')).not.toBeNull();
        expect(screen.queryByTestId('mcp-tools-viewer')).toBeNull();

        fireEvent.click(toolsTab);

        expect(toolsTab.getAttribute('aria-selected')).toBe('true');
        expect(screen.getByTestId('mcp-tools-viewer')).not.toBeNull();
        expect(screen.queryByText('Jira')).toBeNull();

        fireEvent.click(configTab);

        expect(screen.getByText('Jira')).not.toBeNull();
        expect(screen.queryByTestId('mcp-tools-viewer')).toBeNull();
    });
});

describe('MCPServers license gating', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetMCPTools.mockResolvedValue({servers: []});
    });

    test('unlicensed: existing empty list stays visible and add is disabled with a chip', async () => {
        mockUseIsLicensedFor.mockReturnValue(false);

        renderServers(makeMCPConfig());

        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/}) as HTMLButtonElement;
        expect(addButton.disabled).toBe(true);
        expect(screen.getByText(/No remote MCP servers configured/)).not.toBeNull();
        expect(screen.queryByText('MCP OAuth Callback URL')).toBeNull();
        await waitFor(() => {
            expect(screen.getAllByText('Available on Enterprise plans and above').length).toBeGreaterThan(0);
        });
    });

    test('unlicensed with configured servers: server rows stay visible and add is disabled', async () => {
        mockUseIsLicensedFor.mockReturnValue(false);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        expect(screen.getByText('Jira')).not.toBeNull();
        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/}) as HTMLButtonElement;
        expect(addButton.disabled).toBe(true);
        expect(screen.getByText('MCP OAuth Callback URL')).not.toBeNull();
        await waitFor(() => {
            expect(screen.getAllByText('Available on Enterprise plans and above').length).toBeGreaterThan(0);
        });
    });

    test('licensed: remote server UI is shown and no license UI appears', async () => {
        mockUseIsLicensedFor.mockReturnValue(true);

        renderServers(makeMCPConfig([makeRemoteServer()]));

        const addButton = screen.getByRole('button', {name: /Add Remote MCP Server/});
        expect((addButton as HTMLButtonElement).disabled).toBe(false);
        expect(screen.getByText('Jira')).not.toBeNull();
        expect(screen.getByText('MCP OAuth Callback URL')).not.toBeNull();
        await waitFor(() => {
            expect(screen.queryByText('Available on Enterprise plans and above')).toBeNull();
        });
    });
});

describe('MCPServers built-in & plugin section', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
        mockGetMCPTools.mockResolvedValue({servers: []});
    });

    test('always renders the embedded Mattermost card', async () => {
        renderServers(makeMCPConfig());

        expect(screen.getByTestId('built-in-plugin-servers-section')).not.toBeNull();
        expect(screen.getByText('Built-in & plugin servers')).not.toBeNull();
        expect(screen.getByText('Mattermost')).not.toBeNull();
        expect(screen.getByText('Built-in')).not.toBeNull();
        await waitFor(() => {
            expect(mockGetMCPTools).toHaveBeenCalled();
        });
    });

    test('renders ConsolePolicySection for embedded only when id is present', () => {
        const {rerender} = render(
            <IntlProvider locale='en'>
                <MCPServers
                    onPersistServer={jest.fn()}
                    onDeleteServer={jest.fn()}
                    mcpConfig={makeMCPConfig()}
                    onChange={jest.fn()}
                />
            </IntlProvider>,
        );

        const section = screen.getByTestId('built-in-plugin-servers-section');
        expect(section.querySelector('[data-testid="console-policy-section"]')).toBeNull();

        rerender(
            <IntlProvider locale='en'>
                <MCPServers
                    onPersistServer={jest.fn()}
                    onDeleteServer={jest.fn()}
                    mcpConfig={makeMCPConfig([], STABLE_ID)}
                    onChange={jest.fn()}
                />
            </IntlProvider>,
        );

        expect(screen.getByTestId('console-policy-section').textContent).toBe(STABLE_ID);
    });

    test('renders plugin cards from getMCPTools with policy sections when ids are present', async () => {
        const pluginID = 'abcdefghijklmnopqrstuvwxpl';
        mockGetMCPTools.mockResolvedValue({
            servers: [
                {
                    name: 'Demo Plugin',
                    url: 'plugin://com.mattermost.demo/mcp',
                    tools: [],
                    needsOAuth: false,
                    error: null,
                    serverType: 'plugin',
                    enabled: true,
                    id: pluginID,
                },
                {
                    name: 'Remote',
                    url: 'https://mcp.example.com',
                    tools: [],
                    needsOAuth: false,
                    error: null,
                    serverType: 'remote',
                    enabled: true,
                    id: 'abcdefghijklmnopqrstuvwxrm',
                },
            ],
        });

        renderServers(makeMCPConfig([], STABLE_ID));

        await waitFor(() => {
            expect(screen.getByText('Demo Plugin')).not.toBeNull();
        });
        expect(screen.getByText('Plugin ID: com.mattermost.demo')).not.toBeNull();
        expect(screen.getAllByText('Plugin').length).toBeGreaterThanOrEqual(1);

        const policySections = screen.getAllByTestId('console-policy-section');
        const policyIds = policySections.map((el) => el.textContent);
        expect(policyIds).toContain(STABLE_ID);
        expect(policyIds).toContain(pluginID);

        // Remote tools rows must not appear in the built-in section.
        const builtInSection = screen.getByTestId('built-in-plugin-servers-section');
        expect(builtInSection.textContent).not.toContain('https://mcp.example.com');
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

        // Unlicensed so remote add is disabled; built-in cards must stay read-only.
        mockUseIsLicensedFor.mockReturnValue(false);
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
