// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools} from '@/client';

import McpsTab from './mcps_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Stable intl object: the catalog loader is memoized on intl, so a fresh one per render would reload forever.
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getUserMCPTools: jest.fn(),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

// OverlayTrigger renders the overlay alongside children so tests can assert the tooltip text.
jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

const mockedGetUserMCPTools = getUserMCPTools as unknown as jest.Mock;

const serviceAccountToggleName = /^Use service accounts for authentication/;
const serviceAccountWarning = /^Anyone who can use this agent acts with its shared service account access/;
const orphanWarning = /from servers that are no longer available/;
const unavailableTooltip = /not available to this agent/;
const serverToggleName = /Enable all tools for \{serverName\}|Disable all tools for \{serverName\}/;
const toolToggleName = /Enable tool \{toolName\} on \{serverName\}|Disable tool \{toolName\} on \{serverName\}/;

const mattermostServer = {
    name: 'Mattermost',
    serverOrigin: 'embedded://mattermost',
    kind: 'embedded' as const,
    authenticated: true,
    needsOAuth: false,
    authEmail: '',
    serviceAccountConfigured: false,
    tools: [
        {name: 'read_post', description: '', enabled: true, policy: 'auto_run'},
    ],
};

type RenderOpts = {
    agentId?: string;
    enabledTools?: Array<{server_origin: string; tool_name: string}>;
    autoEnableNewMCPTools?: boolean;
    useServiceAccountAuth?: boolean;
    serviceAccountFieldsLocked?: boolean;
    canEditServiceAccountAuth?: boolean;
};

function renderTab({
    agentId,
    enabledTools = [],
    autoEnableNewMCPTools = true,
    useServiceAccountAuth = false,
    serviceAccountFieldsLocked = false,
    canEditServiceAccountAuth = true,
}: RenderOpts = {}) {
    const onChange = jest.fn();
    const onReconcileEnabledTools = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <McpsTab
                    agentId={agentId}
                    enabledTools={enabledTools}
                    autoEnableNewMCPTools={autoEnableNewMCPTools}
                    useServiceAccountAuth={useServiceAccountAuth}
                    serviceAccountFieldsLocked={serviceAccountFieldsLocked}
                    canEditServiceAccountAuth={canEditServiceAccountAuth}
                    onChange={onChange}
                    onReconcileEnabledTools={onReconcileEnabledTools}
                />
            </IntlProvider>,
        ),
        onChange,
        onReconcileEnabledTools,
    };
}

// Draft holding one grant that the loaded catalog does not contain ('deleted_tool').
function renderWithOrphanedTool(useServiceAccountAuth: boolean) {
    const onChange = jest.fn();
    const onReconcileEnabledTools = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <McpsTab
                    enabledTools={[
                        {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
                        {server_origin: 'embedded://mattermost', tool_name: 'deleted_tool'},
                    ]}
                    autoEnableNewMCPTools={false}
                    useServiceAccountAuth={useServiceAccountAuth}
                    serviceAccountFieldsLocked={false}
                    canEditServiceAccountAuth={true}
                    onChange={onChange}
                    onReconcileEnabledTools={onReconcileEnabledTools}
                />
            </IntlProvider>,
        ),
        onChange,
        onReconcileEnabledTools,
    };
}

