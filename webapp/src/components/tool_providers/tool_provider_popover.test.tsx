// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, renderHook, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import type {LLMBot} from '@/bots';
import {getUserMCPTools, getUserToolPreferences, refreshUserMCPTools, updateUserToolPreferences} from '@/client';

import ToolProviderPopover, {UserMCPServerInfo, useToolProviders} from './tool_provider_popover';
import {resetToolPreferences} from './use_tool_preferences';

jest.mock('@/client', () => ({
    disconnectMCPOAuth: jest.fn(),
    getUserMCPTools: jest.fn(),
    getUserToolPreferences: jest.fn(),
    refreshUserMCPTools: jest.fn(),
    updateUserToolPreferences: jest.fn(),
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

// WithTooltip renders its title alongside children so tests can assert the tooltip text.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children, title}: {children: React.ReactNode; title?: React.ReactNode}) => (
        <>
            {children}
            <div>{title}</div>
        </>
    ),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    IntlProvider: ({children}: {children: React.ReactNode}) => children,
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    }),
}));

jest.mock('@mattermost/compass-icons/components', () => ({
    ChevronDownIcon: () => <span data-testid='chevron-icon'/>,
    RefreshIcon: () => <span data-testid='refresh-icon'/>,
}));

const mockGetUserMCPTools = getUserMCPTools as jest.MockedFunction<typeof getUserMCPTools>;
const mockGetUserToolPreferences = getUserToolPreferences as jest.MockedFunction<typeof getUserToolPreferences>;
const mockRefreshUserMCPTools = refreshUserMCPTools as jest.MockedFunction<typeof refreshUserMCPTools>;
const mockUpdateUserToolPreferences = updateUserToolPreferences as jest.MockedFunction<typeof updateUserToolPreferences>;

const saOnlyServer: UserMCPServerInfo = {
    name: 'n8n',
    serverOrigin: 'https://n8n.example.com/mcp',
    kind: 'remote',
    authenticated: false,
    needsOAuth: false,
    serviceAccountConfigured: true,
    tools: [],
};

const initialServer: UserMCPServerInfo = {
    name: 'Initial Server',
    serverOrigin: 'https://initial.example.com',
    kind: 'remote',
    authenticated: true,
    needsOAuth: false,
    serviceAccountConfigured: false,
    tools: [],
};

const refreshedServer: UserMCPServerInfo = {
    name: 'Refreshed Server',
    serverOrigin: 'https://refreshed.example.com',
    kind: 'remote',
    authenticated: true,
    needsOAuth: false,
    serviceAccountConfigured: false,
    tools: [],
};

const bot = {id: 'bot1', displayName: 'Bot', autoEnableNewMCPTools: true, enabledMCPTools: null} as LLMBot;

function renderComponent(servers: UserMCPServerInfo[] = [initialServer]) {
    mockGetUserMCPTools.mockResolvedValue({servers});
    return render(
        <IntlProvider locale='en'>
            <ToolProviderPopover bot={bot}/>
        </IntlProvider>,
    );
}

beforeEach(() => {
    resetToolPreferences();
    mockGetUserToolPreferences.mockResolvedValue({disabled_servers: []});
});

async function openToolsMenu() {
    fireEvent.click(screen.getByRole('button', {name: 'Tools'}));
    await screen.findByText('Tool Providers');
}

