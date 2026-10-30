// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

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

import {getMCPSuggestedDescription, getMCPTools, getVettedToolSeed} from '../../client';

import MCPServers, {type MCPConfig, type MCPConfigUpdate, type MCPServerConfig} from './mcp_servers';
import type {PluginServerConfig} from './mcp_types';
/* eslint-enable import/first, import/order */

const mockUseIsLicensedFor = useIsLicensedFor as jest.Mock;
const mockGetMCPTools = getMCPTools as jest.Mock;
const mockGetSuggestion = getMCPSuggestedDescription as jest.Mock;
const mockGetVettedToolSeed = getVettedToolSeed as jest.Mock;

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
    return {
        ...render(
            <IntlProvider locale='en'>
                <MCPServers
                    mcpConfig={mcpConfig}
                    savedServers={savedServers}
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
        mockUseIsLicensedFor.mockReturnValue(true);

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

    it('opens the name editor from a focusable rename button', () => {
        renderServers(makeMCPConfig([existingServer]));

        const rename = screen.getByRole('button', {name: 'Rename Jira'});
        rename.focus();
        expect(document.activeElement).toBe(rename);

        fireEvent.click(rename);
        expect(screen.getByPlaceholderText('Server name')).toBeTruthy();
    });

    it.each(['Enter', 'Escape'])('returns focus to the rename button after %s', (key) => {
        renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByRole('button', {name: 'Rename Jira'}));
        fireEvent.keyDown(screen.getByPlaceholderText('Server name'), {key});

        expect(document.activeElement).toBe(screen.getByRole('button', {name: /^Rename /}));
    });

    it('leaves focus alone when the name editor loses focus', () => {
        renderServers(makeMCPConfig([existingServer]));

        fireEvent.click(screen.getByRole('button', {name: 'Rename Jira'}));
        fireEvent.blur(screen.getByPlaceholderText('Server name'));

        expect(document.activeElement).not.toBe(screen.getByRole('button', {name: 'Rename Jira'}));
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

describe('MCPServers direct messages only', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    function dmOnlyRadio(value: 'True' | 'False') {
        const group = screen.getByRole('radiogroup', {name: 'Direct Messages Only'});
        const radio = Array.from(group.querySelectorAll('label')).find((label) => label.textContent === value)?.querySelector('input');
        expect(radio).toBeTruthy();
        return radio as HTMLInputElement;
    }

    test('turning it on emits dmOnly for that server only', () => {
        const other = {...existingServer, id: 'otherserverid0123456789abc', name: 'Docs', baseURL: 'https://docs.example.com'};
        const {onChange} = renderServers(makeMCPConfig([existingServer, other]));

        const groups = screen.getAllByRole('radiogroup', {name: 'Direct Messages Only'});
        expect(groups).toHaveLength(2);
        const trueRadio = Array.from(groups[0].querySelectorAll('label')).find((label) => label.textContent === 'True')?.querySelector('input');
        fireEvent.click(trueRadio as HTMLInputElement);

        const servers = lastChangedServers(onChange);
        expect(servers[0]).toEqual(expect.objectContaining({id: STABLE_ID, dmOnly: true}));
        expect(servers[1].dmOnly).toBeFalsy();
    });

    test('an edit to another field keeps a server limited to direct messages', () => {
        const {onChange} = renderServers(makeMCPConfig([{...existingServer, dmOnly: true}]));
        expect(dmOnlyRadio('True').checked).toBe(true);

        fireEvent.change(screen.getByPlaceholderText('https://mcp.example.com'), {target: {value: 'https://jira2.example.com'}});

        expect(lastChangedServers(onChange)[0].dmOnly).toBe(true);
    });
});

describe('MCPServers service account headers', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
    });

    // Rows render in DOM order: base Headers first, then Service Account headers.
    const baseHeaderValueInput = () => screen.getAllByPlaceholderText('Value')[0];
    const serviceAccountHeaderValueInput = () => screen.getByPlaceholderText('Header value (e.g. Bearer token)');

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

    // Editing either map must leave the other intact; the base-header half is
    // also the regression pin for the config rebuild dropping serviceAccountHeaders.
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

    // Rows render by index, so a rename that appends would swap rows under the cursor.
    test('renaming a header keeps its position in the emitted map', async () => {
        const {onChange} = await renderOneServer(makeRemoteServer({
            headers: {'X-First': 'one', 'X-Second': 'two'},
        }));

        fireEvent.change(screen.getAllByPlaceholderText('Header name')[0], {target: {value: 'X-Renamed'}});

        expect(Object.keys(savedServer(onChange).headers)).toEqual(['X-Renamed', 'X-Second']);
    });

    test('service account header editor explains name and value are separate fields', async () => {
        await renderOneServer(makeRemoteServer({
            serviceAccountHeaders: {Authorization: 'Bearer pat'},
        }));

        expect(screen.getByPlaceholderText('Header name (e.g. Authorization)')).not.toBeNull();
        expect(screen.getByPlaceholderText('Header value (e.g. Bearer token)')).not.toBeNull();
        expect(screen.getByText(/Do not repeat the header name in the value/)).not.toBeNull();
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

describe('MCPServers description auto-fill', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    const autoFillButton = () => screen.getByRole('button', {name: 'Auto-fill from server'}) as HTMLButtonElement;

    test('is available for a server that matches its saved configuration', () => {
        renderServers(makeMCPConfig([existingServer]), [existingServer]);

        expect(autoFillButton().disabled).toBe(false);
    });

    test.each([
        ['a server that was never saved', makeRemoteServer(), []],
        ['a server whose URL has unsaved changes', {...existingServer, baseURL: 'https://jira-new.example.com'}, [existingServer]],
    ])('is unavailable for %s', (_name, server, saved) => {
        renderServers(makeMCPConfig([server]), saved);

        expect(autoFillButton().disabled).toBe(true);
    });

    test('editing the description keeps the server id', () => {
        const {onChange} = renderServers(makeMCPConfig([existingServer]), [existingServer]);

        fireEvent.change(screen.getByPlaceholderText(/What this integration is for/), {target: {value: 'Jira issues'}});

        const servers = lastChangedServers(onChange);
        expect(servers[0].description).toBe('Jira issues');
        expect(servers[0].id).toBe(STABLE_ID);
    });
});

// Late results must land on the configuration as it is when they arrive, not
// as it was when the request started.
describe('MCPServers results that arrive after further edits', () => {
    const github: MCPServerConfig = {
        id: 'zyxwvutsrqponmlkjihgfedcba',
        name: 'GitHub',
        enabled: true,
        baseURL: 'https://github.example.com',
        headers: {},
    };

    // Holds the configuration the way config.tsx does, resolving update
    // functions against the current state.
    const StatefulServers = ({initial, saved}: {initial: MCPConfig; saved: MCPServerConfig[]}) => {
        const [mcpConfig, setMCPConfig] = useState(initial);
        latestConfig = mcpConfig;
        return (
            <IntlProvider locale='en'>
                <MCPServers
                    mcpConfig={mcpConfig}
                    savedServers={saved}
                    onChange={(update: MCPConfigUpdate) => setMCPConfig((current) => (typeof update === 'function' ? update(current) : update))}
                />
            </IntlProvider>
        );
    };
    let latestConfig: MCPConfig;

    const deferred = <T, >() => {
        let resolve: (value: T) => void = () => null;
        const promise = new Promise<T>((r) => {
            resolve = r;
        });
        return {promise, resolve};
    };

    const urlInputs = () => screen.getAllByPlaceholderText('https://mcp.example.com');
    const deleteGitHub = () => fireEvent.click(screen.getAllByRole('button', {name: 'Delete Server'})[1]);
    const editGitHubURL = () => fireEvent.change(urlInputs()[1], {target: {value: 'https://gh2.example.com'}});
    const editJiraURL = () => fireEvent.change(urlInputs()[0], {target: {value: 'https://jira2.example.com'}});

    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsLicensedFor.mockReturnValue(true);
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
        mockGetVettedToolSeed.mockResolvedValue([]);
    });

    describe('description auto-fill', () => {
        test.each([
            ['another server is deleted', deleteGitHub, (servers: MCPServerConfig[]) => expect(servers.map((s) => s.name)).toEqual(['Jira'])],
            ['another server is edited', editGitHubURL, (servers: MCPServerConfig[]) => expect(servers[1].baseURL).toBe('https://gh2.example.com')],
        ])('fills the description and keeps the change when %s meanwhile', async (_name, edit, check) => {
            const suggestion = deferred<unknown>();
            mockGetSuggestion.mockReturnValue(suggestion.promise);
            render(
                <StatefulServers
                    initial={makeMCPConfig([existingServer, github])}
                    saved={[existingServer, github]}
                />,
            );

            fireEvent.click(screen.getAllByRole('button', {name: 'Auto-fill from server'})[0]);
            edit();
            suggestion.resolve({description: 'Jira issues', source: 'description', needsOAuth: false});

            await waitFor(() => expect(latestConfig.servers?.[0].description).toBe('Jira issues'));
            check(latestConfig.servers ?? []);
        });

        test('drops the description when the server is reached differently by then', async () => {
            const suggestion = deferred<unknown>();
            mockGetSuggestion.mockReturnValue(suggestion.promise);
            render(
                <StatefulServers
                    initial={makeMCPConfig([existingServer, github])}
                    saved={[existingServer, github]}
                />,
            );

            fireEvent.click(screen.getAllByRole('button', {name: 'Auto-fill from server'})[0]);
            editJiraURL();
            await act(async () => {
                suggestion.resolve({description: 'Jira issues', source: 'description', needsOAuth: false});
            });

            expect(mockGetSuggestion).toHaveBeenCalledWith(STABLE_ID);
            expect(latestConfig.servers?.[0].description).toBeFalsy();
            expect(latestConfig.servers?.[0].baseURL).toBe('https://jira2.example.com');
        });
    });

    describe('tool seeding after a URL change', () => {
        const seededTools = [{name: 'search', policy: 'ask' as const, enabled: true}];

        test.each([
            ['another server is deleted', deleteGitHub, (servers: MCPServerConfig[]) => expect(servers.map((s) => s.name)).toEqual(['Jira'])],
            ['another server is edited', editGitHubURL, (servers: MCPServerConfig[]) => expect(servers[1].baseURL).toBe('https://gh2.example.com')],
        ])('seeds the tools and keeps the change when %s meanwhile', async (_name, edit, check) => {
            const seed = deferred<unknown>();
            mockGetVettedToolSeed.mockReturnValue(seed.promise);
            render(
                <StatefulServers
                    initial={makeMCPConfig([existingServer, github])}
                    saved={[existingServer, github]}
                />,
            );

            editJiraURL();
            fireEvent.blur(urlInputs()[0]);
            edit();
            seed.resolve(seededTools);

            await waitFor(() => expect(latestConfig.servers?.[0].tool_configs).toEqual(seededTools));
            check(latestConfig.servers ?? []);
        });

        test('drops the seeded tools when the URL has changed again by then', async () => {
            const seed = deferred<unknown>();
            mockGetVettedToolSeed.mockReturnValue(seed.promise);
            render(
                <StatefulServers
                    initial={makeMCPConfig([existingServer, github])}
                    saved={[existingServer, github]}
                />,
            );

            editJiraURL();
            fireEvent.blur(urlInputs()[0]);
            fireEvent.change(urlInputs()[0], {target: {value: 'https://jira3.example.com'}});
            await act(async () => {
                seed.resolve(seededTools);
            });

            expect(mockGetVettedToolSeed).toHaveBeenCalledWith('https://jira2.example.com');
            expect(latestConfig.servers?.[0].baseURL).toBe('https://jira3.example.com');
            expect(latestConfig.servers?.[0].tool_configs).toBeUndefined();
        });
    });
});
