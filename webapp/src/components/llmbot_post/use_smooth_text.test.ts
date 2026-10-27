// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';

import {
    BACKLOG_DRAIN_MS,
    FLUSH_MAX_MS,
    FRESH_MAX_AGE_MS,
    FrameScheduler,
    MAX_REVEAL_LAG_MS,
    MIN_REVEAL_CHARS_PER_SECOND,
    SmoothTextOptions,
    revealBoundary,
    useSmoothText,
} from './use_smooth_text';

const FRAME_MS = 16;

// A deterministic frame clock: frames run only when the test advances time.
function makeFrames() {
    let time = 1000;
    let nextHandle = 1;
    let queue: Map<number, () => void> = new Map();
    const scheduler: FrameScheduler = {
        request: (callback) => {
            const handle = nextHandle++;
            queue.set(handle, callback);
            return handle;
        },
        cancel: (handle) => {
            queue.delete(handle);
        },
    };
    const advance = (ms: number) => {
        const end = time + ms;
        while (time < end) {
            time = Math.min(end, time + FRAME_MS);
            const callbacks = Array.from(queue.values());
            queue = new Map();
            act(() => callbacks.forEach((callback) => callback()));
        }
    };
    return {scheduler, now: () => time, advance, pending: () => queue.size};
}

interface Props {
    text: string;
    streaming: boolean;
    enabled?: boolean;
    flushImmediately?: boolean;
}

function setup(initial: Props) {
    const frames = makeFrames();
    const hook = renderHook((props: Props) => {
        const options: SmoothTextOptions = {
            enabled: props.enabled ?? true,
            streaming: props.streaming,
            flushImmediately: props.flushImmediately ?? false,
            scheduler: frames.scheduler,
            now: frames.now,
        };
        return useSmoothText(props.text, options);
    }, {initialProps: initial});
    let current = initial;
    const update = (props: Partial<Props>) => {
        current = {...current, ...props};
        hook.rerender(current);
    };
    return {...frames, hook, update, shown: () => hook.result.current.text};
}

// A stream observed from its start: mounted empty, then generating.
function startStream() {
    const t = setup({text: '', streaming: false});
    t.update({streaming: true});
    return t;
}

const words = (count: number) => Array.from({length: count}, (_, i) => `word${i}`).join(' ') + ' ';

describe('revealBoundary', () => {
    test.each([
        {name: 'stops after the last whitespace passed', text: 'hello brave new world', from: 0, cursor: 14, floor: 0, expected: 12},
        {name: 'holds a word that is still being revealed', text: 'hello brave', from: 6, cursor: 9, floor: 0, expected: 6},
        {name: 'reveals everything at the end of the text', text: 'hello', from: 0, cursor: 5, floor: 0, expected: 5},
        {name: 'cuts a very long run mid-word', text: 'x'.repeat(60), from: 0, cursor: 30, floor: 0, expected: 30},
        {name: 'honors the lag floor mid-word', text: 'hello brave', from: 6, cursor: 7, floor: 9, expected: 9},
        {name: 'never splits a surrogate pair', text: 'ab' + '\u{1F600}'.repeat(20), from: 2, cursor: 2 + 25, floor: 0, expected: 2 + 26},
    ])('$name', ({text, from, cursor, floor, expected}) => {
        expect(revealBoundary(text, from, cursor, floor)).toBe(expected);
    });
});