describe('ToolProviderPopover', () => {
    beforeEach(() => {
        mockGetUserMCPTools.mockResolvedValue({servers: [initialServer]});
        mockRefreshUserMCPTools.mockResolvedValue({servers: [refreshedServer]});
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    test('the Tools dropdown opens a menu of provider rows', async () => {
        renderComponent([initialServer]);

        const trigger = screen.getByRole('button', {name: 'Tools'});
        expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
        expect(trigger.getAttribute('aria-expanded')).toBe('false');
        await openToolsMenu();

        expect(trigger.getAttribute('aria-expanded')).toBe('true');
        expect(screen.getByRole('menu', {name: 'Tool Providers'})).not.toBeNull();
        const row = await screen.findByRole('menuitemcheckbox', {name: 'Initial Server'});
        expect(row.getAttribute('aria-checked')).toBe('true');
    });

    test('a row toggles its provider, keeping the other saved ones', async () => {
        mockGetUserToolPreferences.mockResolvedValue({disabled_servers: ['https://saved.example.com']});
        renderComponent([initialServer]);
        mockUpdateUserToolPreferences.mockResolvedValue({disabled_servers: []});

        await openToolsMenu();
        const row = await screen.findByRole('menuitemcheckbox', {name: 'Initial Server'});
        fireEvent.click(row);

        await waitFor(() => expect(mockUpdateUserToolPreferences).toHaveBeenCalledWith({disabled_servers: ['https://saved.example.com', initialServer.serverOrigin]}));
        expect(row.getAttribute('aria-checked')).toBe('false');
    });

    test('preferences that failed to load show an error with a retry, and no toggles that could overwrite them', async () => {
        jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetUserToolPreferences.mockRejectedValueOnce(new Error('down'));
        renderComponent([initialServer]);

        await openToolsMenu();
        await screen.findByText('Couldn\'t load your tool settings.');
        expect(screen.queryByRole('menuitemcheckbox')).toBeNull();
        expect(mockUpdateUserToolPreferences).not.toHaveBeenCalled();

        mockGetUserToolPreferences.mockResolvedValueOnce({disabled_servers: [initialServer.serverOrigin]});
        fireEvent.click(screen.getByRole('menuitem', {name: 'Try again'}));
        const row = await screen.findByRole('menuitemcheckbox', {name: 'Initial Server'});
        expect(row.getAttribute('aria-checked')).toBe('false');
    });

    test('shows loading instead of toggles while the preferences load', async () => {
        mockGetUserToolPreferences.mockReturnValue(new Promise(() => null));
        renderComponent([initialServer]);

        await openToolsMenu();
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalled());
        expect(screen.getByText('Loading providers...')).not.toBeNull();
        expect(screen.queryByRole('menuitemcheckbox')).toBeNull();
    });

    test('refresh forces a user tools refresh', async () => {
        renderComponent();

        fireEvent.click(screen.getByRole('button', {name: 'Tools'}));
        fireEvent.click(await screen.findByRole('button', {name: 'Refresh tool providers'}));

        await waitFor(() => expect(mockRefreshUserMCPTools).toHaveBeenCalledTimes(1));
        expect(screen.getByText('Refreshed Server')).not.toBeNull();
    });

    test('shows Unavailable with a disabled off toggle for an SA-only server', async () => {
        renderComponent([saOnlyServer]);

        await openToolsMenu();
        await screen.findByText('n8n');

        expect(screen.getByText('Unavailable')).not.toBeNull();
        expect(screen.queryByRole('menuitem', {name: /Connect/})).toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();

        const row = screen.getByRole('menuitemcheckbox', {name: /n8n/}) as HTMLButtonElement;
        expect(row.getAttribute('aria-checked')).toBe('false');
        expect(row.disabled).toBe(true);
        expect(mockUpdateUserToolPreferences).not.toHaveBeenCalled();
    });

    test('still shows Connect for an unauthenticated OAuth server', async () => {
        const oauthServer: UserMCPServerInfo = {
            name: 'OAuth Server',
            serverOrigin: 'https://oauth.example.com/mcp',
            kind: 'remote',
            authenticated: false,
            needsOAuth: true,
            authURL: 'http://localhost/oauth/start',
            serviceAccountConfigured: false,
            tools: [],
        };
        mockGetUserMCPTools.mockResolvedValue({servers: [oauthServer]});
        renderComponent([oauthServer]);

        await openToolsMenu();
        await screen.findByText('OAuth Server');

        expect(screen.getByRole('menuitem', {name: /OAuth Server/})).toHaveProperty('textContent', expect.stringContaining('Connect'));
        expect(screen.queryByText('Unavailable')).toBeNull();
    });

    test.each([
        {authenticated: false, expectDisconnect: false},
        {authenticated: true, expectDisconnect: true},
    ])('hides Connect when remote MCP is not licensed (authenticated=$authenticated)', async ({authenticated, expectDisconnect}) => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockReturnValue(false);

        const oauthServer: UserMCPServerInfo = {
            name: 'OAuth Server',
            serverOrigin: 'https://oauth.example.com/mcp',
            kind: 'remote',
            authenticated,
            needsOAuth: true,
            authURL: 'http://localhost/oauth/start',
            serviceAccountConfigured: false,
            tools: [],
        };
        mockGetUserMCPTools.mockResolvedValue({servers: [oauthServer]});
        renderComponent([oauthServer]);

        await openToolsMenu();
        await screen.findByText('OAuth Server');
        expect(screen.queryByRole('menuitem', {name: /Connect$/})).toBeNull();
        expect(screen.queryByRole('menuitem', {name: /^Disconnect/}) !== null).toBe(expectDisconnect);
        useIsLicensedFor.mockReturnValue(true);
    });

    test('does not mark an authenticated SA-configured server as unavailable', async () => {
        const connectedServer: UserMCPServerInfo = {
            name: 'n8n',
            serverOrigin: 'https://n8n.example.com/mcp',
            kind: 'remote',
            authenticated: true,
            needsOAuth: false,
            serviceAccountConfigured: true,
            tools: [{name: 'workflow_list', description: '', enabled: true, policy: 'ask'}],
        };
        mockGetUserMCPTools.mockResolvedValue({servers: [connectedServer]});
        renderComponent([connectedServer]);

        await openToolsMenu();
        await screen.findByText('n8n');

        expect(screen.queryByText('Unavailable')).toBeNull();
        const row = screen.getByRole('menuitemcheckbox', {name: /n8n/}) as HTMLButtonElement;
        expect(row.getAttribute('aria-checked')).toBe('true');
        expect(row.disabled).toBe(false);
    });
});

