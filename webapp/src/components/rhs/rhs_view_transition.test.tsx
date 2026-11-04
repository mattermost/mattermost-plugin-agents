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
        {name: 'a chat to history slides back', from: ['thread:a', 1], to: ['threads', 0], direction: 'back'},
        {name: 'the new chat to its first chat cross-fades', from: ['new', 1], to: ['thread:a', 1], direction: 'none'},
    ] as const)('$name', ({from, to, direction}) => {
        const {rerender} = renderTransition(from[0], from[1]);

        rerenderTo(rerender, to[0], to[1]);

        expect(exiting()?.getAttribute('data-direction')).toBe(direction);

        // The new view mounts and waits where its slide starts; two frames later the slide runs.
        const entering = () => screen.getByLabelText(`${to[0]} draft`).closest('[data-state]');
        expect(entering()?.getAttribute('data-state')).toBe('enter-wait');
        expect(exiting()?.getAttribute('data-state')).toBe('exit-wait');
        act(() => {
            jest.advanceTimersByTime(40);
        });
        expect(entering()?.getAttribute('data-state')).toBe('enter');
        expect(exiting()?.getAttribute('data-state')).toBe('exit');
    });

    test('going forward the new view slides in empty and its content mounts when the slide is over', () => {
        const {rerender} = renderTransition('threads', 0);

        rerenderTo(rerender, 'thread:a', 1);

        // The heavy view is not mounted while it moves; the old one is still there.
        expect(exiting()?.getAttribute('data-direction')).toBe('forward');
        expect(screen.queryByLabelText('thread:a draft')).toBeNull();
        expect(screen.getByLabelText('threads draft')).toBeTruthy();

        act(() => {
            jest.advanceTimersByTime(50);
        });
        expect(screen.queryByLabelText('thread:a draft')).toBeNull();

        act(() => {
            jest.advanceTimersByTime(400);
        });

        // Slide over: the content mounted (to fade in) and the old view is gone.
        const content = screen.getByLabelText('thread:a draft');
        expect(content.closest('[data-state]')?.getAttribute('data-state')).toBe('idle');
        expect(content.closest('[data-reveal]')?.getAttribute('data-reveal')).toBe('fade');
        expect(exiting()).toBeNull();
    });

    test('the outgoing view stays mounted with its state, inert, and goes when the exit is over', () => {
        const {rerender} = renderTransition('thread:a', 1);
        fireEvent.change(screen.getByLabelText('thread:a draft'), {target: {value: 'half typed'}});

        rerenderTo(rerender, 'threads', 0);

        // Still there, as the user left it, and out of the way of the new view.
        expect((screen.getByLabelText('thread:a draft') as HTMLInputElement).value).toBe('half typed');
        expect(exiting()?.getAttribute('aria-hidden')).toBe('true');
        expect(exiting()?.hasAttribute('inert')).toBe(true);
        expect(screen.getByLabelText('threads draft')).toBeTruthy();

        // The slide starts two frames in, and the exit unmounts after it.
        act(() => {
            jest.advanceTimersByTime(50);
        });
        act(() => {
            jest.advanceTimersByTime(400);
        });

        expect(exiting()).toBeNull();
        expect(screen.queryByLabelText('thread:a draft')).toBeNull();
        expect(screen.getByLabelText('threads draft').closest('[data-state]')?.getAttribute('data-state')).toBe('idle');
    });

    test('a second change during an exit replaces the outgoing view', () => {
        const {rerender} = renderTransition('thread:a', 1);
        rerenderTo(rerender, 'threads', 0);
        rerenderTo(rerender, 'thread:b', 1);

        expect(exiting()?.getAttribute('data-direction')).toBe('forward');

        // Only the view that was showing is outgoing (the one before it is gone), and the new,
        // heavy one is not mounted yet.
        expect(screen.getAllByLabelText(/draft/).length).toBe(1);
        expect(screen.getByLabelText('threads draft')).toBeTruthy();

        // The slide starts two frames in, and the exit unmounts after it.
        act(() => {
            jest.advanceTimersByTime(50);
        });
        act(() => {
            jest.advanceTimersByTime(400);
        });

        expect(screen.getAllByLabelText(/draft/).length).toBe(1);
        expect(screen.getByLabelText('thread:b draft')).toBeTruthy();
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
