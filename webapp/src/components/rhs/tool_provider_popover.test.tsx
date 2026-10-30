// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, renderHook, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools, refreshUserMCPTools, updateUserToolPreferences} from '@/client';

import ToolProviderPopover, {UserMCPServerInfo, publishDisabledServers, useToolProviders} from './tool_provider_popover';

jest.mock('@/client', () => ({
    disconnectMCPOAuth: jest.fn(),
    getUserMCPTools: jest.fn(),
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

function renderComponent(servers: UserMCPServerInfo[] = [initialServer]) {
    const onDisabledServersChange = jest.fn();
    return {
        onDisabledServersChange,
        ...render(
            <IntlProvider locale='en'>
                <ToolProviderPopover
                    disabledServers={[]}
                    onDisabledServersChange={onDisabledServersChange}
                    preloadedServers={servers}
                    autoEnableNewMCPTools={true}
                />
            </IntlProvider>,
        ),
    };
}

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

    test('a row toggles its provider', async () => {
        const {onDisabledServersChange} = renderComponent([initialServer]);
        mockUpdateUserToolPreferences.mockResolvedValue({disabled_servers: [initialServer.serverOrigin]});

        await openToolsMenu();
        fireEvent.click(await screen.findByRole('menuitemcheckbox', {name: 'Initial Server'}));

        await waitFor(() => expect(mockUpdateUserToolPreferences).toHaveBeenCalledWith({disabled_servers: [initialServer.serverOrigin]}));
        expect(onDisabledServersChange).toHaveBeenCalledWith([initialServer.serverOrigin]);
    });

    test('refresh forces a user tools refresh', async () => {
        renderComponent();

        fireEvent.click(screen.getByRole('button', {name: 'Tools'}));
        fireEvent.click(await screen.findByRole('button', {name: 'Refresh tool providers'}));

        await waitFor(() => expect(mockRefreshUserMCPTools).toHaveBeenCalledTimes(1));
        expect(screen.getByText('Refreshed Server')).not.toBeNull();
    });

    test('shows Unavailable with a disabled off toggle for an SA-only server', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: [saOnlyServer]});
        const {onDisabledServersChange} = renderComponent([saOnlyServer]);

        await openToolsMenu();
        await screen.findByText('n8n');

        expect(screen.getByText('Unavailable')).not.toBeNull();
        expect(screen.queryByRole('menuitem', {name: /Connect/})).toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();

        const row = screen.getByRole('menuitemcheckbox', {name: /n8n/}) as HTMLButtonElement;
        expect(row.getAttribute('aria-checked')).toBe('false');
        expect(row.disabled).toBe(true);
        expect(mockUpdateUserToolPreferences).not.toHaveBeenCalled();
        expect(onDisabledServersChange).not.toHaveBeenCalled();
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
    const hookArgs = {disabledServers: [], onDisabledServersChange: jest.fn(), autoEnableNewMCPTools: true};

    afterEach(() => {
        jest.clearAllMocks();
    });

    test('a slow earlier fetch never overwrites a newer one', async () => {
        let resolveSlow!: (value: {servers: UserMCPServerInfo[]}) => void;
        mockGetUserMCPTools.mockReturnValueOnce(new Promise((resolve) => {
            resolveSlow = resolve;
        }));
        mockRefreshUserMCPTools.mockResolvedValueOnce({servers: [refreshedServer]});
        const {result} = renderHook(() => useToolProviders(hookArgs));

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
        const {result} = renderHook(() => useToolProviders(hookArgs));

        await act(() => result.current.fetchServers());
        expect(result.current.error).toBe(fail);

        await act(() => result.current.fetchServers());
        expect(result.current.error).toBe(false);
        expect(result.current.servers).toEqual([initialServer]);
    });

    test('toggle reverts and resolves false when the save fails', async () => {
        mockUpdateUserToolPreferences.mockRejectedValueOnce(new Error('nope'));
        const onDisabledServersChange = jest.fn();
        const {result} = renderHook(() => useToolProviders({...hookArgs, onDisabledServersChange}));

        let saved = true;
        await act(async () => {
            saved = await result.current.toggle(initialServer.serverOrigin, false);
        });

        expect(saved).toBe(false);
        expect(onDisabledServersChange.mock.calls).toEqual([[[initialServer.serverOrigin]], [[]]]);
    });
});

describe('useToolProviders preference sync', () => {
    afterEach(() => {
        jest.clearAllMocks();
    });

    const twoCopies = () => {
        const onA = jest.fn();
        const onB = jest.fn();
        const a = renderHook(() => useToolProviders({disabledServers: [], onDisabledServersChange: onA, autoEnableNewMCPTools: true}));
        const b = renderHook(() => useToolProviders({disabledServers: [], onDisabledServersChange: onB, autoEnableNewMCPTools: true}));
        return {onA, onB, a, b};
    };

    test.each([
        {name: 'a saved toggle reaches the other copy', fail: false, expectedB: [[[initialServer.serverOrigin]]]},
        {name: 'a failed toggle reaches it, then its revert', fail: true, expectedB: [[[initialServer.serverOrigin]], [[]]]},
    ])('$name, never echoing back to its own copy', async ({fail, expectedB}) => {
        if (fail) {
            mockUpdateUserToolPreferences.mockRejectedValueOnce(new Error('nope'));
        } else {
            mockUpdateUserToolPreferences.mockResolvedValueOnce({disabled_servers: [initialServer.serverOrigin]});
        }
        const {onA, onB, a} = twoCopies();

        await act(async () => {
            await a.result.current.toggle(initialServer.serverOrigin, false);
        });

        expect(onB.mock.calls).toEqual(expectedB);
        expect(onA.mock.calls).toEqual(expectedB);
    });

    test('an unmounted copy stops listening', () => {
        const {onB, b} = twoCopies();
        b.unmount();
        act(() => publishDisabledServers(['x'], Symbol('elsewhere')));
        expect(onB).not.toHaveBeenCalled();
    });
});
