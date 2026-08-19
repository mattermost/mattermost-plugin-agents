// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ServiceInfo} from '@/types/agents';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

import {AgentDraft} from '../agent_config_view';

import ConfigTab from './config_tab';

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
            formatNumber: (value: number) => new Intl.NumberFormat('en').format(value),
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    fetchModelsForAgentService: jest.fn(),
    getBotProfilePictureUrl: jest.fn(),
}));

const {fetchModelsForAgentService, getBotProfilePictureUrl} = jest.requireMock('@/client') as {
    fetchModelsForAgentService: jest.Mock;
    getBotProfilePictureUrl: jest.Mock;
};

const draft: AgentDraft = {
    displayName: 'My Agent',
    username: 'myagent',
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
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: ['web_search'],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    maxToolTurns: 30,
};

beforeEach(() => {
    fetchModelsForAgentService.mockResolvedValue([]);
    getBotProfilePictureUrl.mockResolvedValue('');
});

// Structured output is configured per service by administrators, so the agent
// form must not offer a per-agent toggle for any provider.
describe.each([
    {type: 'anthropic', name: 'Anthropic'},
    {type: 'openai', name: 'OpenAI'},
    {type: 'openaicompatible', name: 'OpenAI Compatible'},
    {type: 'azure', name: 'Azure'},
])('ConfigTab advanced configuration for a $type service', ({type, name}) => {
    const services: ServiceInfo[] = [{
        id: 'svc_1',
        name,
        type,
        defaultModel: 'some-model',
        outputTokenLimit: 4096,
        useResponsesAPI: true,
    }];

    it('has no structured output control', async () => {
        render(
            <IntlProvider locale='en'>
                <ConfigTab
                    draft={draft}
                    onChange={jest.fn()}
                    onAvatarChange={jest.fn()}
                    services={services}
                />
            </IntlProvider>,
        );

        fireEvent.click(screen.getByRole('button', {name: /Advanced configuration/}));

        // The provider-specific block that used to host the toggle still renders.
        await waitFor(() => expect(screen.getByText('Enable Vision')).not.toBeNull());

        expect(screen.queryByText('Structured Output')).toBeNull();
        expect(screen.queryByText(/structured JSON output/)).toBeNull();
    });
});
