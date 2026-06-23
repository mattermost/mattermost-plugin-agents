// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools} from '@/client';

import McpsTab from './mcps_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
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
                    mcpDynamicToolLoading={false}
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
});
