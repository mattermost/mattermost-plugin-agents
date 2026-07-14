// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {UserAccessLevel} from '@/components/system_console/bot';

import {AgentDraft, DefaultMaxToolTurns} from '../agent_config_view';

import AccessTab from './access_tab';

// Source strings use defaultMessage without ids (ids are injected at build
// time); render defaultMessage directly, matching the other component tests.
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

// User/channel pickers hit the Mattermost client; not under test here.
jest.mock('@/components/select', () => ({
    SelectUser: () => <div data-testid='select-user'/>,
    SelectChannel: () => <div data-testid='select-channel'/>,
}));

jest.mock('@/components/access_control/policy_editor', () => ({
    __esModule: true,
    default: (props: {resourceId: string; allowSimplified: boolean; allowAdvanced: boolean}) => (
        <div
            data-testid='policy-editor'
            data-resource-id={props.resourceId}
            data-allow-simplified={String(props.allowSimplified)}
            data-allow-advanced={String(props.allowAdvanced)}
        />
    ),
}));

const baseDraft: AgentDraft = {
    displayName: 'My Agent',
    username: 'my-agent',
    serviceId: 'serviceid',
    customInstructions: '',
    channelAccessLevel: 0,
    channelIds: [],
    userAccessLevel: UserAccessLevel.All,
    userIds: [],
    teamIds: [],
    adminUserIds: [],
    enabledTools: [],
    autoEnableNewMCPTools: true,
    mcpDynamicToolLoading: true,
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: [],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    structuredOutputEnabled: false,
    maxToolTurns: DefaultMaxToolTurns,
};

type RenderOptions = {
    draft?: Partial<AgentDraft>;
    agentId?: string;
    abacSupported?: boolean;
    isSystemAdmin?: boolean;
};

function renderTab(options: RenderOptions = {}) {
    const onChange = jest.fn();
    const result = render(
        <IntlProvider locale='en'>
            <AccessTab
                draft={{...baseDraft, ...options.draft}}
                onChange={onChange}
                agentId={options.agentId}
                abacSupported={options.abacSupported ?? true}
                isSystemAdmin={options.isSystemAdmin ?? false}
            />
        </IntlProvider>,
    );
    return {...result, onChange};
}

// The radio labels render as bare text nodes inside the options grid, so
// visibility is asserted via the radio input values.
function findAttributeBasedRadio(): HTMLInputElement | undefined {
    return screen.getAllByRole('radio').
        map((radio) => radio as HTMLInputElement).
        find((radio) => radio.value === String(UserAccessLevel.AttributeBased));
}

describe('AccessTab attribute-based access', () => {
    it('hides the attribute-based option when ABAC is unsupported', () => {
        renderTab({abacSupported: false});
        expect(findAttributeBasedRadio()).toBeUndefined();
    });

    it('shows the attribute-based option when ABAC is supported', () => {
        renderTab();
        expect(findAttributeBasedRadio()).toBeDefined();
    });

    it('keeps the option visible for an already attribute-based agent on a downgraded server, with a warning', () => {
        renderTab({
            abacSupported: false,
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(findAttributeBasedRadio()).toBeDefined();
        expect(screen.getByText('Attribute-based access is configured but not available on this server; users are currently denied access.')).toBeTruthy();
        expect(screen.queryByTestId('policy-editor')).toBeNull();
    });

    it('selecting attribute-based reports the new level', () => {
        const {onChange} = renderTab();

        const attributeBasedRadio = findAttributeBasedRadio();
        expect(attributeBasedRadio).toBeDefined();

        fireEvent.click(attributeBasedRadio as HTMLInputElement);
        expect(onChange).toHaveBeenCalledWith({userAccessLevel: UserAccessLevel.AttributeBased});
    });

    it('hides the user allow/block list in attribute-based mode', () => {
        renderTab({
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });
        expect(screen.queryByText('Allow list')).toBeNull();
        expect(screen.queryByText('Block list')).toBeNull();
    });

    it('shows the save-first note instead of the editor while creating', () => {
        renderTab({
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.getByText('Save the agent first, then define who can use it. Until a policy is defined, all users can use this agent.')).toBeTruthy();
        expect(screen.queryByTestId('policy-editor')).toBeNull();
    });

    it('renders the policy editor for a saved agent, simplified-only for non-admins', () => {
        renderTab({
            agentId: 'agentid',
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        const editor = screen.getByTestId('policy-editor');
        expect(editor.getAttribute('data-resource-id')).toBe('agentid');
        expect(editor.getAttribute('data-allow-simplified')).toBe('true');
        expect(editor.getAttribute('data-allow-advanced')).toBe('false');
    });

    it('enables the advanced editor for system admins', () => {
        renderTab({
            agentId: 'agentid',
            isSystemAdmin: true,
            draft: {userAccessLevel: UserAccessLevel.AttributeBased},
        });

        expect(screen.getByTestId('policy-editor').getAttribute('data-allow-advanced')).toBe('true');
    });
});
