// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import RHSViewTransition from './rhs_view_transition';

// A view with state of its own, to show the outgoing one is kept, not remounted.
const Draft = ({name}: {name: string}) => {
    const [text, setText] = useState('');
    return (
        <input
            aria-label={`${name} draft`}
            value={text}
            onChange={(e) => setText(e.target.value)}
        />
    );
};

const renderTransition = (viewKey: string, rank: number) => render(
    <RHSViewTransition
        viewKey={viewKey}
        rank={rank}
    >
        <Draft name={viewKey}/>
    </RHSViewTransition>,
);

const rerenderTo = (rerender: (ui: React.ReactElement) => void, viewKey: string, rank: number) => rerender(
    <RHSViewTransition
        viewKey={viewKey}
        rank={rank}
    >
        <Draft name={viewKey}/>
    </RHSViewTransition>,
);

const exiting = () => screen.queryByTestId('rhs-view-exiting');

describe('RHSViewTransition', () => {
    beforeEach(() => {
        jest.useFakeTimers();
        Object.defineProperty(window, 'matchMedia', {writable: true, configurable: true, value: null});
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    test('shows the first view without a transition', () => {
        renderTransition('threads', 0);

        expect(screen.getByLabelText('threads draft')).toBeTruthy();
        expect(exiting()).toBeNull();
    });

    test.each([
        {name: 'history to a chat slides forward', from: ['threads', 0], to: ['thread:a', 1], direction: 'forward'},
        {name: 'a chat to history slides back', from: ['thread:a', 1], to: ['threads', 0], direction: 'back'},
        {name: 'the new chat to its first chat cross-fades', from: ['new', 1], to: ['thread:a', 1], direction: 'none'},
    ] as const)('$name', ({from, to, direction}) => {
        const {rerender} = renderTransition(from[0], from[1]);

        rerenderTo(rerender, to[0], to[1]);

        expect(exiting()?.getAttribute('data-direction')).toBe(direction);
        expect(screen.getByLabelText(`${to[0]} draft`).closest('[data-state]')?.getAttribute('data-state')).toBe('enter');
    });

    test('the outgoing view stays mounted with its state, inert, and goes when the exit is over', () => {
        const {rerender} = renderTransition('threads', 0);
        fireEvent.change(screen.getByLabelText('threads draft'), {target: {value: 'half typed'}});

        rerenderTo(rerender, 'thread:a', 1);

        // Still there, as the user left it, and out of the way of the new view.
        expect((screen.getByLabelText('threads draft') as HTMLInputElement).value).toBe('half typed');
        expect(exiting()?.getAttribute('aria-hidden')).toBe('true');
        expect(exiting()?.hasAttribute('inert')).toBe(true);
        expect(screen.getByLabelText('thread:a draft')).toBeTruthy();

        act(() => {
            jest.advanceTimersByTime(400);
        });

        expect(exiting()).toBeNull();
        expect(screen.queryByLabelText('threads draft')).toBeNull();
        expect(screen.getByLabelText('thread:a draft').closest('[data-state]')?.getAttribute('data-state')).toBe('idle');
    });

    test('a second change during an exit replaces the outgoing view', () => {
        const {rerender} = renderTransition('threads', 0);
        rerenderTo(rerender, 'thread:a', 1);
        rerenderTo(rerender, 'threads', 0);

        expect(exiting()?.getAttribute('data-direction')).toBe('back');
        expect(screen.getAllByLabelText(/draft/).length).toBe(2);

        act(() => {
            jest.advanceTimersByTime(400);
        });

        expect(screen.getAllByLabelText(/draft/).length).toBe(1);
        expect(screen.getByLabelText('threads draft')).toBeTruthy();
    });

    test('with reduced motion the views just swap', () => {
        Object.defineProperty(window, 'matchMedia', {
            writable: true,
            configurable: true,
            value: () => ({matches: true}),
        });
        const {rerender} = renderTransition('threads', 0);

        rerenderTo(rerender, 'thread:a', 1);

        expect(exiting()).toBeNull();
        expect(screen.queryByLabelText('threads draft')).toBeNull();
        expect(screen.getByLabelText('thread:a draft')).toBeTruthy();
    });
});
