// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

jest.mock('react-intl', () => {
    const ReactLocal = jest.requireActual('react') as typeof React;

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactLocal.createElement(ReactLocal.Fragment, null, children),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, unknown>) => {
                return defaultMessage.replace(/\{(\w+)\}/g, (match, key) => String(values?.[key] ?? match));
            },
        }),
    };
});

import {THINKING_DELAY_MS, THINKING_FADE_MS, THINKING_MIN_VISIBLE_MS, ThinkingDots} from './thinking_dots';

function setup(active: boolean, agentName?: string) { // eslint-disable-line no-undefined
    const ui = (isActive: boolean) => (
        <IntlProvider locale='en'>
            <ThinkingDots
                active={isActive}
                agentName={agentName}
            />
        </IntlProvider>
    );
    const view = render(ui(active));
    return {setActive: (isActive: boolean) => view.rerender(ui(isActive)), view};
}

const dots = () => screen.queryByTestId('llm-bot-thinking');
const advance = (ms: number) => act(() => {
    jest.advanceTimersByTime(ms);
});

beforeEach(() => {
    jest.useFakeTimers();
});

afterEach(() => {
    jest.useRealTimers();
    delete (window as {matchMedia?: unknown}).matchMedia;
});

describe('ThinkingDots timing', () => {
    test.each([
        {name: 'not shown just before the delay', elapsed: THINKING_DELAY_MS - 1, shown: false},
        {name: 'shown once the delay passes', elapsed: THINKING_DELAY_MS, shown: true},
    ])('$name', ({elapsed, shown}) => {
        setup(true);
        advance(elapsed);
        expect(dots() !== null).toBe(shown);
    });

    test('never shown when generation ends before the delay', () => {
        const {setActive} = setup(true);
        advance(THINKING_DELAY_MS - 10);
        setActive(false);
        advance(THINKING_DELAY_MS * 4);
        expect(dots()).toBeNull();
    });

    test('never shown while inactive', () => {
        setup(false);
        advance(THINKING_DELAY_MS * 4);
        expect(dots()).toBeNull();
    });

    test.each([
        {name: 'leaves right away once past the minimum', extra: THINKING_MIN_VISIBLE_MS, leavingAfter: 0},
        {name: 'is held to the minimum on-screen time', extra: 100, leavingAfter: THINKING_MIN_VISIBLE_MS - 100},
    ])('$name', ({extra, leavingAfter}) => {
        const {setActive} = setup(true);
        advance(THINKING_DELAY_MS);
        advance(extra);
        setActive(false);
        if (leavingAfter > 0) {
            advance(leavingAfter - 1);
            expect(dots()?.getAttribute('data-phase')).toBe('visible');
            advance(1);
        } else {
            advance(0);
        }
        expect(dots()?.getAttribute('data-phase')).toBe('leaving');
        advance(THINKING_FADE_MS);
        expect(dots()).toBeNull();
    });

    test('regenerate shows the dots again for the next generation', () => {
        const {setActive} = setup(true);
        advance(THINKING_DELAY_MS);
        setActive(false);
        advance(THINKING_MIN_VISIBLE_MS + THINKING_FADE_MS);
        expect(dots()).toBeNull();
        setActive(true);
        advance(THINKING_DELAY_MS);
        expect(dots()?.getAttribute('data-phase')).toBe('visible');
    });

    test('becoming active again while leaving cancels the fade-out', () => {
        const {setActive} = setup(true);
        advance(THINKING_DELAY_MS + THINKING_MIN_VISIBLE_MS);
        setActive(false);
        advance(0);
        expect(dots()?.getAttribute('data-phase')).toBe('leaving');
        setActive(true);
        advance(THINKING_FADE_MS * 2);
        expect(dots()?.getAttribute('data-phase')).toBe('visible');
    });

    test('unmounting clears pending timers', () => {
        const {view} = setup(true);
        view.unmount();
        expect(jest.getTimerCount()).toBe(0);
    });
});

describe('ThinkingDots announcement', () => {
    test.each([
        {name: 'names the agent', agentName: 'Claude', text: 'Claude is thinking'},
        {name: 'falls back without a name', agentName: undefined, text: 'The agent is thinking'}, // eslint-disable-line no-undefined
    ])('$name', ({agentName, text}) => {
        setup(true, agentName);
        expect(screen.getByRole('status').textContent).toBe('');
        advance(THINKING_DELAY_MS);
        expect(screen.getByRole('status').textContent).toBe(text);
    });

    test('is not repeated by re-renders and clears when the dots leave', () => {
        const {setActive} = setup(true, 'Claude');
        advance(THINKING_DELAY_MS);
        const status = screen.getByRole('status');
        const before = status.textContent;
        setActive(true);
        advance(500);
        expect(screen.getByRole('status')).toBe(status);
        expect(status.textContent).toBe(before);
        setActive(false);
        advance(THINKING_MIN_VISIBLE_MS);
        expect(status.textContent).toBe('');
    });

    test('the dots themselves are hidden from assistive tech', () => {
        setup(true);
        advance(THINKING_DELAY_MS);
        expect(dots()?.getAttribute('aria-hidden')).toBe('true');
    });
});

describe('ThinkingDots reduced motion', () => {
    test.each([
        {name: 'full motion', reduce: false, motion: 'full'},
        {name: 'reduced motion', reduce: true, motion: 'reduced'},
    ])('$name', ({reduce, motion}) => {
        window.matchMedia = jest.fn().mockReturnValue({matches: reduce}) as unknown as typeof window.matchMedia;
        setup(true);
        advance(THINKING_DELAY_MS);
        const element = dots();
        expect(element?.getAttribute('data-motion')).toBe(motion);

        // The wave animation is only attached when motion is allowed.
        const animated = Array.from(element?.children ?? []).map((dot) => dot.className);
        expect(new Set(animated).size).toBe(1);
        const sheet = Array.from(document.querySelectorAll('style')).map((s) => s.textContent).join('');
        const dotRule = new RegExp(`\\.${animated[0].split(' ').pop()}\\{[^}]*animation:[^;}]*infinite`);
        expect(dotRule.test(sheet.replace(/\s+/g, ''))).toBe(!reduce);
    });
});
