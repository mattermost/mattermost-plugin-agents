// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor, within} from '@testing-library/react';

import type {UserMCPServerInfo} from '@/client';

import {bot, openSubmenu, renderMenu} from './composer_plus_menu_test_harness';

const mockClient = {
    getUserToolPreferences: jest.fn(),
    getUserMCPTools: jest.fn(),
    refreshUserMCPTools: jest.fn(),
    updateUserToolPreferences: jest.fn(),
    disconnectMCPOAuth: jest.fn(),
    getCustomPrompts: jest.fn(),
    getCustomPromptPins: jest.fn(),
    renderCustomPrompt: jest.fn(),
    rewriteMessage: jest.fn(),
    getAgentsBridgeStatus: jest.fn(),
};
jest.mock('@/client', () => Object.fromEntries([
    'getUserToolPreferences', 'getUserMCPTools', 'refreshUserMCPTools', 'updateUserToolPreferences', 'disconnectMCPOAuth',
    'getCustomPrompts', 'getCustomPromptPins', 'renderCustomPrompt', 'rewriteMessage', 'getAgentsBridgeStatus',
].map((name) => [name, (...args: unknown[]) => (mockClient as Record<string, jest.Mock>)[name](...args)])));

const mockLicensed = jest.fn(() => true);
jest.mock('@/license', () => ({
    useIsLicensedFor: () => mockLicensed(),
    licenseAllows: () => false,
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl, FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage};
});

const server = (name: string, overrides: Partial<UserMCPServerInfo> = {}): UserMCPServerInfo => ({
    name,
    serverOrigin: `https://${name.toLowerCase()}.example.com`,
    kind: 'remote',
    authenticated: true,
    needsOAuth: false,
    serviceAccountConfigured: false,
    tools: [],
    ...overrides,
});

const github = server('GitHub');
const jira = server('Jira');

beforeEach(() => {
    Object.values(mockClient).forEach((fn) => fn.mockReset());
    mockLicensed.mockReturnValue(true);
    mockClient.getUserToolPreferences.mockResolvedValue({disabled_servers: [jira.serverOrigin]});
    mockClient.getUserMCPTools.mockResolvedValue({servers: [github, jira]});
    mockClient.getCustomPrompts.mockResolvedValue([]);
    mockClient.getCustomPromptPins.mockResolvedValue([]);
    mockClient.getAgentsBridgeStatus.mockResolvedValue({available: true});
    jest.spyOn(console, 'error').mockImplementation(() => null);
});

afterEach(() => {
    jest.restoreAllMocks();
});

const checkbox = (menu: HTMLElement, name: string) => within(menu).getByRole('menuitemcheckbox', {name: new RegExp(`^${name}`)});

