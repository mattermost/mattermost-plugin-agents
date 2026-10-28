// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ToolCard from '../tool_card';
import {ToolCall, ToolCallStatus} from '../tool_types';

import {
    WAIT_TOOL_NAME,
    WaitResolution,
    WaitStatusFooter,
    WaitStepLabel,
    WaitToolContext,
    clampWaitMinutes,
    formatCountdown,
    parseWaitMinutes,
} from './wait_status';

describe('formatCountdown', () => {
    test.each([
        [570000, '9:30'],
        [600000, '10:00'],
        [65000, '1:05'],
        [5000, '0:05'],
        [0, '0:00'],
        [-5000, '0:00'],
    ])('formats %dms as %s', (ms, expected) => {
        expect(formatCountdown(ms)).toBe(expected);
    });
});

describe('clampWaitMinutes', () => {
    test.each([
        [0, 1],
        [1, 1],
        [5, 5],
        [30, 30],
        [90, 30],
        [10.4, 10],
    ])('clamps %d to %d', (input, expected) => {
        expect(clampWaitMinutes(input)).toBe(expected);
    });
});

describe('parseWaitMinutes', () => {
    test('reads and clamps the minutes argument', () => {
        expect(parseWaitMinutes({minutes: 5})).toBe(5);
        expect(parseWaitMinutes({minutes: 90})).toBe(30);
    });

    test('returns null for redacted or malformed arguments', () => {
        expect(parseWaitMinutes(undefined)).toBeNull(); // eslint-disable-line no-undefined
        expect(parseWaitMinutes(null)).toBeNull();
        expect(parseWaitMinutes({})).toBeNull();
        expect(parseWaitMinutes([1, 2])).toBeNull();
        expect(parseWaitMinutes({minutes: 'soon'})).toBeNull();
    });
});

interface FooterOverrides {
    isOwner?: boolean;
    requesterUsername?: string;
    botDisplayName?: string;
    resolution?: WaitResolution;
    resumeAt?: number;
    onCheckNow?: () => void;
    onCancel?: () => void;
}

function renderFooter(overrides: FooterOverrides = {}) {
    return render(
        <IntlProvider locale='en'>
            <WaitStatusFooter
                resumeAt={overrides.resumeAt ?? Date.now() + (9 * 60 * 1000) + (35 * 1000)}
                isOwner={overrides.isOwner ?? false}
                requesterUsername={overrides.requesterUsername}
                botDisplayName={overrides.botDisplayName}
                resolution={overrides.resolution ?? 'pending'}
                busy={false}
                onCheckNow={overrides.onCheckNow ?? jest.fn()}
                onCancel={overrides.onCancel ?? jest.fn()}
            />
        </IntlProvider>,
    );
}

describe('WaitStatusFooter', () => {
    test('owner view shows the bot name, a countdown, and both controls', () => {
        renderFooter({isOwner: true, botDisplayName: 'Matty'});

        const status = screen.getByTestId('llm-bot-wait-status');
        expect(status.getAttribute('data-wait-state')).toBe('pending');
        expect(status.textContent).toContain('Matty will check again in');
        expect(status.textContent).toMatch(/\d+:\d\d/);
        expect(screen.getByTestId('llm-bot-wait-check-now')).not.toBeNull();
        expect(screen.getByTestId('llm-bot-wait-cancel')).not.toBeNull();
    });

    test('owner view falls back to "the assistant" without a bot name', () => {
        renderFooter({isOwner: true});

        expect(screen.getByTestId('llm-bot-wait-status').textContent).toContain('the assistant will check again in');
    });

    test('onlooker view with a known owner shows the username and no controls', () => {
        renderFooter({isOwner: false, requesterUsername: 'alice'});

        const status = screen.getByTestId('llm-bot-wait-status');
        expect(status.textContent).toContain('@alice');
        expect(status.textContent).toMatch(/\d+:\d\d/);
        expect(screen.queryByTestId('llm-bot-wait-check-now')).toBeNull();
        expect(screen.queryByTestId('llm-bot-wait-cancel')).toBeNull();
    });

    test('onlooker fallback shows only the countdown, no username, no controls', () => {
        renderFooter({isOwner: false});

        const status = screen.getByTestId('llm-bot-wait-status');
        expect(status.textContent).toContain('Waiting on async work');
        expect(status.textContent).not.toContain('@');
        expect(status.textContent).not.toContain('will check again');
        expect(screen.queryByTestId('llm-bot-wait-check-now')).toBeNull();
    });

    test('controls are hidden for an onlooker even when a wait is pending', () => {
        renderFooter({isOwner: false, requesterUsername: 'alice'});

        expect(screen.queryByTestId('llm-bot-wait-check-now')).toBeNull();
        expect(screen.queryByTestId('llm-bot-wait-cancel')).toBeNull();
    });

    test('stays pending with controls at 0:00 so a delayed resume can be cancelled', () => {
        renderFooter({isOwner: true, resumeAt: Date.now() - 1000});

        const status = screen.getByTestId('llm-bot-wait-status');
        expect(status.getAttribute('data-wait-state')).toBe('pending');
        expect(status.textContent).toContain('0:00');
        expect(screen.getByTestId('llm-bot-wait-check-now')).not.toBeNull();
        expect(screen.getByTestId('llm-bot-wait-cancel')).not.toBeNull();
    });

    test('shows a cancelled state when the wait was cancelled', () => {
        renderFooter({isOwner: true, resolution: 'cancelled'});

        const status = screen.getByTestId('llm-bot-wait-status');
        expect(status.getAttribute('data-wait-state')).toBe('cancelled');
        expect(status.textContent).toContain('Wait cancelled');
        expect(screen.queryByTestId('llm-bot-wait-check-now')).toBeNull();
    });
});

