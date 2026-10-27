// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {act, render} from '@testing-library/react';

import {
    BubbleFitState,
    BubbleMetrics,
    HUG_MAX_FRACTION,
    decideBubbleFit,
    measureBubble,
    nextBubbleFit,
    useBubbleWidth,
} from './use_bubble_width';

const AVAILABLE = 800;
const CAP = HUG_MAX_FRACTION * AVAILABLE;
const PADDING = 32;

function metrics(overrides: Partial<BubbleMetrics> = {}): BubbleMetrics {
    return {available: AVAILABLE, content: 100, header: 80, padding: PADDING, width: 200, wide: false, ...overrides};
}

describe('decideBubbleFit', () => {
    test.each([
        {name: 'short content hugs', m: metrics(), current: null, expected: 'hug'},
        {name: 'long content spans the row', m: metrics({content: 700}), current: null, expected: 'full'},
        {name: 'just under the threshold hugs', m: metrics({content: CAP - PADDING - 1}), current: null, expected: 'hug'},
        {name: 'exactly at the threshold spans the row', m: metrics({content: CAP - PADDING}), current: null, expected: 'full'},
        {name: 'a header wider than the content decides', m: metrics({content: 50, header: CAP}), current: null, expected: 'full'},
        {name: 'a short reply under a short header hugs the header', m: metrics({content: 10, header: 120}), current: null, expected: 'hug'},
        {name: 'wide content (code, table, tools, media) spans the row', m: metrics({wide: true}), current: null, expected: 'full'},
        {name: 'a hugging bubble pinned at its cap spans the row', m: metrics({content: 500, width: CAP}), current: 'hug', expected: 'full'},
        {name: 'no measurable row spans the row', m: metrics({available: 0}), current: null, expected: 'full'},
        {name: 'a full bubble just under the cap stays full', m: metrics({content: CAP - PADDING - 2, width: AVAILABLE}), current: 'full', expected: 'full'},
        {name: 'a full bubble well under the cap hugs', m: metrics({content: CAP - PADDING - 20, width: AVAILABLE}), current: 'full', expected: 'hug'},
    ] as const)('$name', ({m, current, expected}) => {
        expect(decideBubbleFit(m, current)).toBe(expected);
    });
});

describe('nextBubbleFit', () => {
    const hugAt = (minWidth: number): BubbleFitState => ({fit: 'hug', minWidth});

    test.each([
        {name: 'streaming: a hugging bubble ratchets its min width up', prev: hugAt(0), m: metrics({content: 101}), streaming: true, expected: {fit: 'hug', minWidth: 104}},
        {name: 'streaming: a reflow never shrinks it', prev: hugAt(240), m: metrics({content: 60}), streaming: true, expected: {fit: 'hug', minWidth: 240}},
        {name: 'streaming: crossing the threshold goes full', prev: hugAt(240), m: metrics({content: 700}), streaming: true, expected: {fit: 'full', minWidth: 0}},
        {name: 'streaming: once full it never flips back', prev: {fit: 'full', minWidth: 0}, m: metrics({content: 20}), streaming: true, expected: {fit: 'full', minWidth: 0}},
        {name: 'complete: recomputed from the final content', prev: {fit: 'full', minWidth: 0}, m: metrics({content: 20}), streaming: false, expected: {fit: 'hug', minWidth: 0}},
        {name: 'complete: the ratchet is dropped', prev: hugAt(240), m: metrics({content: 60}), streaming: false, expected: {fit: 'hug', minWidth: 0}},
    ] as const)('$name', ({prev, m, streaming, expected}) => {
        expect(nextBubbleFit(prev, m, streaming)).toEqual(expected);
    });
});

