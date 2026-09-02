// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

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
import {SECRET_PLACEHOLDER} from './plugin_config_types';
/* eslint-enable import/first, import/order */

const mockUseIsBasicsLicensed = useIsBasicsLicensed as jest.Mock;
const {getMCPTools: mockGetMCPTools, getVettedToolSeed: mockGetVettedToolSeed} = jest.requireMock('../../client') as {
    getMCPTools: jest.Mock;
    getVettedToolSeed: jest.Mock;
};

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

const STORED_URL = 'https://mcp.example.com/jira';
const MOVED_URL = 'https://mcp.other.example.com/jira';
const CLEARED_NOTE = /Saved credentials do not carry over/;

// A server as GET /admin/config returns it: every stored credential arrives masked.
function makeServerWithStoredCredentials(): MCPServerConfig {
    return {
        name: 'Jira',
        enabled: true,
        baseURL: STORED_URL,
        headers: {Authorization: SECRET_PLACEHOLDER},
        serviceAccountHeaders: {Authorization: SECRET_PLACEHOLDER},
        clientID: 'client-id',
        clientSecret: SECRET_PLACEHOLDER,
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

// The admin console holds the edited config, so the fields have to be driven
// through real state to observe what a sequence of edits leaves behind.
function renderEditableServers(mcpConfig: MCPConfig) {
    let latest = mcpConfig;

    const Harness = () => {
        const [config, setConfig] = useState(mcpConfig);
        return (
            <IntlProvider locale='en'>
                <MCPServers
                    mcpConfig={config}
                    onChange={(next) => {
                        latest = next;
                        setConfig(next);
                    }}
                />
            </IntlProvider>
        );
    };

    return {
        ...render(<Harness/>),
        server: () => (latest.servers ?? [])[0],
    };
}

const urlField = () => screen.getByPlaceholderText('https://mcp.example.com');

const typeURL = (value: string) => fireEvent.change(urlField(), {target: {value}});

const leaveURLField = async () => {
    await act(async () => {
        fireEvent.blur(urlField());
    });
};

const renameServer = (from: string, to: string) => {
    fireEvent.click(screen.getByText(from));
    const nameField = screen.getByPlaceholderText('Server name');
    fireEvent.change(nameField, {target: {value: to}});
    fireEvent.blur(nameField);
};

const serviceAccountHeaderValueInput = () => screen.getByPlaceholderText('Header value (e.g. Bearer token)');

describe('MCPServers credentials when the server URL changes', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsBasicsLicensed.mockReturnValue(true);

        // The tools prefetch is unrelated to the fields under test; keep it
        // pending so it cannot settle in the middle of an edit sequence.
        mockGetMCPTools.mockReturnValue(new Promise(() => null));
    });

    const cases: {
        name: string;
        edit: () => Promise<void>;
        clientSecret: string;
        headerValue: string;
        serviceAccountHeaderValue: string;
    }[] = [
        {
            name: 'moving the server to another URL empties the untouched credential fields',
            edit: async () => {
                typeURL(MOVED_URL);
                await leaveURLField();
            },
            clientSecret: '',
            headerValue: '',
            serviceAccountHeaderValue: '',
        },
        {
            name: 'a client secret entered before the move is kept',
            edit: async () => {
                fireEvent.change(screen.getByPlaceholderText('Client Secret'), {target: {value: 'entered-secret'}});
                typeURL(MOVED_URL);
                await leaveURLField();
            },
            clientSecret: 'entered-secret',
            headerValue: '',
            serviceAccountHeaderValue: '',
        },
        {
            name: 'a header value entered before the move is kept',
            edit: async () => {
                fireEvent.change(screen.getByPlaceholderText('Value'), {target: {value: 'entered-header'}});
                typeURL(MOVED_URL);
                await leaveURLField();
            },
            clientSecret: '',
            headerValue: 'entered-header',
            serviceAccountHeaderValue: '',
        },
        {
            name: 'a service account header entered before the move is kept',
            edit: async () => {
                fireEvent.change(serviceAccountHeaderValueInput(), {target: {value: 'entered-sa-header'}});
                typeURL(MOVED_URL);
                await leaveURLField();
            },
            clientSecret: '',
            headerValue: '',
            serviceAccountHeaderValue: 'entered-sa-header',
        },
        {
            name: 'editing another field and leaving the URL alone keeps the credential fields',
            edit: async () => {
                renameServer('Jira', 'Jira Cloud');
                await leaveURLField();
            },
            clientSecret: SECRET_PLACEHOLDER,
            headerValue: SECRET_PLACEHOLDER,
            serviceAccountHeaderValue: SECRET_PLACEHOLDER,
        },
        {
            name: 'putting the original URL back before leaving the field keeps the credential fields',
            edit: async () => {
                typeURL(MOVED_URL);
                typeURL(STORED_URL);
                await leaveURLField();
            },
            clientSecret: SECRET_PLACEHOLDER,
            headerValue: SECRET_PLACEHOLDER,
            serviceAccountHeaderValue: SECRET_PLACEHOLDER,
        },
    ];

    test.each(cases)('$name', async ({edit, clientSecret, headerValue, serviceAccountHeaderValue}) => {
        const {server} = renderEditableServers(makeMCPConfig([makeServerWithStoredCredentials()]));

        await edit();

        expect(server().clientSecret).toBe(clientSecret);
        expect(server().headers.Authorization).toBe(headerValue);
        expect(server().serviceAccountHeaders?.Authorization).toBe(serviceAccountHeaderValue);
    });

    test('mid-edit keystrokes leave the credential fields alone', async () => {
        const {server} = renderEditableServers(makeMCPConfig([makeServerWithStoredCredentials()]));

        typeURL('https://mcp.other');

        expect(server().clientSecret).toBe(SECRET_PLACEHOLDER);
        expect(server().headers.Authorization).toBe(SECRET_PLACEHOLDER);
        expect(server().serviceAccountHeaders?.Authorization).toBe(SECRET_PLACEHOLDER);
    });

    test('the URL field explains why the credential fields emptied', async () => {
        renderEditableServers(makeMCPConfig([makeServerWithStoredCredentials()]));

        expect(screen.queryByText(CLEARED_NOTE)).toBeNull();

        typeURL(MOVED_URL);
        await leaveURLField();

        expect(screen.getByText(CLEARED_NOTE)).not.toBeNull();
    });

    test('a credential typed while the tool seed is in flight is kept', async () => {
        let resolveSeed: (value: unknown[]) => void = () => { /* assigned when the mock Promise is created */ };
        mockGetVettedToolSeed.mockReturnValue(new Promise((resolve) => {
            resolveSeed = resolve;
        }));

        const {server} = renderEditableServers(makeMCPConfig([makeServerWithStoredCredentials()]));

        typeURL(MOVED_URL);
        await leaveURLField();
        fireEvent.change(screen.getByPlaceholderText('Client Secret'), {target: {value: 'typed-while-seeding'}});

        await act(async () => {
            resolveSeed([]);
        });

        expect(server().clientSecret).toBe('typed-while-seeding');
        expect(server().baseURL).toBe(MOVED_URL);
    });
});

describe('MCPServers service account headers', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUseIsBasicsLicensed.mockReturnValue(true);
    });

    // Rows render in DOM order: base Headers first, then Service Account headers.
    const baseHeaderValueInput = () => screen.getAllByPlaceholderText('Value')[0];

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