describe('useSmoothText', () => {
    test('a burst drains at the minimum steady rate, word by word', () => {
        const t = startStream();
        const text = 'one two three four five six seven eight nine ten ';
        t.update({text});

        // The first word shows at once (the thinking indicator hands off).
        expect(t.shown()).toBe('one ');

        const samples: number[] = [];
        for (let i = 0; i < 6; i++) {
            t.advance(50);
            samples.push(t.shown().length);
            expect(text.startsWith(t.shown())).toBe(true);
            expect(t.shown() === '' || t.shown().endsWith(' ')).toBe(true);
        }

        // Grows steadily rather than in one jump, and within the catch-up target.
        expect(samples[0]).toBeLessThan(text.length);
        expect(new Set(samples).size).toBeGreaterThan(2);
        t.advance(BACKLOG_DRAIN_MS);
        expect(t.shown()).toBe(text);
    });

    test.each([
        {name: 'a small backlog', size: 4},
        {name: 'a large backlog', size: 200},
        {name: 'a reconnect catch-up', size: 2000},
    ])('$name is fully shown within the catch-up target', ({size}) => {
        const t = startStream();
        const text = words(size);
        t.update({text});
        const minDrain = (text.length / MIN_REVEAL_CHARS_PER_SECOND) * 1000;
        t.advance(Math.min(minDrain, BACKLOG_DRAIN_MS) + (2 * FRAME_MS));
        expect(t.shown()).toBe(text);
    });

    test('a steady stream never lags more than the max lag', () => {
        const t = startStream();
        let text = '';
        const received: Array<{at: number; length: number}> = [];
        for (let i = 0; i < 60; i++) {
            text += words(i % 3 === 0 ? 40 : 2);
            t.update({text});
            received.push({at: t.now(), length: text.length});
            t.advance(100);

            // Everything received more than the max lag ago is visible.
            const due = received.filter((r) => r.at <= t.now() - MAX_REVEAL_LAG_MS).pop();
            expect(t.shown().length).toBeGreaterThanOrEqual(due?.length ?? 0);
        }
        t.advance(MAX_REVEAL_LAG_MS);
        expect(t.shown()).toBe(text);
    });

    test('ending generation flushes the buffer within the flush time', () => {
        const t = startStream();
        const text = words(400);
        t.update({text});
        t.advance(FRAME_MS * 2);
        expect(t.shown().length).toBeLessThan(text.length);
        t.update({streaming: false});
        t.advance(FLUSH_MAX_MS + FRAME_MS);
        expect(t.shown()).toBe(text);
    });

    test.each([
        {name: 'stop or cancel', change: {streaming: false, flushImmediately: true}},
        {name: 'switching the smoothing off', change: {enabled: false}},
    ])('$name shows everything at once', ({change}) => {
        const t = startStream();
        const text = words(300);
        t.update({text});
        expect(t.shown().length).toBeLessThan(text.length);
        t.update(change);
        expect(t.shown()).toBe(text);
    });

    test.each([
        {name: 'a historic post', streaming: false},
        {name: 'a post mounted mid-stream', streaming: true},
    ])('$name renders its current text at once, with nothing fresh', ({streaming}) => {
        const t = setup({text: words(100), streaming});
        expect(t.shown()).toBe(words(100));
        expect(t.hook.result.current.fresh).toHaveLength(0);
        expect(t.pending()).toBe(0);
    });

    test('mid-stream mount: the first update carries all earlier text and shows at once', () => {
        const t = setup({text: '', streaming: false});
        t.update({text: words(100), streaming: true});
        expect(t.shown()).toBe(words(100));

        // Later chunks are smoothed.
        t.update({text: words(100) + words(50)});
        expect(t.shown()).toBe(words(100));
        t.advance(BACKLOG_DRAIN_MS + FRAME_MS);
        expect(t.shown()).toBe(words(100) + words(50));
    });

    test('reduced motion (disabled) shows text exactly as received', () => {
        const t = setup({text: '', streaming: false, enabled: false});
        t.update({streaming: true});
        for (const text of ['Hel', 'Hello wor', 'Hello world, again']) {
            t.update({text});
            expect(t.shown()).toBe(text);
            expect(t.hook.result.current.fresh).toHaveLength(0);
        }
        expect(t.pending()).toBe(0);
    });

    test.each([
        {name: 'a replacement that diverges from the shown prefix', next: 'Completely different answer'},
        {name: 'a reset to empty (new round)', next: ''},
    ])('$name snaps to the new text', ({next}) => {
        const t = startStream();
        t.update({text: 'First draft of the answer that streams '});
        t.advance(FRAME_MS * 4);
        t.update({text: next});
        expect(t.shown()).toBe(next);
        expect(t.hook.result.current.fresh).toHaveLength(0);
    });

    test('a final authoritative text after the stream snaps when it differs', () => {
        const t = startStream();
        t.update({text: 'Streamed prefix '});
        t.update({streaming: false});
        t.update({text: 'Edited final text'});
        expect(t.shown()).toBe('Edited final text');
    });

    test('a new generation starts fresh', () => {
        const t = startStream();
        t.update({text: words(50)});
        t.update({streaming: false, text: ''});
        expect(t.shown()).toBe('');
        t.update({streaming: true});
        t.update({text: 'New '});
        expect(t.shown()).toBe('New ');
    });

    test('fresh ranges cover only newly revealed text and retire', () => {
        const t = startStream();
        t.update({text: 'alpha beta gamma delta '});
        t.advance(FRAME_MS * 6);
        const {fresh, text, retire} = t.hook.result.current;
        expect(fresh.length).toBeGreaterThan(1);

        // Contiguous, in order, inside the shown text.
        expect(fresh[0].start).toBe(0);
        for (let i = 1; i < fresh.length; i++) {
            expect(fresh[i].start).toBe(fresh[i - 1].end);
        }
        expect(fresh[fresh.length - 1].end).toBe(text.length);

        act(() => retire(fresh[0].id));
        expect(t.hook.result.current.fresh.map((range) => range.id)).not.toContain(fresh[0].id);
    });

    test('fresh ranges whose fade never ends are settled after the max age', () => {
        const t = startStream();
        t.update({text: 'alpha beta '});
        t.advance(BACKLOG_DRAIN_MS);
        expect(t.hook.result.current.fresh.length).toBeGreaterThan(0);
        t.advance(FRESH_MAX_AGE_MS + FRAME_MS);
        expect(t.hook.result.current.fresh).toHaveLength(0);
        expect(t.pending()).toBe(0);
    });
});