describe('useToolProviders', () => {
    afterEach(() => {
        jest.clearAllMocks();
    });

    test('a slow earlier fetch never overwrites a newer one', async () => {
        let resolveSlow!: (value: {servers: UserMCPServerInfo[]}) => void;
        mockGetUserMCPTools.mockReturnValueOnce(new Promise((resolve) => {
            resolveSlow = resolve;
        }));
        mockRefreshUserMCPTools.mockResolvedValueOnce({servers: [refreshedServer]});
        const {result} = renderHook(() => useToolProviders(bot));

        let slow!: Promise<void>;
        act(() => {
            slow = result.current.fetchServers();
        });
        await act(() => result.current.fetchServers({showLoading: true, forceRefresh: true}));
        await act(async () => {
            resolveSlow({servers: [initialServer]});
            await slow;
        });

        expect(result.current.servers).toEqual([refreshedServer]);
        expect(result.current.loading).toBe(false);
    });

    test.each([
        {name: 'a failed fetch sets error until a later one succeeds', fail: true},
        {name: 'a successful fetch leaves error unset', fail: false},
    ])('$name', async ({fail}) => {
        if (fail) {
            mockGetUserMCPTools.mockRejectedValueOnce(new Error('down'));
        }
        mockGetUserMCPTools.mockResolvedValue({servers: [initialServer]});
        jest.spyOn(console, 'error').mockImplementation(() => null);
        const {result} = renderHook(() => useToolProviders(bot));

        await act(() => result.current.fetchServers());
        expect(result.current.error).toBe(fail);

        await act(() => result.current.fetchServers());
        expect(result.current.error).toBe(false);
        expect(result.current.servers).toEqual([initialServer]);
    });

    test('toggle reverts and resolves false when the save fails', async () => {
        mockUpdateUserToolPreferences.mockRejectedValueOnce(new Error('nope'));
        const {result} = renderHook(() => useToolProviders(bot));
        await waitFor(() => expect(result.current.prefs.status).toBe('ready'));

        let saved = true;
        await act(async () => {
            saved = await result.current.toggle(initialServer.serverOrigin, false);
        });

        expect(saved).toBe(false);
        expect(mockUpdateUserToolPreferences).toHaveBeenCalledWith({disabled_servers: [initialServer.serverOrigin]});
        expect(result.current.prefs.disabledServers).toEqual([]);
    });

    test('a toggle after the preferences failed to load saves nothing', async () => {
        jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetUserToolPreferences.mockRejectedValueOnce(new Error('down'));
        const {result} = renderHook(() => useToolProviders(bot));
        await waitFor(() => expect(result.current.prefs.status).toBe('error'));

        let saved = true;
        await act(async () => {
            saved = await result.current.toggle(initialServer.serverOrigin, false);
        });

        expect(saved).toBe(false);
        expect(mockUpdateUserToolPreferences).not.toHaveBeenCalled();
    });

    test('a toggle in one copy shows in the other', async () => {
        mockUpdateUserToolPreferences.mockResolvedValueOnce({disabled_servers: [initialServer.serverOrigin]});
        const a = renderHook(() => useToolProviders(bot));
        const b = renderHook(() => useToolProviders(bot));
        await waitFor(() => expect(a.result.current.prefs.status).toBe('ready'));

        await act(async () => {
            await a.result.current.toggle(initialServer.serverOrigin, false);
        });

        expect(b.result.current.prefs.disabledServers).toEqual([initialServer.serverOrigin]);
    });
});