describe('WaitStepLabel', () => {
    function renderLabel(active: boolean, minutes: number | null) {
        return render(
            <IntlProvider locale='en'>
                <WaitStepLabel
                    active={active}
                    minutes={minutes}
                />
            </IntlProvider>,
        );
    }

    test('shows a pulsing "waiting" label while the wait is active', () => {
        renderLabel(true, 10);

        expect(screen.getByTestId('llm-bot-wait-step-waiting')).not.toBeNull();
        expect(screen.queryByTestId('llm-bot-wait-step-waited')).toBeNull();
    });

    test('shows "waited Nm" once the wait has resolved', () => {
        renderLabel(false, 10);

        const waited = screen.getByTestId('llm-bot-wait-step-waited');
        expect(waited.textContent).toBe('waited 10m');
        expect(screen.queryByTestId('llm-bot-wait-step-waiting')).toBeNull();
    });

    test('renders nothing when resolved with an unknown duration (redacted args)', () => {
        const {container} = renderLabel(false, null);

        expect(container.firstChild).toBeNull();
    });
});

function makeWaitTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'wait_1',
        name: WAIT_TOOL_NAME,
        description: '',
        status: ToolCallStatus.Success,
        arguments: {minutes: 10, reason: 'poll the agent'},
        ...overrides,
    };
}

function renderWaitRow(active: boolean, tool: ToolCall) {
    return render(
        <IntlProvider locale='en'>
            <WaitToolContext.Provider value={{active}}>
                <ToolCard
                    tool={tool}
                    isCollapsed={true}
                    isProcessing={false}
                    onToggleCollapse={jest.fn()}
                    canExpand={false}
                    showArguments={true}
                    showResults={false}
                />
            </WaitToolContext.Provider>
        </IntlProvider>,
    );
}

describe('wait tool row step indicator', () => {
    test('shows the amber clock and "waiting" while the wait is active', () => {
        renderWaitRow(true, makeWaitTool());

        expect(screen.getByTestId('llm-bot-wait-step-clock')).not.toBeNull();
        expect(screen.getByTestId('llm-bot-wait-step-waiting')).not.toBeNull();
    });

    test('shows the status check and "waited Nm" once resolved', () => {
        renderWaitRow(false, makeWaitTool());

        expect(screen.queryByTestId('llm-bot-wait-step-clock')).toBeNull();
        expect(screen.getByTestId('llm-bot-wait-step-waited').textContent).toBe('waited 10m');
    });

    test('a non-wait tool is unaffected by the wait context', () => {
        renderWaitRow(true, makeWaitTool({name: 'create_post', arguments: {}}));

        expect(screen.queryByTestId('llm-bot-wait-step-clock')).toBeNull();
        expect(screen.queryByTestId('llm-bot-wait-step-waiting')).toBeNull();
        expect(screen.queryByTestId('llm-bot-wait-step-waited')).toBeNull();
    });
});
