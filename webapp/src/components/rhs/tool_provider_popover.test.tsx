// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools, refreshUserMCPTools, updateUserToolPreferences} from '@/client';

import ToolProviderPopover, {UserMCPServerInfo} from './tool_provider_popover';

jest.mock('@/client', () => ({
    disconnectMCPOAuth: jest.fn(),
    getUserMCPTools: jest.fn(),
    refreshUserMCPTools: jest.fn(),
    updateUserToolPreferences: jest.fn(),
}));

// OverlayTrigger renders the overlay alongside children so tests can assert the tooltip text.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

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

const unavailableTooltip = /not available to this agent/;

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

    test('refresh button forces a user tools refresh', async () => {
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
        expect(screen.getByText(unavailableTooltip)).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Connect'})).toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();

        const toggle = screen.getByRole('checkbox');
        expect((toggle as HTMLInputElement).checked).toBe(false);
        expect((toggle as HTMLInputElement).disabled).toBe(true);
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

        expect(screen.getByRole('button', {name: 'Connect'})).not.toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();
        expect(screen.queryByText(unavailableTooltip)).toBeNull();
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
        const toggle = screen.getByRole('checkbox');
        expect((toggle as HTMLInputElement).checked).toBe(true);
        expect((toggle as HTMLInputElement).disabled).toBe(false);
    });
});
