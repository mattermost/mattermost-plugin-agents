// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

import {AgentDraft} from '../agent_config_view';

import AccessTab from './access_tab';

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

jest.mock('@/client', () => ({
    getProfilesByIds: jest.fn().mockResolvedValue([]),
    getTeamsByIds: jest.fn().mockResolvedValue([]),
    getAutocompleteAllUsers: jest.fn().mockResolvedValue({users: []}),
    searchTeams: jest.fn().mockResolvedValue([]),
    getProfilePictureUrl: jest.fn().mockReturnValue(''),
    getTeamIconUrl: jest.fn().mockReturnValue(''),
}));

function makeDraft(overrides: Partial<AgentDraft> = {}): AgentDraft {
    return {
        displayName: 'Test Agent',
        username: 'testagent',
        serviceId: 'svc_1',
        customInstructions: '',
        channelAccessLevel: ChannelAccessLevel.All,
        channelIds: [],
        userAccessLevel: UserAccessLevel.All,
        userIds: [],
        teamIds: [],
        adminUserIds: [],
        enabledTools: [],
        autoEnableNewMCPTools: true,
        mcpDynamicToolLoading: true,
        useServiceAccountAuth: true,
        model: '',
        enableVision: true,
        disableTools: false,
        enabledNativeTools: ['web_search'],
        reasoningEnabled: true,
        reasoningEffort: 'medium',
        thinkingBudget: 0,
        structuredOutputEnabled: false,
        maxToolTurns: 30,
        ...overrides,
    };
}

function formRowForLabel(label: string): HTMLElement {
    const labelEl = screen.getByText(label, {selector: 'label'});
    const row = labelEl.parentElement;
    if (!row) {
        throw new Error(`No form row for label: ${label}`);
    }
    return row;
}

function agentAdminsCombobox(): HTMLInputElement {
    // Disabled react-select inputs are omitted from getByRole's accessibility tree.
    const input = formRowForLabel('Agent admins').querySelector('input[role="combobox"]');
    if (!input) {
        throw new Error('Agent admins combobox not found');
    }
    return input as HTMLInputElement;
}

function renderAccessTab(serviceAccountFieldsLocked: boolean) {
    const onChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <AccessTab
                    draft={makeDraft()}
                    onChange={onChange}
                    serviceAccountFieldsLocked={serviceAccountFieldsLocked}
                />
            </IntlProvider>,
        ),
        onChange,
    };
}

describe('AccessTab', () => {
    // Soft-lock must disable the real Access selectors (channel, user, agent admins).
    test('disables channel access, user access, and agent admins when locked', async () => {
        renderAccessTab(true);

        await waitFor(() => expect(agentAdminsCombobox()).not.toBeNull());

        const channelRadios = within(formRowForLabel('Channel access')).getAllByRole('radio');
        expect(channelRadios.length).toBeGreaterThan(0);
        for (const radio of channelRadios) {
            expect((radio as HTMLInputElement).disabled).toBe(true);
        }

        const userRadios = within(formRowForLabel('User access')).getAllByRole('radio');
        expect(userRadios.length).toBeGreaterThan(0);
        for (const radio of userRadios) {
            expect((radio as HTMLInputElement).disabled).toBe(true);
        }

        expect(agentAdminsCombobox().disabled).toBe(true);
    });

    test('leaves channel access, user access, and agent admins enabled when unlocked', async () => {
        renderAccessTab(false);

        await waitFor(() => expect(agentAdminsCombobox()).not.toBeNull());

        for (const radio of within(formRowForLabel('Channel access')).getAllByRole('radio')) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }
        for (const radio of within(formRowForLabel('User access')).getAllByRole('radio')) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }

        expect(agentAdminsCombobox().disabled).toBe(false);
    });
});
