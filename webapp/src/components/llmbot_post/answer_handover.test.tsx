// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';

import {useAnswerHandover} from './answer_handover';

function renderHandover(answerText: string, holding: boolean) {
    return renderHook(
        (props: {answerText: string; holding: boolean}) => useAnswerHandover(props.answerText, props.holding),
        {initialProps: {answerText, holding}},
    );
}

/** Longer than either transition, so nothing can still be pending after it. */
function settle() {
    act(() => {
        jest.advanceTimersByTime(1000);
    });
}

describe('useAnswerHandover', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    // A finished post opened in a thread, scrolled back into view or reached
    // by a channel switch mounts with its answer already in place. Nothing
    // moved, so nothing may animate.
    test('signals nothing when it mounts on a settled answer', () => {
        const {result} = renderHandover('Here is the answer', false);

        expect(result.current.foldingText).toBeNull();
        expect(result.current.revealAnswer).toBe(false);
    });

    test('keeps a snapshot of the text that leaves the main area', () => {
        const {result, rerender} = renderHandover('Let me look that up', true);

        rerender({answerText: '', holding: true});
        expect(result.current.foldingText).toBe('Let me look that up');

        settle();
        expect(result.current.foldingText).toBeNull();
    });

    test('signals the entrance when the answer comes back', () => {
        const {result, rerender} = renderHandover('', true);

        rerender({answerText: 'Here is the answer', holding: false});
        expect(result.current.revealAnswer).toBe(true);

        settle();
        expect(result.current.revealAnswer).toBe(false);
    });

    // A response that never called a tool has no row holding its text, so its
    // first chunk is not a handover and must render as it always did.
    test('signals nothing for text arriving with nothing withheld', () => {
        const {result, rerender} = renderHandover('', false);

        rerender({answerText: 'Here is', holding: false});

        expect(result.current.foldingText).toBeNull();
        expect(result.current.revealAnswer).toBe(false);
    });

    // Expanding the area mid-fold puts the real text back; the snapshot has
    // to go with it rather than sit underneath until its timer runs out.
    test('drops the snapshot when the text returns mid-fold', () => {
        const {result, rerender} = renderHandover('Let me look that up', true);
        rerender({answerText: '', holding: true});
        expect(result.current.foldingText).toBe('Let me look that up');

        rerender({answerText: 'Let me look that up', holding: false});

        expect(result.current.foldingText).toBeNull();
    });

    // A snapshot that cannot animate would just freeze the outgoing text on
    // screen and then cut it, which is worse than removing it right away.
    test('skips the snapshot for a reader who prefers reduced motion', () => {
        const original = Object.getOwnPropertyDescriptor(window, 'matchMedia');
        Object.defineProperty(window, 'matchMedia', {
            configurable: true,
            writable: true,
            value: () => ({matches: true}),
        });

        try {
            const {result, rerender} = renderHandover('Let me look that up', true);

            rerender({answerText: '', holding: true});

            expect(result.current.foldingText).toBeNull();
        } finally {
            if (original) {
                Object.defineProperty(window, 'matchMedia', original);
            } else {
                delete (window as {matchMedia?: unknown}).matchMedia;
            }
        }
    });
});
