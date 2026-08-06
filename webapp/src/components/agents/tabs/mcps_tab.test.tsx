// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools} from '@/client';
import {useCurrentUserHasSystemPermission} from '@/utils/permissions';

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

jest.mock('@/utils/permissions', () => ({
    useCurrentUserHasSystemPermission: jest.fn(),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

const mockedGetUserMCPTools = getUserMCPTools as unknown as jest.Mock;
const mockedUseCurrentUserHasSystemPermission = useCurrentUserHasSystemPermission as unknown as jest.Mock;

const serviceAccountToggleName = /^Use service accounts for authentication/;
const serviceAccountWarning = /^Anyone who can use this agent acts with its shared service account access/;
const orphanWarning = /from servers that are no longer available/;

const mattermostServer = {
    name: 'Mattermost',
    serverOrigin: 'embedded://mattermost',
    authenticated: true,
    needsOAuth: false,
    authEmail: '',
    tools: [
        {name: 'read_post', description: '', enabled: true, policy: 'auto_run'},
    ],
};

function renderTab(useServiceAccountAuth = false) {
    const onChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <McpsTab
                    enabledTools={[]}
                    autoEnableNewMCPTools={true}
                    useServiceAccountAuth={useServiceAccountAuth}
                    onChange={onChange}
                />
            </IntlProvider>,
        ),
        onChange,
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
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(true);
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

        const off = renderTab(false);
        await screen.findByText('Mattermost');
        const toggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((toggle as HTMLInputElement).checked).toBe(false);
        expect(screen.queryByText(serviceAccountWarning)).toBeNull();

        fireEvent.click(toggle);
        expect(off.onChange).toHaveBeenCalledWith({useServiceAccountAuth: true});
        off.unmount();

        renderTab(true);
        await screen.findByText('Mattermost');
        expect((screen.getByRole('checkbox', {name: serviceAccountToggleName}) as HTMLInputElement).checked).toBe(true);
        expect(screen.getByText(serviceAccountWarning)).not.toBeNull();
    });

    // Non-admins cannot enable the setting, but can turn it off when it is already on.
    test('lets users without manage_system turn service account auth off', async () => {
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(false);
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const off = renderTab(false);
        await screen.findByText('Mattermost');
        expect(screen.queryByRole('checkbox', {name: serviceAccountToggleName})).toBeNull();
        expect(screen.queryByText(serviceAccountWarning)).toBeNull();
        off.unmount();

        const on = renderTab(true);
        await screen.findByText('Mattermost');
        const toggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((toggle as HTMLInputElement).checked).toBe(true);
        expect(screen.getByText(serviceAccountWarning)).not.toBeNull();

        fireEvent.click(toggle);
        expect(on.onChange).toHaveBeenCalledWith({useServiceAccountAuth: false});
    });

    // The setting must stay reachable even when the catalog renders no servers.
    test('shows the service account toggle when no MCP servers are available', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: []});

        renderTab();

        expect(await screen.findByText(/^No MCP servers are configured/)).not.toBeNull();
        expect(screen.getByRole('checkbox', {name: serviceAccountToggleName})).not.toBeNull();
    });
});