describe('McpsTab', () => {
    beforeEach(() => {
        mockedGetUserMCPTools.mockReset();
    });

    // MM-69185 regression: when the live MCP catalog drops entries that were
    // saved in enabledTools (orphans), the tab must route the cleanup through
    // onReconcileEnabledTools (which the parent applies to both draft and
    // baseline) rather than the user-edit onChange path. Routing through
    // onChange falsely marks the form dirty and causes "Discard changes?" to
    // appear when the user clicks Cancel without making any edits.
    test('routes orphan reconciliation through onReconcileEnabledTools (MM-69185)', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const onChange = jest.fn();
        const onReconcileEnabledTools = jest.fn();
        const {findByText} = render(
            <IntlProvider locale='en'>
                <McpsTab
                    enabledTools={[
                        {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
                        {server_origin: 'embedded://mattermost', tool_name: 'deleted_tool'},
                    ]}
                    autoEnableNewMCPTools={false}
                    useServiceAccountAuth={false}
                    serviceAccountFieldsLocked={false}
                    canEditServiceAccountAuth={true}
                    onChange={onChange}
                    onReconcileEnabledTools={onReconcileEnabledTools}
                />
            </IntlProvider>,
        );

        await findByText('Mattermost');
        await waitFor(() =>
            expect(onReconcileEnabledTools).toHaveBeenCalledWith([
                {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
            ]),
        );
        expect(onChange).not.toHaveBeenCalled();
    });

    // A service account agent runs tools against the admin-provisioned catalog, so
    // the editing user's own catalog is not authoritative. Reconciling against it
    // would silently drop valid grants (e.g. from an OAuth server this user never
    // connected) the next time the agent is saved.
    test('skips orphan reconciliation while service account auth is enabled', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const withoutServiceAccounts = renderWithOrphanedTool(false);
        await screen.findByText('Mattermost');
        await waitFor(() => expect(withoutServiceAccounts.onReconcileEnabledTools).toHaveBeenCalled());
        expect(screen.getByText(orphanWarning)).not.toBeNull();
        withoutServiceAccounts.unmount();

        const withServiceAccounts = renderWithOrphanedTool(true);
        await screen.findByText('Mattermost');
        expect(screen.queryByText(orphanWarning)).toBeNull();
        expect(withServiceAccounts.onReconcileEnabledTools).not.toHaveBeenCalled();
        expect(withServiceAccounts.onChange).not.toHaveBeenCalled();
    });

    test('service account toggle reflects the draft, updates it, and warns while enabled', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const off = renderTab({useServiceAccountAuth: false});
        await screen.findByText('Mattermost');
        const toggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((toggle as HTMLInputElement).checked).toBe(false);
        expect(screen.queryByText(serviceAccountWarning)).toBeNull();

        fireEvent.click(toggle);
        expect(off.onChange).toHaveBeenCalledWith({useServiceAccountAuth: true});
        off.unmount();

        renderTab({useServiceAccountAuth: true});
        await screen.findByText('Mattermost');
        expect((screen.getByRole('checkbox', {name: serviceAccountToggleName}) as HTMLInputElement).checked).toBe(true);
        expect(screen.getByText(serviceAccountWarning)).not.toBeNull();
    });

    // Non-admins cannot enable the setting, but can turn it off when it is already on.
    test('lets users without manage_system turn service account auth off', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const off = renderTab({
            useServiceAccountAuth: false,
            canEditServiceAccountAuth: false,
        });
        await screen.findByText('Mattermost');
        expect(screen.queryByRole('checkbox', {name: serviceAccountToggleName})).toBeNull();
        expect(screen.queryByText(serviceAccountWarning)).toBeNull();
        off.unmount();

        const on = renderTab({
            useServiceAccountAuth: true,
            canEditServiceAccountAuth: false,
        });
        await screen.findByText('Mattermost');
        const toggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((toggle as HTMLInputElement).checked).toBe(true);
        expect(screen.getByText(serviceAccountWarning)).not.toBeNull();

        fireEvent.click(toggle);
        expect(on.onChange).toHaveBeenCalledWith({useServiceAccountAuth: false});
    });

    // Soft-lock mirrors the server sensitive-field ACL: auto-enable and tool
    // grants are disabled while fields-locked, but the SA off-switch stays usable.
    test('disables auto-enable and tool grants while service account fields are locked', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const onChange = jest.fn();
        render(
            <IntlProvider locale='en'>
                <McpsTab
                    enabledTools={[]}
                    autoEnableNewMCPTools={false}
                    useServiceAccountAuth={true}
                    serviceAccountFieldsLocked={true}
                    canEditServiceAccountAuth={false}
                    onChange={onChange}
                />
            </IntlProvider>,
        );
        await screen.findByText('Mattermost');

        const autoEnable = screen.getByRole('checkbox', {name: /^Automatically enable all MCP tools/});
        expect((autoEnable as HTMLInputElement).disabled).toBe(true);

        const saToggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((saToggle as HTMLInputElement).disabled).toBe(false);

        const serverToggle = screen.getByRole('button', {name: serverToggleName});
        expect((serverToggle as HTMLButtonElement).disabled).toBe(true);
    });

    // The setting must stay reachable even when the catalog renders no servers.
    test('shows the service account toggle when no MCP servers are available', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: []});

        renderTab();

        expect(await screen.findByText(/^No MCP servers are configured/)).not.toBeNull();
        expect(screen.getByRole('checkbox', {name: serviceAccountToggleName})).not.toBeNull();
    });

    test('loads the service-account catalog and hides Connect while SA auth is on', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'n8n',
                serverOrigin: 'https://n8n.example.com/mcp',
                kind: 'remote',
                authenticated: true,
                needsOAuth: false,
                serviceAccountConfigured: true,
                tools: [{name: 'workflow_list', description: '', enabled: true, policy: 'ask'}],
            }],
        });

        renderTab({agentId: 'agent-1', useServiceAccountAuth: true});

        await screen.findByText('n8n');
        expect(mockedGetUserMCPTools).toHaveBeenCalledWith({
            agentId: 'agent-1',
            serviceAccount: true,
        });
        expect(screen.getByText('Connected')).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Connect'})).toBeNull();
        expect(screen.getByText(/service-account catalog, not your personal MCP connections/)).not.toBeNull();
    });

    test('does not label an SA-only server as not connected', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'OAuth Only',
                serverOrigin: 'https://oauth.example.com/mcp',
                kind: 'remote',
                authenticated: false,
                needsOAuth: true,
                authURL: 'http://localhost/oauth/start',
                serviceAccountConfigured: false,
                tools: [],
            }],
        });

        renderTab({useServiceAccountAuth: true});

        await screen.findByText('OAuth Only');
        expect(screen.getByText('No service account credentials')).not.toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Connect'})).toBeNull();
    });

    test('shows Couldn\'t connect for failed SA credentials while SA auth is on', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'n8n',
                serverOrigin: 'https://n8n.example.com/mcp',
                kind: 'remote',
                authenticated: false,
                needsOAuth: false,
                serviceAccountConfigured: true,
                tools: [],
            }],
        });

        renderTab({useServiceAccountAuth: true});

        await screen.findByText('n8n');
        expect(screen.getByText("Couldn't connect")).not.toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Connect'})).toBeNull();
    });

    test('does not treat a local plugin without tools as an SA credential failure', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'Example Plugin',
                serverOrigin: 'plugin://com.example.mcp',
                kind: 'plugin',
                authenticated: false,
                needsOAuth: false,
                serviceAccountConfigured: false,
                tools: [],
            }],
        });

        renderTab({useServiceAccountAuth: true});

        await screen.findByText('Example Plugin');
        expect(screen.getByText('Not connected')).not.toBeNull();
        expect(screen.queryByText('No service account credentials')).toBeNull();
        expect(screen.queryByText("Couldn't connect")).toBeNull();
    });

    test('shows Unavailable for an SA-only server in user mode without persisting off', async () => {
        const server = {
            name: 'n8n',
            serverOrigin: 'https://n8n.example.com/mcp',
            kind: 'remote' as const,
            authenticated: false,
            needsOAuth: false,
            serviceAccountConfigured: true,
            tools: [{name: 'workflow_list', description: '', enabled: true, policy: 'ask'}],
        };
        mockedGetUserMCPTools.mockResolvedValue({servers: [server]});

        const {onChange, onReconcileEnabledTools} = renderTab({
            useServiceAccountAuth: false,
            autoEnableNewMCPTools: false,
            enabledTools: [{server_origin: server.serverOrigin, tool_name: 'workflow_list'}],
        });

        await screen.findByText('n8n');
        expect(screen.getByText('Unavailable')).not.toBeNull();
        expect(screen.getByText(unavailableTooltip)).not.toBeNull();
        expect(screen.queryByText('Not connected')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Connect'})).toBeNull();

        const serverToggle = screen.getByRole('button', {name: serverToggleName});
        expect((serverToggle as HTMLButtonElement).disabled).toBe(true);
        expect(serverToggle.getAttribute('aria-checked')).toBe('false');

        fireEvent.click(serverToggle);
        expect(onChange).not.toHaveBeenCalled();
        expect(onReconcileEnabledTools).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: /Press to expand or collapse tools/}));
        const toolToggle = await screen.findByRole('button', {name: toolToggleName});
        expect((toolToggle as HTMLButtonElement).disabled).toBe(true);
        expect(toolToggle.getAttribute('aria-checked')).toBe('false');

        fireEvent.click(toolToggle);
        expect(onChange).not.toHaveBeenCalled();
    });

    test('does not strip saved grants when an unavailable server lists no tools', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'n8n',
                serverOrigin: 'https://n8n.example.com/mcp',
                kind: 'remote',
                authenticated: false,
                needsOAuth: false,
                serviceAccountConfigured: true,
                tools: [],
            }],
        });

        const {onChange, onReconcileEnabledTools} = renderTab({
            useServiceAccountAuth: false,
            autoEnableNewMCPTools: false,
            enabledTools: [{server_origin: 'https://n8n.example.com/mcp', tool_name: 'workflow_list'}],
        });

        await screen.findByText('n8n');
        expect(screen.getByText('Unavailable')).not.toBeNull();
        await waitFor(() => {
            expect(onChange).not.toHaveBeenCalled();
            expect(onReconcileEnabledTools).not.toHaveBeenCalled();
        });
    });

    test('still shows Connect for an unauthenticated OAuth server in user mode', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'OAuth Server',
                serverOrigin: 'https://oauth.example.com/mcp',
                kind: 'remote',
                authenticated: false,
                needsOAuth: true,
                authURL: 'http://localhost/oauth/start',
                serviceAccountConfigured: false,
                tools: [],
            }],
        });

        renderTab({useServiceAccountAuth: false});

        await screen.findByText('OAuth Server');
        expect(screen.getByRole('button', {name: 'Connect'})).not.toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();
    });

    test('does not mark an authenticated SA-configured server as unavailable in user mode', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'n8n',
                serverOrigin: 'https://n8n.example.com/mcp',
                kind: 'remote',
                authenticated: true,
                needsOAuth: false,
                serviceAccountConfigured: true,
                tools: [{name: 'workflow_list', description: '', enabled: true, policy: 'ask'}],
            }],
        });

        renderTab({
            useServiceAccountAuth: false,
            autoEnableNewMCPTools: false,
            enabledTools: [{server_origin: 'https://n8n.example.com/mcp', tool_name: 'workflow_list'}],
        });

        await screen.findByText('n8n');
        expect(screen.getByText('Connected')).not.toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();

        const serverToggle = screen.getByRole('button', {name: serverToggleName});
        expect(serverToggle.getAttribute('aria-checked')).toBe('true');
        expect((serverToggle as HTMLButtonElement).disabled).toBe(false);
    });

    test('keeps Not connected for an unauthenticated user-mode server without SA headers', async () => {
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [{
                name: 'Headers Only',
                serverOrigin: 'https://headers.example.com/mcp',
                kind: 'remote',
                authenticated: false,
                needsOAuth: false,
                serviceAccountConfigured: false,
                tools: [],
            }],
        });

        renderTab({useServiceAccountAuth: false});

        await screen.findByText('Headers Only');
        expect(screen.getByText('Not connected')).not.toBeNull();
        expect(screen.queryByText('Unavailable')).toBeNull();
    });
});
