// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RefObject, useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';

import {
    BUBBLE_HEADER_SELECTOR,
    BUBBLE_SELECTOR,
    BUBBLE_TOOLBAR_SELECTOR,
    BUBBLE_WIDE_HOST_CONTENT_SELECTOR,
    HUG_MAX_FRACTION,
} from '../agent_chat/agent_chat_host_styles';

export {HUG_MAX_FRACTION};

export type BubbleFit = 'hug' | 'full';

// Plugin content that always needs the full row: code, tables, media, tool
// and reasoning UI, artifacts, permalink previews, notices.
const WIDE_CONTENT_SELECTOR = [
    'pre',
    'table',
    'img',
    'video',
    'iframe',
    '[data-testid=\'llm-bot-tool-activity\']',
    '[data-testid=\'html-artifact-card\']',
    '[data-testid=\'html-artifact-wrapper\']',
    '[data-testid=\'llm-bot-permalink\']',
    '[data-testid=\'llm-bot-post-summary-help\']',
    '[data-bubble-wide]',
].join(', ');

// A hugging bubble this close to its cap is pinned there: its content wants
// more room, so it goes full width. Absorbs subpixel rounding; not a design value.
const PINNED_TOLERANCE_PX = 1;

// The streaming min-width ratchet rounds up to this step, so it does not
// generate a new class for every pixel the text grows; not a design value.
const MIN_WIDTH_STEP_PX = 4;

export interface BubbleMetrics {

    /** Row width available to the bubble (row minus avatar slot and gap). */
    available: number;

    /** Natural width of the plugin content (its widest text line). */
    content: number;

    /** Natural width of the host header (name, time). */
    header: number;

    /** The bubble's horizontal padding. */
    padding: number;

    /** The bubble's current width. */
    width: number;

    /** Content that always needs the full row is present. */
    wide: boolean;
}

/** Hug when the bubble's natural width is under HUG_MAX_FRACTION of the row. */
export function decideBubbleFit(metrics: BubbleMetrics, current: BubbleFit | null): BubbleFit {
    if (metrics.wide || metrics.available <= 0) {
        return 'full';
    }
    const cap = HUG_MAX_FRACTION * metrics.available;
    if (current === 'hug' && metrics.width >= cap - PINNED_TOLERANCE_PX) {
        return 'full';
    }
    return Math.max(metrics.content, metrics.header) + metrics.padding < cap ? 'hug' : 'full';
}

export interface BubbleFitState {
    fit: BubbleFit | null;

    /** Widest content width reached while hugging a streaming reply. */
    minWidth: number;
}

/**
 * While streaming the decision is a one-way ratchet: once full, it stays full
 * until the reply ends, and a hugging bubble never shrinks. Otherwise it is
 * computed from the content alone.
 */
export function nextBubbleFit(prev: BubbleFitState, metrics: BubbleMetrics, streaming: boolean): BubbleFitState {
    if (!streaming) {
        return {fit: decideBubbleFit(metrics, prev.fit), minWidth: 0};
    }
    if (prev.fit === 'full' || decideBubbleFit(metrics, prev.fit) === 'full') {
        return {fit: 'full', minWidth: 0};
    }
    const reached = Math.ceil(metrics.content / MIN_WIDTH_STEP_PX) * MIN_WIDTH_STEP_PX;
    return {fit: 'hug', minWidth: Math.max(prev.minWidth, reached)};
}

function px(value: string): number {
    const parsed = parseFloat(value);
    return Number.isFinite(parsed) ? parsed : 0;
}

// Right edge of the widest text line under `root`, from its left edge. Text
// line boxes are measured (not element boxes, which span the column), so the
// result is the natural width as long as lines do not wrap.
function textExtent(root: Element, left: number): number {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const range = document.createRange();
    if (typeof range.getBoundingClientRect !== 'function') {
        return 0;
    }
    let right = left;
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (!node.nodeValue || node.nodeValue.trim() === '') {
            continue;
        }
        range.selectNodeContents(node);
        const rect = range.getBoundingClientRect();
        if (rect.width > 0) {
            right = Math.max(right, rect.right);
        }
    }
    return right - left;
}

function headerExtent(header: Element): number {
    const left = header.getBoundingClientRect().left;
    let right = left;
    for (const child of Array.from(header.children)) {
        if (child.matches(BUBBLE_TOOLBAR_SELECTOR)) {
            continue;
        }
        right = Math.max(right, child.getBoundingClientRect().right);
    }
    return right - left;
}

