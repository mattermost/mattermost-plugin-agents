// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useReducer, useRef} from 'react';

// Reveal pacing has no compass token (compass motion tokens are UI transition
// durations, not text rates), so these are local named values.

/** Slowest steady reveal rate, so a small burst reads as quick typing. */
export const MIN_REVEAL_CHARS_PER_SECOND = 120;

/** A burst is revealed linearly within this long of its arrival. */
export const BACKLOG_DRAIN_MS = 350;

/** Hard cap on how far displayed text may trail received text. */
export const MAX_REVEAL_LAG_MS = 1000;

/** Once generation ends, the remaining buffer is revealed within this long. */
export const FLUSH_MAX_MS = 280;

/** A run without whitespace longer than this is revealed mid-word. */
export const MAX_WORD_HOLD_CHARS = 24;

/** Fresh ranges whose fade never reported its end are settled after this. */
export const FRESH_MAX_AGE_MS = 1000;

/** A just-revealed slice of the displayed text, faded in by the renderer. */
export interface FreshRange {
    id: number;
    start: number;
    end: number;
}

export interface FrameScheduler {
    request: (callback: () => void) => number;
    cancel: (handle: number) => void;
}

export interface SmoothTextOptions {

    /** Off: the displayed text is the received text, as before. */
    enabled: boolean;

    /** True while the text may still grow. */
    streaming: boolean;

    /** When streaming stops (Stop, cancel, error), show everything at once. */
    flushImmediately: boolean;
    scheduler?: FrameScheduler;
    now?: () => number;
}

export interface SmoothText {
    text: string;
    fresh: readonly FreshRange[];

    /** Settles a fresh range, once its fade has finished. */
    retire: (id: number) => void;
}

interface TimedFreshRange extends FreshRange {
    at: number;
}

interface RevealState {
    target: string;
    shown: number;
    cursor: number;
    streaming: boolean;
    deadline: number;
    arrivals: Array<{end: number; at: number}>;
    flush: {start: number; from: number} | null;
    fresh: TimedFreshRange[];
    nextId: number;
    lastFrame: number | null;
    handle: number | null;
}

const NO_FRESH: readonly FreshRange[] = [];

const defaultScheduler: FrameScheduler = {
    request: (callback) => window.requestAnimationFrame(() => callback()),
    cancel: (handle) => window.cancelAnimationFrame(handle),
};

const defaultNow = () => performance.now();

function isWhitespace(code: number): boolean {
    return code === 32 || code === 9 || code === 10 || code === 13;
}

function isHighSurrogate(code: number): boolean {
    return code >= 0xD800 && code <= 0xDBFF;
}

/**
 * Where the reveal may stop: after the last whitespace the cursor passed, so
 * words appear whole; mid-word only for very long runs or a lag floor. Never
 * between the halves of a surrogate pair.
 */
export function revealBoundary(text: string, from: number, cursor: number, floor: number): number {
    const len = text.length;
    const pos = Math.min(len, Math.floor(cursor));
    if (pos >= len) {
        return len;
    }

    let candidate = from;
    for (let i = pos - 1; i >= from; i--) {
        if (isWhitespace(text.charCodeAt(i))) {
            candidate = i + 1;
            break;
        }
    }
    if (candidate === from && pos - from >= MAX_WORD_HOLD_CHARS) {
        candidate = pos;
    }
    candidate = Math.max(candidate, Math.min(floor, len));

    if (candidate > 0 && candidate < len && isHighSurrogate(text.charCodeAt(candidate - 1))) {
        candidate += 1;
    }
    return candidate;
}

function snap(state: RevealState, target: string, keepFresh: boolean) {
    state.target = target;
    state.shown = target.length;
    state.cursor = target.length;
    state.arrivals = [];
    state.flush = null;
    if (!keepFresh) {
        state.fresh = [];
    }
}

// The first word shows as soon as it arrives, so the thinking indicator hands
// off without waiting a frame (or appearing at all for a reply that started
// just before its delay).
function revealFirstWord(state: RevealState, now: number) {
    const text = state.target;
    let end = Math.min(text.length, MAX_WORD_HOLD_CHARS);
    for (let i = 0; i < end; i++) {
        if (isWhitespace(text.charCodeAt(i))) {
            end = i + 1;
            break;
        }
    }
    if (end < text.length && isHighSurrogate(text.charCodeAt(end - 1))) {
        end += 1;
    }
    state.fresh = [...state.fresh, {id: state.nextId, start: 0, end, at: now}];
    state.nextId += 1;
    state.shown = end;
    state.cursor = Math.max(state.cursor, end);
}

