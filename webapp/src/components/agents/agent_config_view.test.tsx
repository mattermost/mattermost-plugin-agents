// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitForElementToBeRemoved} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {updateAgent} from '@/client';
import {ServiceInfo} from '@/types/agents';

import AgentConfigView, {AgentDraft} from './agent_config_view';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => {
                if (!values) {
                    return defaultMessage;
                }
                return Object.entries(values).reduce(
                    (message, [key, value]) => message.replace(`{${key}}`, String(value)),
                    defaultMessage,
                );
            },
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    createAgent: jest.fn(),
    updateAgent: jest.fn(),
    uploadAgentAvatar: jest.fn(),
}));

jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {
        All: 0,
    },
    UserAccessLevel: {
        All: 0,
    },
}));

jest.mock('./tabs/config_tab', () => ({
    __esModule: true,
    default: ({draft, onChange, errors = {}}: {draft: AgentDraft; onChange: (updates: Partial<AgentDraft>) => void; errors?: Record<string, string>}) => (
        <>
            <input
                aria-label='Display Name'
                value={draft.displayName}
                onChange={(e) => onChange({displayName: e.target.value})}
            />
            <input
                aria-label='Max tool turns'
                value={draft.maxToolTurns}
                onChange={(e) => onChange({maxToolTurns: Number(e.target.value)})}
            />
            {errors.maxToolTurns && <div>{errors.maxToolTurns}</div>}
        </>
    ),
}));

jest.mock('./tabs/access_tab', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./tabs/mcps_tab', () => ({
    __esModule: true,
    default: () => null,
}));

const services: ServiceInfo[] = [
    {
        id: 'svc_1',
        name: 'Mock Service',
        type: 'openai',
        defaultModel: 'gpt-4.1',
        outputTokenLimit: 4096,
        useResponsesAPI: true,
    },
];

function renderView(onBack = jest.fn()) {
    const result = render(
        <IntlProvider locale='en'>
            <AgentConfigView
                mode='create'
                services={services}
                onBack={onBack}
                onSaved={jest.fn()}
            />
        </IntlProvider>,
    );

    return {
        ...result,
        onBack,
    };
}

describe('AgentConfigView', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('confirms before dismissing unsaved changes from back button', async () => {
        const {onBack} = renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved Agent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));

        expect(screen.getByRole('dialog', {name: 'Discard changes?'})).not.toBeNull();
        expect(onBack).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Keep editing'}));
        await waitForElementToBeRemoved(() => screen.queryByRole('dialog', {name: 'Discard changes?'}));
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Unsaved Agent');

        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));
        fireEvent.click(screen.getByRole('button', {name: 'Discard'}));

        expect(onBack).toHaveBeenCalledTimes(1);
    });

    test('navigates back immediately when there are no unsaved changes', () => {
        const {onBack} = renderView();

        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('loads edit mode without treating existing values as dirty', () => {
        const onBack = jest.fn();

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_1',
                        name: 'existingagent',
                        displayName: 'Existing Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 30,
                    }}
                    services={services}
                    onBack={onBack}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('legacy agent with unset maxToolTurns is not treated as dirty in edit mode', () => {
        // Pre-migration agents (or any record where maxToolTurns is 0) should
        // load into the form with the runner default applied silently; this
        // mirrors what the user would see if they re-saved. The form must not
        // pop the discard dialog when the user presses Escape immediately.
        const onBack = jest.fn();

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_legacy',
                        name: 'legacyagent',
                        displayName: 'Legacy Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 0,
                    }}
                    services={services}
                    onBack={onBack}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('blocks saving when maxToolTurns exceeds the hard cap', () => {
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_1',
                        name: 'existingagent',
                        displayName: 'Existing Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 30,
                    }}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Max tool turns'), {target: {value: '251'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(screen.getByText('Max tool turns must be between 1 and 250')).not.toBeNull();
        expect(updateAgent).not.toHaveBeenCalled();
    });
});
