// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {SCROLLED_UP_THRESHOLD_PX, useThreadScroll} from './use_thread_scroll';

const Probe = () => {
    const ref = useRef<HTMLDivElement>(null);
    const {scrolledUp, scrollToBottom} = useThreadScroll(ref);
    return (
        <div ref={ref}>
            <div
                id='threadViewerScrollContainer'
                data-testid='scroller'
            />
            <span data-testid='state'>{scrolledUp ? 'up' : 'bottom'}</span>
            <button
                type='button'
                onClick={scrollToBottom}
            >{'jump'}</button>
        </div>
    );
};

const scrollerMetrics = (scroller: HTMLElement, {scrollHeight, clientHeight, scrollTop}: {scrollHeight: number; clientHeight: number; scrollTop: number}) => {
    Object.defineProperty(scroller, 'scrollHeight', {configurable: true, value: scrollHeight});
    Object.defineProperty(scroller, 'clientHeight', {configurable: true, value: clientHeight});
    Object.defineProperty(scroller, 'scrollTop', {configurable: true, writable: true, value: scrollTop});
};

describe('useThreadScroll', () => {
    test.each([
        {name: 'at the bottom', scrollTop: 600, expected: 'bottom'},
        {name: 'within the threshold of the bottom', scrollTop: 600 - SCROLLED_UP_THRESHOLD_PX, expected: 'bottom'},
        {name: 'past the threshold', scrollTop: 600 - SCROLLED_UP_THRESHOLD_PX - 1, expected: 'up'},
        {name: 'at the top', scrollTop: 0, expected: 'up'},
    ])('$name is $expected', ({scrollTop, expected}) => {
        render(<Probe/>);
        const scroller = screen.getByTestId('scroller');
        scrollerMetrics(scroller, {scrollHeight: 1000, clientHeight: 400, scrollTop});

        act(() => {
            fireEvent.scroll(scroller);
        });

        expect(screen.getByTestId('state').textContent).toBe(expected);
    });

    test('scrolling back down clears it', () => {
        render(<Probe/>);
        const scroller = screen.getByTestId('scroller');
        scrollerMetrics(scroller, {scrollHeight: 1000, clientHeight: 400, scrollTop: 0});
        act(() => {
            fireEvent.scroll(scroller);
        });
        expect(screen.getByTestId('state').textContent).toBe('up');

        scroller.scrollTop = 600;
        act(() => {
            fireEvent.scroll(scroller);
        });
        expect(screen.getByTestId('state').textContent).toBe('bottom');
    });

    test('jump scrolls the container to its bottom', () => {
        render(<Probe/>);
        const scroller = screen.getByTestId('scroller');
        scrollerMetrics(scroller, {scrollHeight: 1000, clientHeight: 400, scrollTop: 0});
        const scrollTo = jest.fn();
        scroller.scrollTo = scrollTo;

        fireEvent.click(screen.getByRole('button', {name: 'jump'}));

        expect(scrollTo).toHaveBeenCalledWith({top: 1000, behavior: 'smooth'});
    });

    describe('with a ResizeObserver', () => {
        const original = global.ResizeObserver;
        let observed: Array<{target: Element; callback: () => void}> = [];

        beforeEach(() => {
            observed = [];
            global.ResizeObserver = class {
                callback: () => void;
                constructor(callback: () => void) {
                    this.callback = callback;
                }
                observe(target: Element) {
                    observed.push({target, callback: this.callback});
                }
                disconnect() {
                    observed = observed.filter((o) => o.callback !== this.callback);
                }
            } as unknown as typeof ResizeObserver;
        });

        afterEach(() => {
            global.ResizeObserver = original;
        });

        const resize = (target: Element) => observed.filter((o) => o.target === target).forEach((o) => o.callback());

        test('a row added after mount still updates the state when it resizes', async () => {
            render(<Probe/>);
            const scroller = screen.getByTestId('scroller');
            scrollerMetrics(scroller, {scrollHeight: 400, clientHeight: 400, scrollTop: 0});
            const row = document.createElement('div');
            await act(async () => {
                scroller.appendChild(row);
                await Promise.resolve();
            });

            scrollerMetrics(scroller, {scrollHeight: 1000, clientHeight: 400, scrollTop: 0});
            act(() => resize(row));

            expect(screen.getByTestId('state').textContent).toBe('up');
        });
    });
});