// Runs during render so a replaced or reset text never paints stale for a frame.
function reconcile(state: RevealState, target: string, options: SmoothTextOptions, now: number) {
    const {enabled, streaming, flushImmediately} = options;
    if (!enabled) {
        state.streaming = streaming;
        if (state.target !== target || state.shown !== target.length || state.fresh.length > 0) {
            snap(state, target, false);
        }
        return;
    }

    const rising = streaming && !state.streaming;
    const falling = !streaming && state.streaming;
    state.streaming = streaming;

    if (target !== state.target) {
        const continues = target.startsWith(state.target.slice(0, state.shown));
        if (rising || !continues) {
            // A new generation, a mid-stream mount, or a replacement text.
            snap(state, target, false);
        } else {
            state.target = target;
            if (state.cursor > target.length) {
                state.cursor = target.length;
            }
            if (target.length > state.shown) {
                state.arrivals.push({end: target.length, at: now});
                state.deadline = now + BACKLOG_DRAIN_MS;
            }
            if (streaming && state.shown === 0 && target.length > 0) {
                revealFirstWord(state, now);
            }
        }
    } else if (rising) {
        snap(state, target, false);
    }

    if (streaming) {
        return;
    }
    if (state.shown >= state.target.length) {
        state.flush = null;
        return;
    }
    if (flushImmediately) {
        snap(state, state.target, true);
    } else if (falling) {
        state.flush = {start: now, from: state.shown};
    } else if (!state.flush) {
        // Never streamed here (historic post, or a finished post remounting).
        snap(state, state.target, true);
    }
}

// One frame: returns whether anything visible changed.
function step(state: RevealState, now: number): boolean {
    const dt = state.lastFrame === null ? 0 : Math.max(0, now - state.lastFrame);
    state.lastFrame = now;
    const len = state.target.length;
    let changed = false;

    if (state.shown < len) {
        const backlog = len - state.cursor;
        const remaining = state.deadline - now;
        const catchUpRate = remaining > 0 ? backlog / remaining : Infinity;
        const rate = Math.max(MIN_REVEAL_CHARS_PER_SECOND / 1000, catchUpRate);
        let cursor = Number.isFinite(rate) ? state.cursor + (rate * Math.max(dt, 1)) : len;

        if (state.flush) {
            const progress = Math.min(1, (now - state.flush.start) / FLUSH_MAX_MS);

            // Ease-in: the flush accelerates into its end.
            cursor = Math.max(cursor, state.flush.from + ((len - state.flush.from) * progress * progress));
        }

        let floor = 0;
        while (state.arrivals.length > 0 && state.arrivals[0].at <= now - MAX_REVEAL_LAG_MS) {
            floor = Math.max(floor, state.arrivals.shift()!.end);
        }
        cursor = Math.min(len, Math.max(cursor, floor));
        state.cursor = cursor;

        const next = revealBoundary(state.target, state.shown, cursor, floor);
        if (next > state.shown) {
            state.fresh = [...state.fresh, {id: state.nextId, start: state.shown, end: next, at: now}];
            state.nextId += 1;
            state.shown = next;
            changed = true;
        }
        while (state.arrivals.length > 0 && state.arrivals[0].end <= state.shown) {
            state.arrivals.shift();
        }
        if (state.shown >= len) {
            state.flush = null;
        }
    }

    const live = state.fresh.filter((range) => range.at > now - FRESH_MAX_AGE_MS);
    if (live.length !== state.fresh.length) {
        state.fresh = live;
        changed = true;
    }
    return changed;
}

function needsFrames(state: RevealState): boolean {
    return state.shown < state.target.length || state.fresh.length > 0;
}

/**
 * Decouples received text from displayed text: displayed text advances every
 * frame at a steady rate that speeds up with the backlog, so bursty chunks
 * read as even typing without trailing far behind. Text that was not
 * streaming when it mounted shows in full at once.
 */
export function useSmoothText(target: string, options: SmoothTextOptions): SmoothText {
    const scheduler = options.scheduler ?? defaultScheduler;
    const now = options.now ?? defaultNow;
    const [, forceRender] = useReducer((count: number) => count + 1, 0);

    const stateRef = useRef<RevealState | null>(null);
    if (stateRef.current === null) {
        stateRef.current = {
            target,
            shown: target.length,
            cursor: target.length,
            streaming: options.streaming,
            deadline: 0,
            arrivals: [],
            flush: null,
            fresh: [],
            nextId: 1,
            lastFrame: null,
            handle: null,
        };
    }
    const state = stateRef.current;
    reconcile(state, target, options, now());

    const schedulerRef = useRef(scheduler);
    schedulerRef.current = scheduler;
    const nowRef = useRef(now);
    nowRef.current = now;

    const tick = useCallback(() => {
        const current = stateRef.current!;
        current.handle = null;
        const changed = step(current, nowRef.current());
        if (needsFrames(current)) {
            current.handle = schedulerRef.current.request(tick);
        } else {
            current.lastFrame = null;
        }
        if (changed) {
            forceRender();
        }
    }, []);

    useEffect(() => {
        if (options.enabled && state.handle === null && needsFrames(state)) {
            state.lastFrame = nowRef.current();
            state.handle = schedulerRef.current.request(tick);
        }
    });

    useEffect(() => () => {
        const current = stateRef.current;
        if (current && current.handle !== null) {
            schedulerRef.current.cancel(current.handle);
            current.handle = null;
        }
    }, []);

    const retire = useCallback((id: number) => {
        const current = stateRef.current!;
        if (current.fresh.some((range) => range.id === id)) {
            current.fresh = current.fresh.filter((range) => range.id !== id);
            forceRender();
        }
    }, []);

    return {
        text: state.shown === state.target.length ? state.target : state.target.slice(0, state.shown),
        fresh: state.fresh.length === 0 ? NO_FRESH : state.fresh,
        retire,
    };
}
