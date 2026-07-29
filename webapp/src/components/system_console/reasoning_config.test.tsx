// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ReasoningConfigItem, {usesAdaptiveThinking} from './reasoning_config';
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

function renderItem(bot: Partial<LLMBotConfig>, service: Partial<LLMService> = {}) {
    return render(
        <IntlProvider locale='en'>
            <ReasoningConfigItem
                bot={{...baseBot, ...bot}}
                service={{...baseService, ...service}}
                maxTokens={4096}
                onChange={jest.fn()}
            />
        </IntlProvider>,
    );
}

const budgetTooLargeError = /Thinking budget cannot exceed max tokens/;
const budgetTooSmallError = /Thinking budget must be at least 1024 tokens/;
const adaptiveHelpText = /adaptive thinking/;

describe('usesAdaptiveThinking model classification', () => {
    const cases: Array<[string, boolean]> = [

        // Opus dropped budget-based thinking at 4.7.
        ['claude-opus-4-6', false],
        ['claude-opus-4-7', true],
        ['claude-opus-4-8', true],
        ['claude-opus-4-9', true],
        ['claude-opus-5', true],
        ['claude-opus-6', true],
        ['claude-opus-4-5-20251101', false],
        ['claude-opus-4-1-20250805', false],

        // Date suffix without a minor version is not a minor version.
        ['claude-opus-4-20250514', false],

        // Sonnet dropped budget-based thinking at 5.
        ['claude-sonnet-4-6', false],
        ['claude-sonnet-4-5-20250929', false],
        ['claude-sonnet-5', true],
        ['claude-sonnet-5-1', true],

        // Fable/Mythos are always adaptive-only.
        ['claude-fable-5', true],
        ['claude-mythos-5', true],

        // Unknown families and legacy names stay budget-based.
        ['claude-haiku-4-5-20251001', false],
        ['claude-3-7-sonnet', false],
        ['', false],
    ];

    it.each(cases)('%s -> %s', (model, expected) => {
        expect(usesAdaptiveThinking(model)).toBe(expected);
    });
});

describe('ReasoningConfigItem thinking budget validation', () => {
    const cases = [
        {
            name: 'budget-based model shows out-of-range error',
            model: 'claude-sonnet-4-5-20250929',
            thinkingBudget: 99999,
            expectError: budgetTooLargeError,
            expectAdaptiveHelp: false,
        },
        {
            name: 'budget-based model shows too-small error',
            model: 'claude-haiku-4-5-20251001',
            thinkingBudget: 500,
            expectError: budgetTooSmallError,
            expectAdaptiveHelp: false,
        },
        {
            name: 'adaptive model ignores oversized budget',
            model: 'claude-opus-5',
            thinkingBudget: 99999,
            expectError: null,
            expectAdaptiveHelp: true,
        },
        {
            name: 'adaptive model ignores undersized budget',
            model: 'claude-fable-5',
            thinkingBudget: 500,
            expectError: null,
            expectAdaptiveHelp: true,
        },
        {
            name: 'adaptive detection falls back to the service default model',
            model: '',
            serviceDefaultModel: 'claude-sonnet-5',
            thinkingBudget: 99999,
            expectError: null,
            expectAdaptiveHelp: true,
        },
    ];

    it.each(cases)('$name', ({model, serviceDefaultModel, thinkingBudget, expectError, expectAdaptiveHelp}) => {
        renderItem(
            {model, thinkingBudget},
            serviceDefaultModel ? {defaultModel: serviceDefaultModel} : {},
        );

        if (expectError) {
            expect(screen.getByText(expectError)).toBeTruthy();
        } else {
            expect(screen.queryByText(budgetTooLargeError)).toBeNull();
            expect(screen.queryByText(budgetTooSmallError)).toBeNull();
        }

        if (expectAdaptiveHelp) {
            expect(screen.getByText(adaptiveHelpText)).toBeTruthy();
        } else {
            expect(screen.queryByText(adaptiveHelpText)).toBeNull();
        }
    });
});
