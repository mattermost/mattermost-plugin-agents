// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools} from '@/client';
import {EnabledTool} from '@/types/agents';

import McpsTab from './mcps_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // One stable intl object: McpsTab's catalog loader is memoized on intl, so a
    // fresh object per render would reload the catalog on every render.
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

const mockedGetUserMCPTools = getUserMCPTools as unknown as jest.Mock;

const serviceAccountToggleName = /^Use service accounts for authentication/;
const serviceAccountWarning = /^Anyone who can use this agent acts with its shared service account access/;

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

function renderTab(props: {
    enabledTools?: EnabledTool[];
    autoEnableNewMCPTools?: boolean;
    useServiceAccountAuth?: boolean;
    onChange?: jest.Mock;
} = {}) {
    const onChange = props.onChange ?? jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <McpsTab
                    enabledTools={props.enabledTools ?? []}
                    autoEnableNewMCPTools={props.autoEnableNewMCPTools ?? true}
                    useServiceAccountAuth={props.useServiceAccountAuth ?? false}
                    onChange={onChange}
                />
            </IntlProvider>,
        ),
        onChange,
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
        mockedGetUserMCPTools.mockResolvedValue({
            servers: [
                {
                    name: 'Mattermost',
                    serverOrigin: 'embedded://mattermost',
                    authenticated: true,
                    needsOAuth: false,
                    authEmail: '',
                    tools: [
                        {name: 'read_post', description: '', enabled: true, policy: 'auto_run'},
                    ],
                },
            ],
        });

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

    test('renders the service account toggle and emits onChange when clicked', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const {onChange} = renderTab({useServiceAccountAuth: false});
        await screen.findByText('Mattermost');

        const toggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((toggle as HTMLInputElement).checked).toBe(false);

        fireEvent.click(toggle);

        expect(onChange).toHaveBeenCalledWith({useServiceAccountAuth: true});
    });

    test('shows the shared-credential warning only when service account auth is enabled', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: [mattermostServer]});

        const enabled = renderTab({useServiceAccountAuth: true});
        await screen.findByText('Mattermost');
        const checkedToggle = screen.getByRole('checkbox', {name: serviceAccountToggleName});
        expect((checkedToggle as HTMLInputElement).checked).toBe(true);
        expect(screen.getByText(serviceAccountWarning)).not.toBeNull();
        enabled.unmount();

        renderTab({useServiceAccountAuth: false});
        await screen.findByText('Mattermost');
        expect(screen.getByRole('checkbox', {name: serviceAccountToggleName})).not.toBeNull();
        expect(screen.queryByText(serviceAccountWarning)).toBeNull();
    });

    // The flag also switches embedded MCP identity, so it must stay reachable
    // when the remote catalog is empty.
    test('shows the service account toggle when no MCP servers are available', async () => {
        mockedGetUserMCPTools.mockResolvedValue({servers: []});

        renderTab();

        expect(await screen.findByText(/^No MCP servers are configured/)).not.toBeNull();
        expect(screen.getByRole('checkbox', {name: serviceAccountToggleName})).not.toBeNull();
    });
});
