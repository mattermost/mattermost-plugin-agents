// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ReasoningConfigItem, {defaultReasoningEffort} from './reasoning_config';
import {LLMBotConfig} from './bot';
import {type LLMService} from './service';

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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

const baseService: LLMService = {
    id: 'svc-1',
    name: 'Anthropic',
    type: 'anthropic',
    apiURL: '',
    apiKey: 'test-key',
    orgId: '',
    defaultModel: 'claude-sonnet-4-5-20250929',
    tokenLimit: 0,
    streamingTimeoutSeconds: 0,
    outputTokenLimit: 0,
    useResponsesAPI: false,
    region: '',
    awsAccessKeyID: '',
    awsSecretAccessKey: '',
    vertexProjectID: '',
    vertexProjectNumber: '',
    vertexAuthCredentials: '',
    fallbackServiceID: '',
};

const baseBot: LLMBotConfig = {
    id: 'bot-1',
    name: 'agent',
    displayName: 'Agent',
    serviceID: 'svc-1',
    model: '',
    customInstructions: '',
    enableVision: false,
    disableTools: false,
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    reasoningEnabled: true,
};

function renderItem(bot: Partial<LLMBotConfig>, service: Partial<LLMService> = {}, onChange = jest.fn()) {
    return render(
        <IntlProvider locale='en'>
            <ReasoningConfigItem
                bot={{...baseBot, ...bot}}
                service={{...baseService, ...service}}
                maxTokens={4096}
                onChange={onChange}
            />
        </IntlProvider>,
    );
}

const optionValues = (select: HTMLSelectElement) => Array.from(select.options).map((o) => o.value);

describe('ReasoningConfigItem Anthropic effort', () => {
    it.each([
        {name: 'unset effort defaults to high', reasoningEffort: '', expected: 'high'},
        {name: 'stored effort is shown', reasoningEffort: 'medium', expected: 'medium'},
        {name: 'minimal is shown as low', reasoningEffort: 'minimal', expected: 'low'},
    ])('$name', ({reasoningEffort, expected}) => {
        renderItem({reasoningEffort});

        const select = screen.getByRole('combobox') as HTMLSelectElement;
        expect(select.value).toBe(expected);
        expect(optionValues(select)).toEqual(['low', 'medium', 'high']);
        expect(screen.queryByRole('spinbutton')).toBeNull();
    });

    it('saves the selected effort', () => {
        const onChange = jest.fn();
        renderItem({reasoningEffort: 'high'}, {}, onChange);

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'low'}});

        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({reasoningEffort: 'low'}));
    });
});

describe('defaultReasoningEffort', () => {
    it.each([
        ['anthropic', 'high'],
        ['gemini', 'medium'],
        ['openai', 'medium'],
        ['', 'medium'],
    ])('%s -> %s', (serviceType, expected) => {
        expect(defaultReasoningEffort(serviceType)).toBe(expected);
    });
});

describe('ReasoningConfigItem Gemini', () => {
    it('keeps the optional thinking budget alongside effort', () => {
        renderItem({reasoningEffort: 'low'}, {type: 'gemini', defaultModel: 'gemini-2.5-pro'});

        expect(screen.getByRole('spinbutton')).toBeTruthy();
        const select = screen.getByRole('combobox') as HTMLSelectElement;
        expect(select.value).toBe('low');
        expect(optionValues(select)).toEqual(['minimal', 'low', 'medium', 'high']);
    });
});

describe('ReasoningConfigItem Cohere North', () => {
    it('shows effort-based reasoning even when useResponsesAPI is false', () => {
        renderItem(
            {reasoningEnabled: true, reasoningEffort: 'medium'},
            {type: 'north', useResponsesAPI: false, defaultModel: ''},
        );

        expect(screen.getByText('Reasoning')).toBeTruthy();
        expect(screen.getByText('Reasoning Effort')).toBeTruthy();
        expect(screen.queryByText('Thinking Budget (tokens)')).toBeNull();
        expect(screen.queryByText('Native OpenAI Tools')).toBeNull();
    });
});
