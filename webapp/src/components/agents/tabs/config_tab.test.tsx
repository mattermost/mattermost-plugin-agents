// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {ServiceInfo} from '@/types/agents';

import {AgentDraft} from '../agent_config_view';

import ConfigTab from './config_tab';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => {
            if (!values) {
                return defaultMessage;
            }
            return Object.entries(values).reduce(
                (message, [key, value]) => message.replace(`{${key}}`, String(value)),
                defaultMessage,
            );
        },
        formatNumber: (value: number) => new Intl.NumberFormat('en').format(value),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    fetchModelsForAgentService: jest.fn().mockResolvedValue([]),
    getBotProfilePictureUrl: jest.fn().mockResolvedValue(''),
}));

jest.mock('src/../../assets/bot_icon.png', () => 'placeholder-icon.png', {virtual: true});

const openaiService: ServiceInfo = {
    id: 'svc_openai',
    name: 'OpenAI Mock',
    type: 'openai',
    defaultModel: 'gpt-4.1',
    outputTokenLimit: 4096,
    useResponsesAPI: true,
};

function makeDraft(overrides: Partial<AgentDraft> = {}): AgentDraft {
    return {
        displayName: 'Test Agent',
        username: 'testagent',
        serviceId: openaiService.id,
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
        maxToolTurns: 30,
        ...overrides,
    };
}

function formRowForLabel(label: string): HTMLElement {
    const labelEl = screen.getByText(label);
    const row = labelEl.closest('div');
    if (!row) {
        throw new Error(`No form row for label: ${label}`);
    }

    // ItemLabel is a <label>; climb to the FormRow grid that also holds the control.
    if (labelEl.tagName === 'LABEL' && labelEl.parentElement) {
        return labelEl.parentElement;
    }
    return row;
}

describe('ConfigTab', () => {
    // AI service, tools, dynamic loading, and native tools stay manager-editable
    // even while service account auth is on (Access / MCP grants stay locked elsewhere).
    test('keeps AI service, tools, dynamic tool loading, and native tools editable', async () => {
        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft()}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    services={[openaiService]}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.getByText('AI Service')).not.toBeNull());

        const aiServiceSelect = within(formRowForLabel('AI Service')).getByRole('combobox');
        expect((aiServiceSelect as HTMLSelectElement).disabled).toBe(false);

        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));

        const enableToolsRadios = within(formRowForLabel('Enable Tools')).getAllByRole('radio');
        expect(enableToolsRadios.length).toBeGreaterThan(0);
        for (const radio of enableToolsRadios) {
            expect((radio as HTMLInputElement).disabled).toBe(false);
        }

        expect((screen.getByLabelText('Dynamic tool loading') as HTMLInputElement).disabled).toBe(false);
        expect(
            (within(screen.getByTestId('native-tool-web_search')).getByRole('checkbox') as HTMLInputElement).disabled,
        ).toBe(false);
    });

    test.each([
        {name: 'creating an agent', savedUsername: '', username: 'testagent', warns: false},
        {name: 'editing without renaming', savedUsername: 'testagent', username: 'testagent', warns: false},
        {name: 'editing with a new username', savedUsername: 'testagent', username: 'renamed', warns: true},
    ])('username stays editable and warns about renames only when one is pending: $name', async ({savedUsername, username, warns}) => {
        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({username})}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    services={[openaiService]}
                    savedUsername={savedUsername}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.getByText('Agent username')).not.toBeNull());

        const usernameInput = within(formRowForLabel('Agent username')).getByRole('textbox') as HTMLInputElement;
        expect(usernameInput.disabled).toBe(false);
        expect(screen.queryByText(/people and integrations using @testagent will need to switch/) !== null).toBe(warns);
    });

    test('shows vision and effort reasoning for north without native tools', async () => {
        const northService: ServiceInfo = {
            id: 'svc_north',
            name: 'North',
            type: 'north',
            defaultModel: '',
            outputTokenLimit: 4096,
            useResponsesAPI: false,
        };

        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={makeDraft({serviceId: northService.id})}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    services={[northService]}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.getByText('AI Service')).not.toBeNull());
        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));

        expect(screen.getByText('Enable Vision')).toBeTruthy();
        expect(screen.getByText('Enable Tools')).toBeTruthy();
        expect(screen.getByText('Reasoning')).toBeTruthy();
        expect(screen.getByText('Reasoning Effort')).toBeTruthy();
        expect(screen.queryByTestId('native-tool-web_search')).toBeNull();
        expect(screen.queryByText('Native OpenAI Tools')).toBeNull();
        expect(screen.queryByText('Structured Output')).toBeNull();
    });
});
