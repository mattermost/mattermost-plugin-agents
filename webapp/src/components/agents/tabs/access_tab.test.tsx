// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {DefaultMaxToolTurns} from '@/types/agents';

import {AgentDraft} from '../agent_config_view';

import AccessTab from './access_tab';

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
    getProfilesByIds: jest.fn().mockResolvedValue([]),
    getTeamsByIds: jest.fn().mockResolvedValue([]),
    getAutocompleteAllUsers: jest.fn().mockResolvedValue({users: []}),
    searchTeams: jest.fn().mockResolvedValue([]),
    getProfilePictureUrl: jest.fn().mockReturnValue(''),
    getTeamIconUrl: jest.fn().mockReturnValue(''),
}));

jest.mock('@/components/select', () => ({
    SelectUser: ({disabled}: {disabled?: boolean}) => (
        <input
            role='combobox'
            disabled={disabled}
        />
    ),
    SelectChannel: ({disabled}: {disabled?: boolean}) => (
        <input
            role='combobox'
            disabled={disabled}
        />
    ),
}));

jest.mock('@/components/access_control/policy_editor', () => ({
    __esModule: true,
    default: (props: {resourceId: string; allowSimplified: boolean; allowAdvanced: boolean}) => (
        <div
            data-testid='policy-editor'
            data-resource-id={props.resourceId}
            data-allow-simplified={String(props.allowSimplified)}
            data-allow-advanced={String(props.allowAdvanced)}
        >
            <button type='button'>{'Edit policy'}</button>
        </div>
    ),
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
        maxToolTurns: DefaultMaxToolTurns,
        ...overrides,
    };
}

type RenderOptions = {
    draft?: Partial<AgentDraft>;
    serviceAccountFieldsLocked?: boolean;
    agentId?: string;
    abacSupported?: boolean;
    isSystemAdmin?: boolean;
};

function renderTab(options: RenderOptions = {}) {
    const onChange = jest.fn();
    const result = render(
        <IntlProvider locale='en'>
            <AccessTab
                draft={makeDraft(options.draft)}
                onChange={onChange}
                serviceAccountFieldsLocked={options.serviceAccountFieldsLocked ?? false}
                agentId={options.agentId}
                abacSupported={options.abacSupported ?? true}
                isSystemAdmin={options.isSystemAdmin ?? false}
            />
        </IntlProvider>,
    );
    return {...result, onChange};
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

// The radio labels render as bare text nodes inside the options grid, so
// visibility is asserted via the radio input values.
function findAttributeBasedRadio(): HTMLInputElement | undefined {
    return screen.getAllByRole('radio').
        map((radio) => radio as HTMLInputElement).
        find((radio) => radio.value === String(UserAccessLevel.AttributeBased));
}

describe('AccessTab', () => {
    test('disables channel access, user access, and agent admins when service-account fields are locked', () => {
        renderTab({serviceAccountFieldsLocked: true});

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

    test('leaves channel access, user access, and agent admins enabled when unlocked', () => {
        renderTab();

        for (const radio of within(formRowForLabel('Channel access')).getAllByRole('radio')) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }
        for (const radio of within(formRowForLabel('User access')).getAllByRole('radio')) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }

        expect(agentAdminsCombobox().disabled).toBe(false);
    });

    test('hides the attribute-based option when ABAC is unsupported', () => {
        renderTab({abacSupported: false});
        expect(findAttributeBasedRadio()).toBeUndefined();
    });

    test('shows the attribute-based option when ABAC is supported', () => {
        renderTab();
        expect(findAttributeBasedRadio()).toBeDefined();
    });

    test('keeps the option visible for an already attribute-based agent on a downgraded server, with a warning', () => {
        renderTab({
            abacSupported: false,
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(findAttributeBasedRadio()).toBeDefined();
        expect(screen.getByText('Attribute-based access is configured but not available on this server; users are currently denied access.')).toBeTruthy();
        expect(screen.queryByTestId('policy-editor')).toBeNull();
    });

    test('selecting attribute-based reports the new level', () => {
        const {onChange} = renderTab();
        const attributeBasedRadio = findAttributeBasedRadio();
        expect(attributeBasedRadio).toBeDefined();

        fireEvent.click(attributeBasedRadio as HTMLInputElement);
        expect(onChange).toHaveBeenCalledWith({userAccessLevel: UserAccessLevel.AttributeBased});
    });

    test('hides the user allow/block list in attribute-based mode', () => {
        renderTab({
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.queryByText('Allow list')).toBeNull();
        expect(screen.queryByText('Block list')).toBeNull();
    });

    test('shows the save-first note instead of the editor while creating', () => {
        renderTab({
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.getByText('Save the agent first, then define who can use it. Until a policy is defined, all users can use this agent.')).toBeTruthy();
        expect(screen.queryByTestId('policy-editor')).toBeNull();
    });

    test('renders the policy editor for a saved agent, simplified-only for non-admins', () => {
        renderTab({
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        const editor = screen.getByTestId('policy-editor');
        expect(editor.getAttribute('data-resource-id')).toBe('agentid');
        expect(editor.getAttribute('data-allow-simplified')).toBe('true');
        expect(editor.getAttribute('data-allow-advanced')).toBe('false');
        expect((screen.getByRole('button', {name: 'Edit policy'}) as HTMLButtonElement).disabled).toBe(false);
    });

    test('keeps the policy visible but disables editing when service-account fields are locked', () => {
        renderTab({
            serviceAccountFieldsLocked: true,
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.getByTestId('policy-editor')).not.toBeNull();
        const policyFieldset = screen.getByTestId('policy-editor').closest('fieldset');
        expect(policyFieldset).not.toBeNull();
        expect((policyFieldset as HTMLFieldSetElement).disabled).toBe(true);
    });

    test('enables the advanced editor for system admins', () => {
        renderTab({
            agentId: 'agentid',
            isSystemAdmin: true,
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.getByTestId('policy-editor').getAttribute('data-allow-advanced')).toBe('true');
    });
});