describe('ComposerPlusMenu Tools', () => {
    test('lists the providers checked by the saved preferences', async () => {
        renderMenu();
        const menu = await openSubmenu('Tools');
        await waitFor(() => expect(checkbox(menu, 'GitHub').getAttribute('aria-checked')).toBe('true'));
        expect(checkbox(menu, 'Jira').getAttribute('aria-checked')).toBe('false');
    });

    test('toggling saves the preference and keeps the menu open', async () => {
        mockClient.updateUserToolPreferences.mockResolvedValue({disabled_servers: []});
        renderMenu();
        const menu = await openSubmenu('Tools');
        fireEvent.click(await waitFor(() => checkbox(menu, 'Jira')));
        await waitFor(() => expect(checkbox(menu, 'Jira').getAttribute('aria-checked')).toBe('true'));
        expect(mockClient.updateUserToolPreferences).toHaveBeenCalledWith({disabled_servers: []});
        expect(screen.getByRole('menu', {name: 'Tools'})).toBe(menu);
    });

    test('a failed save reverts the toggle and reports it', async () => {
        mockClient.updateUserToolPreferences.mockRejectedValue(new Error('nope'));
        const {onError} = renderMenu();
        const menu = await openSubmenu('Tools');
        fireEvent.click(await waitFor(() => checkbox(menu, 'GitHub')));
        await waitFor(() => expect(onError).toHaveBeenCalledWith('Couldn\'t update GitHub. Try again.'));
        expect(checkbox(menu, 'GitHub').getAttribute('aria-checked')).toBe('true');
    });

    test('a failed preference load shows an error instead of toggles, and Try again reloads', async () => {
        mockClient.getUserToolPreferences.mockRejectedValueOnce(new Error('down'));
        renderMenu();
        const menu = await openSubmenu('Tools');
        expect((await within(menu).findByRole('alert')).textContent).toBe('Couldn\'t load your tool settings.');
        expect(within(menu).queryAllByRole('menuitemcheckbox')).toHaveLength(0);

        fireEvent.click(within(menu).getByRole('menuitem', {name: 'Try again'}));
        await waitFor(() => expect(checkbox(menu, 'Jira').getAttribute('aria-checked')).toBe('false'));
    });

    test.each([
        {name: 'a failed provider fetch shows an error', fetch: () => mockClient.getUserMCPTools.mockRejectedValue(new Error('down')), text: 'Couldn\'t load tool providers.', role: 'alert'},
        {name: 'no providers shows the empty state', fetch: () => mockClient.getUserMCPTools.mockResolvedValue({servers: []}), text: 'No tool providers available', role: 'status'},
    ])('$name', async ({fetch, text, role}) => {
        fetch();
        renderMenu();
        const menu = await openSubmenu('Tools');
        await waitFor(() => expect(within(menu).getByRole(role).textContent).toBe(text));
    });

    test('only lists the providers the agent may use', async () => {
        renderMenu({bot: {...bot, autoEnableNewMCPTools: false, enabledMCPTools: [{server_origin: github.serverOrigin, tool_name: 'x'}]} as typeof bot});
        const menu = await openSubmenu('Tools');
        await waitFor(() => expect(within(menu).getAllByRole('menuitemcheckbox').map((el) => el.textContent)).toEqual(['GitHub']));
    });

    test('an OAuth provider offers Connect, a connected one Disconnect, a service-account-only one is unavailable', async () => {
        const open = jest.spyOn(window, 'open').mockImplementation(() => null);
        mockClient.disconnectMCPOAuth.mockRejectedValue(new Error('nope'));
        mockClient.getUserMCPTools.mockResolvedValue({servers: [
            server('Linear', {authenticated: false, needsOAuth: true, authURL: 'https://linear.example.com/auth'}),
            server('Notion', {needsOAuth: true}),
            server('N8n', {authenticated: false, serviceAccountConfigured: true}),
        ]});
        const {onError} = renderMenu();
        const menu = await openSubmenu('Tools');

        fireEvent.click(await within(menu).findByRole('menuitem', {name: 'Linear Connect'}));
        expect(open).toHaveBeenCalledWith('https://linear.example.com/auth', '_blank', 'noopener,noreferrer');

        const unavailable = checkbox(menu, 'N8n');
        expect(unavailable.textContent).toBe('N8nUnavailable');
        expect((unavailable as HTMLButtonElement).disabled).toBe(true);

        fireEvent.click(within(menu).getByRole('menuitem', {name: 'Disconnect Notion'}));
        await waitFor(() => expect(onError).toHaveBeenCalledWith('Couldn\'t disconnect Notion. Try again.'));
        expect(mockClient.disconnectMCPOAuth).toHaveBeenCalledWith('Notion');
    });

    test('without the remote MCP license an OAuth provider is a plain toggle', async () => {
        mockLicensed.mockReturnValue(false);
        mockClient.getUserMCPTools.mockResolvedValue({servers: [server('Linear', {authenticated: false, needsOAuth: true, authURL: 'https://x'})]});
        renderMenu();
        const menu = await openSubmenu('Tools');
        await waitFor(() => expect(checkbox(menu, 'Linear')).toBeTruthy());
        expect(within(menu).queryByRole('menuitem', {name: /Connect/})).toBeNull();
    });

    test('Refresh forces a provider refresh', async () => {
        mockClient.refreshUserMCPTools.mockResolvedValue({servers: [server('Fresh')]});
        renderMenu();
        const menu = await openSubmenu('Tools');
        fireEvent.click(await within(menu).findByRole('menuitem', {name: 'Refresh tool providers'}));
        await waitFor(() => expect(checkbox(menu, 'Fresh')).toBeTruthy());
        expect(mockClient.refreshUserMCPTools).toHaveBeenCalledTimes(1);
    });
});
