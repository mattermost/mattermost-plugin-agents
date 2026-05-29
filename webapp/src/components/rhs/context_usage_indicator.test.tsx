// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, fireEvent} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import type {Composition} from '@/types/conversation';

import ContextUsageIndicator, {testOnly} from './context_usage_indicator';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, unknown>}) => {
            if (!values) {
                return defaultMessage;
            }
            return defaultMessage.replace(/\{(\w+)\}/g, (_, k: string) => String(values[k] ?? ''));
        },
    };
});

let mockComposition: Composition | null = null;
jest.mock('@/hooks/use_conversation_context', () => ({
    useConversationContext: () => ({composition: mockComposition, loading: false, error: null}),
}));

function renderIndicator() {
    return render(
        <IntlProvider locale='en'>
            <ContextUsageIndicator conversationId='conv_1'/>
        </IntlProvider>,
    );
}

beforeEach(() => {
    mockComposition = null;
});

describe('ContextUsageIndicator visibility', () => {
    test('renders nothing when there is no composition', () => {
        mockComposition = null;
        const {container} = renderIndicator();
        expect(container.firstChild).toBeNull();
    });

    test('renders absolute count without a ring when no input_token_limit', () => {
        // Production hit this for OpenAI threads: ListModels doesn't
        // publish MaxInputTokens via Bifrost, so input_token_limit is 0.
        // Hiding the indicator entirely is worse than showing the raw
        // count — users still want to know "this thread is at 62k" even
        // without a denominator.
        mockComposition = {
            components: [{source: 'history', proportion: 1, tokens: 62000}],
            total: 62000,
            total_source: 'estimated',
            input_token_limit: 0,
        };
        const {container} = renderIndicator();
        expect(container.firstChild).not.toBeNull();
        expect(container.textContent).toContain('62k');
        expect(container.textContent).not.toContain('%');
    });

    test('renders nothing when total is zero regardless of limit', () => {
        mockComposition = {
            components: [],
            total: 0,
            total_source: 'estimated',
            input_token_limit: 200000,
        };
        const {container} = renderIndicator();
        expect(container.firstChild).toBeNull();
    });

    test('renders nothing when utilization is below the hide threshold', () => {
        mockComposition = {
            components: [{source: 'system', proportion: 1, tokens: 100}],
            total: 100,
            total_source: 'provider',
            input_token_limit: 200000, // 0.05% — well under HideBelow
        };
        const {container} = renderIndicator();
        expect(container.firstChild).toBeNull();
    });

    test('renders the indicator with rounded percentage above the threshold', () => {
        mockComposition = {
            components: [{source: 'system', proportion: 1, tokens: 47000}],
            total: 47000,
            total_source: 'provider',
            input_token_limit: 200000,
        };
        const {container} = renderIndicator();

        // 47000/200000 = 23.5%, rounds to 24%
        expect(container.textContent).toContain('24%');
    });

    test('opening the popover does not crash when components is null', () => {
        // Go marshals a nil slice to JSON null, so components can arrive null
        // with a positive total. Opening the breakdown must not throw.
        mockComposition = {
            components: null as unknown as Composition['components'],
            total: 47000,
            total_source: 'counted',
            input_token_limit: 200000,
        };
        const {container, getByTestId} = renderIndicator();
        fireEvent.click(getByTestId('context-usage-indicator'));
        expect(container.textContent).toContain('Context window');
    });
});

describe('ContextUsageIndicator overflow', () => {
    test('shows the overflow icon when utilization is over 100%', () => {
        // Past the limit, the wrapper's TruncationWrapper starts dropping
        // older messages. A literal "150%" alone reads like the meter is
        // broken; the alert icon tells the user something different is
        // happening.
        mockComposition = {
            components: [{source: 'history', proportion: 1, tokens: 300000}],
            total: 300000,
            total_source: 'provider',
            input_token_limit: 200000,
        };
        const {container} = renderIndicator();
        expect(container.querySelector('[data-testid="context-usage-overflow"]')).not.toBeNull();
    });

    test('hides the overflow icon at or below 100%', () => {
        mockComposition = {
            components: [{source: 'history', proportion: 1, tokens: 150000}],
            total: 150000,
            total_source: 'provider',
            input_token_limit: 200000,
        };
        const {container} = renderIndicator();
        expect(container.querySelector('[data-testid="context-usage-overflow"]')).toBeNull();
    });
});

describe('ringColor thresholds', () => {
    const {ringColor, WarnAt, CritAt} = testOnly;

    test('neutral below warn threshold', () => {
        expect(ringColor(WarnAt - 0.01)).toMatch(/center-channel-color/);
    });
    test('amber at warn threshold', () => {
        expect(ringColor(WarnAt)).toBe('var(--away-indicator)');
        expect(ringColor(CritAt - 0.01)).toBe('var(--away-indicator)');
    });
    test('red at crit threshold', () => {
        expect(ringColor(CritAt)).toBe('var(--dnd-indicator)');
        expect(ringColor(1.1)).toBe('var(--dnd-indicator)');
    });
});

describe('formatTokens', () => {
    const {formatTokens} = testOnly;

    test('renders short numbers as-is', () => {
        expect(formatTokens(0)).toBe('0');
        expect(formatTokens(123)).toBe('123');
        expect(formatTokens(999)).toBe('999');
    });
    test('uses one-decimal k for 1k-9.9k', () => {
        expect(formatTokens(1000)).toBe('1.0k');
        expect(formatTokens(1234)).toBe('1.2k');
        expect(formatTokens(9999)).toBe('10.0k');
    });
    test('uses whole k for 10k+', () => {
        expect(formatTokens(10000)).toBe('10k');
        expect(formatTokens(47123)).toBe('47k');
    });
    test('uses M for million+', () => {
        expect(formatTokens(1_000_000)).toBe('1.0M');
        expect(formatTokens(1_500_000)).toBe('1.5M');
    });
});