describe('useBubbleWidth', () => {
    let observers: Array<() => void> = [];
    let current: BubbleMetrics = metrics();
    const measure = jest.fn(() => current);

    beforeEach(() => {
        observers = [];
        current = metrics();
        measure.mockClear();
        (window as any).ResizeObserver = class {
            private callback: () => void;
            constructor(callback: () => void) {
                this.callback = callback;
                observers.push(() => this.callback());
            }
            observe = jest.fn();
            disconnect = jest.fn();
        };
    });

    afterEach(() => {
        delete (window as any).ResizeObserver;
    });

    const Probe = ({enabled, streaming}: {enabled: boolean; streaming: boolean}) => {
        const ref = useRef<HTMLDivElement>(null);
        const bubble = useBubbleWidth(ref, {enabled, streaming, measure});
        return (
            <div
                ref={ref}
                data-testid='probe'
                data-bubble-fit={bubble.fit ?? undefined} // eslint-disable-line no-undefined
                data-min-width={bubble.minWidth}
            />
        );
    };

    // Host markup: the bubble is the avatar slot's sibling in .post__content.
    function renderProbe(props: {enabled: boolean; streaming: boolean}) {
        const host = (p: {enabled: boolean; streaming: boolean}) => (
            <div className='post__content'>
                <div className='post__img'/>
                <div>
                    <Probe {...p}/>
                </div>
            </div>
        );
        const result = render(host(props));
        const probe = () => result.getByTestId('probe');
        return {
            ...result,
            probe,
            fit: () => probe().getAttribute('data-bubble-fit'),
            minWidth: () => Number(probe().getAttribute('data-min-width')),
            update: (p: {enabled: boolean; streaming: boolean}) => result.rerender(host(p)),
            resize: (m: Partial<BubbleMetrics>) => {
                current = metrics(m);
                act(() => observers.forEach((notify) => notify()));
            },
        };
    }

    test('a historic post decides once on mount', () => {
        const t = renderProbe({enabled: true, streaming: false});
        expect(t.fit()).toBe('hug');
        const calls = measure.mock.calls.length;
        t.update({enabled: true, streaming: false});
        expect(measure.mock.calls.length).toBe(calls);
    });

    test('off: no decision, no measurement', () => {
        const t = renderProbe({enabled: false, streaming: true});
        expect(t.fit()).toBeNull();
        expect(measure).not.toHaveBeenCalled();
    });

    test('streaming: ratchets, goes full once, then recomputes on completion', () => {
        const t = renderProbe({enabled: true, streaming: true});
        expect(t.fit()).toBe('hug');

        t.resize({content: 200});
        expect(t.minWidth()).toBe(200);
        t.resize({content: 120});
        expect(t.fit()).toBe('hug');
        expect(t.minWidth()).toBe(200);

        t.resize({content: 700});
        expect(t.fit()).toBe('full');
        t.resize({content: 40});
        expect(t.fit()).toBe('full');

        t.update({enabled: true, streaming: false});
        expect(t.fit()).toBe('hug');
        expect(t.minWidth()).toBe(0);
    });

    // Live, a hugging bubble read as pinned at its cap (it was mid-transition,
    // or its natural width sat right at the cap) went full, then hugged again
    // on the next measurement, and so on every frame.
    test('content at the cap settles at full instead of flipping', () => {
        const t = renderProbe({enabled: true, streaming: false});
        expect(t.fit()).toBe('hug');
        t.resize({content: CAP - PADDING - 0.5, width: CAP - 0.5});
        expect(t.fit()).toBe('full');
        t.resize({content: CAP - PADDING - 0.5, width: AVAILABLE});
        expect(t.fit()).toBe('full');
        t.resize({content: 100, width: AVAILABLE});
        expect(t.fit()).toBe('hug');
    });

    test('outside a host bubble nothing is decided', () => {
        const Bare = () => {
            const ref = useRef<HTMLDivElement>(null);
            const bubble = useBubbleWidth(ref, {enabled: true, streaming: false, measure});
            return (
                <div
                    ref={ref}
                    data-testid='bare'
                    data-bubble-fit={bubble.fit ?? undefined} // eslint-disable-line no-undefined
                />
            );
        };
        const {getByTestId} = render(<Bare/>);
        expect(getByTestId('bare').getAttribute('data-bubble-fit')).toBeNull();
        expect(measure).not.toHaveBeenCalled();
    });
});

describe('measureBubble wide content', () => {
    test.each([
        {name: 'plain text', inner: '<p>Hi there</p>', host: '', wide: false},
        {name: 'a code block', inner: '<pre><code>x</code></pre>', host: '', wide: true},
        {name: 'a table', inner: '<table><tbody><tr><td>x</td></tr></tbody></table>', host: '', wide: true},
        {name: 'reasoning or tool UI in a round', inner: '<div data-bubble-wide="true">x</div>', host: '', wide: true},
        {name: 'a host-formatted code block', inner: '<div class="post-code"><code>x</code></div>', host: '', wide: true},
        {name: 'a collapsed tool activity line', inner: '<div data-testid="llm-bot-tool-activity"><button>Thinking</button></div><p>Sure.</p>', host: '', wide: false},
        {name: 'the expanded tool activity panel', inner: '<div data-testid="llm-bot-tool-activity"><div data-testid="llm-bot-tool-activity-rounds">x</div></div>', host: '', wide: true},
        {name: 'a host file attachment in the bubble', inner: '<p>Hi</p>', host: '<div class="post-image__columns"></div>', wide: true},
    ])('$name', ({inner, host, wide}) => {
        const row = document.createElement('div');
        row.className = 'post__content';
        row.innerHTML = `<div class="post__img"></div><div><div class="post__header"><span>Agent</span></div><div id="wrapper">${inner}</div>${host}</div>`;
        document.body.appendChild(row);
        const wrapper = row.querySelector<HTMLElement>('#wrapper')!;
        const bubble = wrapper.parentElement!;
        expect(measureBubble(wrapper, bubble)?.wide).toBe(wide);
        row.remove();
    });
});

describe('measureBubble header width', () => {
    const rect = (right: number) => ({left: 0, right, width: right, top: 0, bottom: 0, height: 0, x: 0, y: 0, toJSON: () => ({})});
    const proto = Range.prototype as unknown as {getBoundingClientRect?: () => unknown};
    const original = proto.getBoundingClientRect;

    beforeEach(() => {
        // jsdom has no layout: give each text range a width of 8px per character.
        proto.getBoundingClientRect = function eightPxPerCharacter(this: Range) {
            return rect(this.toString().length * 8);
        };
    });

    afterEach(() => {
        proto.getBoundingClientRect = original;
    });

    // The host's badges wrapper is a flex item that can stretch to the row's right edge; the
    // header's width must come from its text, not from that box.
    test('uses the header text, not a stretched child box, and ignores the hover toolbar', () => {
        const row = document.createElement('div');
        row.className = 'post__content';
        row.innerHTML = '<div class="post__img"></div><div>' +
            '<div class="post__header"><span>Agent</span><div class="badges-wrapper"><span>BOT</span></div><div class="post-menu">a very long toolbar tooltip label</div></div>' +
            '<div id="wrapper"><p>Hi</p></div></div>';
        document.body.appendChild(row);
        const wrapper = row.querySelector<HTMLElement>('#wrapper')!;
        const bubble = wrapper.parentElement!;
        row.querySelector<HTMLElement>('.badges-wrapper')!.getBoundingClientRect = () => rect(1000) as DOMRect;

        const metrics = measureBubble(wrapper, bubble);

        expect(metrics?.header).toBe('Agent'.length * 8);
        row.remove();
    });
});
