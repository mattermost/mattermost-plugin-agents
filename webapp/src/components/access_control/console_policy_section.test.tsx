// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {AccessControlPolicy} from '@/types/access_control';

import ConsolePolicySection from './console_policy_section';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/utils/access_control', () => {
    const actual = jest.requireActual('@/utils/access_control');
    return {
        ...actual,
        useABACSupport: () => ({supported: true, loading: false}),
    };
});

jest.mock('@/client/access_control', () => ({
    getAgentAccessPolicy: jest.fn(),
    putAgentAccessPolicy: jest.fn(),
    deleteAgentAccessPolicy: jest.fn(),
    getServiceAccessPolicy: jest.fn(),
    putServiceAccessPolicy: jest.fn(),
    deleteServiceAccessPolicy: jest.fn(),
    getMCPServerAccessPolicy: jest.fn(),
    putMCPServerAccessPolicy: jest.fn(),
    deleteMCPServerAccessPolicy: jest.fn(),
    getAccessControlFields: jest.fn(),
    checkAccessControlExpression: jest.fn(),
    testAccessControlExpression: jest.fn(),
    getAccessControlVisualAST: jest.fn(),
}));

const client = jest.requireMock('@/client/access_control') as Record<string, jest.Mock>;

const FakeTableEditor = ({value}: {value: string}) => (
    <div data-testid='table-editor'>{value}</div>
);

const FakeCELEditor = ({value}: {value: string}) => (
    <div data-testid='cel-editor'>{value}</div>
);

function policyFor(id: string): AccessControlPolicy {
    return {
        id,
        name: 'Service',
        type: 'mattermost-ai:service',
        active: true,
        create_at: 1,
        revision: 1,
        version: 'v0.2',
        roles: [],
        imports: [],
        rules: [{actions: ['use'], expression: 'user.attributes.team == "sales"'}],
        props: {},
    };
}

function renderSection(resourceId: string) {
    return render(
        <IntlProvider locale='en'>
            <ConsolePolicySection
                resourceType='service'
                resourceId={resourceId}
                resourceDisplayName='Service'
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    Object.values(client).forEach((mock) => mock.mockReset());
    client.getServiceAccessPolicy.mockImplementation((id: string) => Promise.resolve(policyFor(id)));
    client.getAccessControlFields.mockResolvedValue([]);
    (window as unknown as {Components?: Record<string, unknown>}).Components = {
        AccessControlTableEditor: FakeTableEditor,
        AccessControlCELEditor: FakeCELEditor,
    };
});

afterEach(() => {
    delete (window as unknown as {Components?: Record<string, unknown>}).Components;
});

describe('ConsolePolicySection', () => {
    test('the editor mounts on first expand and stays mounted when collapsed', async () => {
        renderSection('serviceidaaaaaaaaaaaaaaaaa');

        expect(screen.queryByTestId('table-editor')).toBeNull();
        expect(client.getServiceAccessPolicy).not.toHaveBeenCalled();
        expect(client.getAccessControlFields).not.toHaveBeenCalled();

        fireEvent.click(screen.getByText('Access policy'));
        const editor = await screen.findByTestId('table-editor');
        expect(editor.closest('[inert]')).toBeNull();
        expect(client.getServiceAccessPolicy).toHaveBeenCalledTimes(1);
        expect(client.getAccessControlFields).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByText('Advanced'));
        expect(await screen.findByTestId('cel-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('Access policy'));
        const collapsedEditor = screen.getByTestId('cel-editor');
        expect(collapsedEditor.closest('[inert]')).toBeTruthy();
        expect(screen.queryByTestId('table-editor')).toBeNull();

        fireEvent.click(screen.getByText('Access policy'));
        expect(screen.getByTestId('cel-editor').closest('[inert]')).toBeNull();
        expect(client.getServiceAccessPolicy).toHaveBeenCalledTimes(1);
        expect(client.getAccessControlFields).toHaveBeenCalledTimes(1);
    });

    test('a resource swap while the delete dialog is open discards the dialog and fires no delete', async () => {
        // PolicyEditor is keyed by resource identity, so changing resourceId
        // remounts it: the old resource's dialog is gone and its delete can
        // never target the new one.
        const {rerender} = renderSection('serviceidaaaaaaaaaaaaaaaaa');

        fireEvent.click(screen.getByText('Access policy'));
        expect(await screen.findByTestId('table-editor')).toBeTruthy();

        fireEvent.click(screen.getByText('Remove policy'));
        expect(screen.getByText('Remove access policy?')).toBeTruthy();

        rerender(
            <IntlProvider locale='en'>
                <ConsolePolicySection
                    resourceType='service'
                    resourceId='serviceidbbbbbbbbbbbbbbbbb'
                    resourceDisplayName='Service'
                />
            </IntlProvider>,
        );

        // Dialog gone; the new resource's editor loads fresh.
        expect(screen.queryByText('Remove access policy?')).toBeNull();
        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(client.getServiceAccessPolicy).toHaveBeenCalledWith('serviceidbbbbbbbbbbbbbbbbb');

        // Nothing (neither the old dialog's confirm nor anything else) may
        // fire a delete across the swap.
        expect(client.deleteServiceAccessPolicy).not.toHaveBeenCalled();
    });

    test('a legacy (non-minted) service id renders an explanatory note instead of the editor', async () => {
        // Hand-crafted ids (raw config PUT before server-side minting, e.g.
        // "mock-openai") can never carry a policy; the section must explain
        // that instead of surfacing a load failure.
        renderSection('mock-openai');

        fireEvent.click(screen.getByText('Access policy'));

        expect(await screen.findByText("Access policies aren't available for this service because it has a legacy ID.")).toBeTruthy();
        expect(screen.queryByTestId('table-editor')).toBeNull();
        expect(screen.queryByTestId('cel-editor')).toBeNull();
        expect(client.getServiceAccessPolicy).not.toHaveBeenCalled();
    });

    test('a legacy (non-minted) MCP server id renders the MCP note instead of the editor', async () => {
        render(
            <IntlProvider locale='en'>
                <ConsolePolicySection
                    resourceType='mcp'
                    resourceId='my-legacy-mcp'
                    resourceDisplayName='Legacy MCP'
                />
            </IntlProvider>,
        );

        fireEvent.click(screen.getByText('Access policy'));

        expect(await screen.findByText("Access policies aren't available for this MCP server because it has a legacy ID.")).toBeTruthy();
        expect(screen.queryByTestId('table-editor')).toBeNull();
        expect(screen.queryByTestId('cel-editor')).toBeNull();
        expect(client.getMCPServerAccessPolicy).not.toHaveBeenCalled();
    });

    test('sysadmins get Simple and Advanced editors, starting in Simple', async () => {
        renderSection('serviceidaaaaaaaaaaaaaaaaa');

        fireEvent.click(screen.getByText('Access policy'));

        expect(await screen.findByTestId('table-editor')).toBeTruthy();
        expect(screen.getByText('Simple')).toBeTruthy();
        expect(screen.getByText('Advanced')).toBeTruthy();
        expect(screen.queryByTestId('cel-editor')).toBeNull();

        fireEvent.click(screen.getByText('Advanced'));
        expect(await screen.findByTestId('cel-editor')).toBeTruthy();
        expect(screen.queryByTestId('table-editor')).toBeNull();
    });
});