/** Reads the host bubble around `wrapper`; never writes to host DOM. */
export function measureBubble(wrapper: HTMLElement, bubble: HTMLElement): BubbleMetrics | null {
    const row = bubble.parentElement;
    if (!row) {
        return null;
    }
    const rowStyle = getComputedStyle(row);
    const avatar = bubble.previousElementSibling;
    const available = row.clientWidth -
        px(rowStyle.paddingLeft) - px(rowStyle.paddingRight) -
        (avatar ? avatar.getBoundingClientRect().width : 0) -
        px(rowStyle.columnGap);

    const bubbleStyle = getComputedStyle(bubble);
    const header = bubble.querySelector(BUBBLE_HEADER_SELECTOR);

    return {
        available,
        content: textExtent(wrapper, wrapper.getBoundingClientRect().left),
        header: header ? headerExtent(header) : 0,
        padding: px(bubbleStyle.paddingLeft) + px(bubbleStyle.paddingRight),
        width: bubble.getBoundingClientRect().width,
        wide: Boolean(wrapper.querySelector(WIDE_CONTENT_SELECTOR) || bubble.querySelector(BUBBLE_WIDE_HOST_CONTENT_SELECTOR)),
    };
}

export interface BubbleWidthOptions {
    enabled: boolean;

    /** True while the reply may still grow (generating or still revealing). */
    streaming: boolean;
    measure?: (wrapper: HTMLElement, bubble: HTMLElement) => BubbleMetrics | null;
}

export interface BubbleWidth {
    fit: BubbleFit | null;
    minWidth: number;

    /** Width changes may animate: this post streamed here and has a first decision. */
    motion: boolean;
}

const INITIAL: BubbleFitState = {fit: null, minWidth: 0};

/**
 * Decides whether the host bubble around `ref` hugs its content. Measures in
 * a layout effect on mount (so a historic post never animates on load), on a
 * reply ending, and from ResizeObserver callbacks, which arrive at most once
 * per frame after layout, so there is no layout read in a loop.
 */
export function useBubbleWidth(ref: RefObject<HTMLElement | null>, options: BubbleWidthOptions): BubbleWidth {
    const {enabled, streaming} = options;
    const measure = options.measure ?? measureBubble;
    const [state, setState] = useState<BubbleFitState>(INITIAL);
    const stateRef = useRef(state);
    const streamingRef = useRef(streaming);
    streamingRef.current = streaming;
    const measureRef = useRef(measure);
    measureRef.current = measure;
    const [streamedHere, setStreamedHere] = useState(streaming);

    const run = useCallback(() => {
        const wrapper = ref.current;
        const bubble = wrapper?.closest<HTMLElement>(BUBBLE_SELECTOR);
        if (!wrapper || !bubble) {
            return;
        }
        const metrics = measureRef.current(wrapper, bubble);
        if (!metrics) {
            return;
        }
        const next = nextBubbleFit(stateRef.current, metrics, streamingRef.current);
        if (next.fit !== stateRef.current.fit || next.minWidth !== stateRef.current.minWidth) {
            stateRef.current = next;
            setState(next);
        }
    }, [ref]);

    useLayoutEffect(() => {
        if (!enabled) {
            if (stateRef.current !== INITIAL) {
                stateRef.current = INITIAL;
                setState(INITIAL);
            }
            return undefined; // eslint-disable-line no-undefined
        }
        run();
        const wrapper = ref.current;
        if (!wrapper || typeof ResizeObserver === 'undefined') {
            return undefined; // eslint-disable-line no-undefined
        }
        const observer = new ResizeObserver(() => run());
        observer.observe(wrapper);
        const row = wrapper.closest(BUBBLE_SELECTOR)?.parentElement;
        if (row) {
            observer.observe(row);
        }
        return () => observer.disconnect();
    }, [enabled, run, ref]);

    // A reply starting or ending: when it ends, drop the ratchet and decide
    // from the final content.
    useLayoutEffect(() => {
        if (streaming) {
            setStreamedHere(true);
        }
        if (enabled) {
            run();
        }
    }, [streaming, enabled, run]);

    // Motion turns on only after the first decision has painted, so the
    // first decision itself never animates.
    const [decisionPainted, setDecisionPainted] = useState(false);
    useEffect(() => {
        if (state.fit !== null && !decisionPainted) {
            setDecisionPainted(true);
        }
    }, [state.fit, decisionPainted]);

    if (!enabled) {
        return {fit: null, minWidth: 0, motion: false};
    }
    return {fit: state.fit, minWidth: state.minWidth, motion: streamedHere && decisionPainted};
}
