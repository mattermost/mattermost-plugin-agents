// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';

import type {FreshRange} from './use_smooth_text';

// Private-use characters mark where fresh text starts and ends. They pass
// through markdown untouched and are stripped from the rendered tree.
const FRESH_START = '';
const FRESH_END = '';
const FRESH_ID_BASE = 0xE100;
const FRESH_ID_SPAN = 0x0E00;
const MARKER_PATTERN = /([-]|)/;

export interface MarkdownBlock {

    /** Offset of the block in the whole message. */
    start: number;
    text: string;
}

const FENCE_LINE = /^ {0,3}(```|~~~)/;
const LIST_ITEM_LINE = /^(?:[-*+]|\d{1,9}[.)])(?:\s|$)/;
const REFERENCE_DEFINITION = /^ {0,3}\[[^\]]+\]:/m;

/**
 * Splits markdown into top-level blocks at blank lines, only where rendering
 * the parts separately cannot change the result: outside code fences, and
 * before a line that starts a new paragraph-level block at column 0 (not a
 * list item or an indented continuation, which could belong to a list above).
 */
export function splitMarkdownBlocks(message: string): MarkdownBlock[] {
    if (REFERENCE_DEFINITION.test(message)) {
        return [{start: 0, text: message}];
    }

    const blocks: MarkdownBlock[] = [];
    const lines = message.split('\n');
    let blockStart = 0;
    let offset = 0;
    let inFence = false;
    let sawBlank = false;

    for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const blank = line.trim() === '';

        if (!inFence && sawBlank && !blank && !(/^\s/).test(line) && !LIST_ITEM_LINE.test(line)) {
            blocks.push({start: blockStart, text: message.slice(blockStart, offset).replace(/\n+$/, '')});
            blockStart = offset;
        }

        if (FENCE_LINE.test(line)) {
            inFence = !inFence;
        }
        sawBlank = blank && !inFence;
        offset += line.length + 1;
    }

    blocks.push({start: blockStart, text: message.slice(blockStart)});
    return blocks;
}

const LINE_PREFIX = /^[ \t]*(?:(?:[-*+]|\d{1,9}[.)])[ \t]+|#{1,6}[ \t]+|>[ \t]?)*/;
const LINE_START_ALLOWED = /[\p{L}\p{N}"'([@:!*_]/u;
const THEMATIC_BREAK = /^([-*_])(?:[ \t]*\1){2,}[ \t]*$/;

function isSpace(char: string): boolean {
    return char === ' ' || char === '\t' || char === '\n' || char === '\r';
}

// The first position at or after `from` where a marker cannot change how the
// block parses: before a word that follows whitespace, past any list, heading
// or quote prefix at the start of a line, outside inline code and indented
// code, and not on a line that would become a table row or a rule.
function nextSafeMarker(text: string, from: number): number | null {
    let p = from;
    while (p < text.length) {
        const lineStart = text.lastIndexOf('\n', p - 1) + 1;
        const lineEndIdx = text.indexOf('\n', lineStart);
        const lineEnd = lineEndIdx === -1 ? text.length : lineEndIdx;
        const line = text.slice(lineStart, lineEnd);
        const prefixText = LINE_PREFIX.exec(line)?.[0] ?? '';

        // Indented code (but not a nested list item) and rules take no marker.
        const indentedCode = (/^( {4}|\t)/).test(line) && !(/[-*+\d#>]/).test(prefixText);
        if (indentedCode || THEMATIC_BREAK.test(line.trim())) {
            p = lineEnd + 1;
            continue;
        }

        if (p === lineStart) {
            const prefix = prefixText.length;
            const rest = line.slice(prefix);
            const first = rest.charAt(0);
            if (prefix < line.length && LINE_START_ALLOWED.test(first) && !THEMATIC_BREAK.test(rest)) {
                return lineStart + prefix;
            }
            p = lineStart + Math.max(prefix, 1);
            continue;
        }

        if (isSpace(text.charAt(p - 1)) && !isSpace(text.charAt(p))) {
            const ticks = text.slice(lineStart, p).split('`').length - 1;
            if (ticks % 2 === 0) {
                return p;
            }
        }
        p += 1;
    }
    return null;
}

/** Marks the fresh ranges that fall inside one block, at parse-safe positions. */
export function markFreshText(block: MarkdownBlock, fresh: readonly FreshRange[]): string {
    if (fresh.length === 0 || FENCE_LINE.test(block.text) || (/\n {0,3}(```|~~~)/).test(block.text)) {
        return block.text;
    }
    const blockEnd = block.start + block.text.length;
    const inserts: Array<{at: number; marker: string}> = [];

    for (const range of fresh) {
        if (range.end <= block.start || range.start >= blockEnd) {
            continue;
        }
        const start = nextSafeMarker(block.text, Math.max(range.start, block.start) - block.start);
        if (start === null) {
            continue;
        }
        const end = range.end >= blockEnd ? null : nextSafeMarker(block.text, range.end - block.start);
        if (end !== null && end <= start) {
            continue;
        }
        inserts.push({at: start, marker: FRESH_START + String.fromCharCode(FRESH_ID_BASE + (range.id % FRESH_ID_SPAN))});
        if (end !== null) {
            inserts.push({at: end, marker: FRESH_END});
        }
    }
    if (inserts.length === 0) {
        return block.text;
    }

    // An end and the next start can share a position: the end goes first.
    inserts.sort((a, b) => a.at - b.at || (a.marker === FRESH_END ? -1 : 1));
    let out = '';
    let last = 0;
    for (const insert of inserts) {
        out += block.text.slice(last, insert.at) + insert.marker;
        last = insert.at;
    }
    return out + block.text.slice(last);
}

interface WalkState {
    open: number | null;

    /** Marker ids of ranges still fading in. */
    fading: ReadonlySet<number>;

    /** Spans emitted so far per range: a range split by inline markup has several. */
    segments: Map<number, number>;
}

function freshIdFromMarker(marker: string): number {
    return marker.charCodeAt(1) - FRESH_ID_BASE;
}

// Each span's key depends only on its own range (never on how many spans come
// before it), so a span keeps its DOM node, and its fade is not replayed, while
// the text around it grows.
function wrapFreshText(node: React.ReactNode, state: WalkState): React.ReactNode {
    if (typeof node === 'string') {
        if (state.open === null && node.indexOf(FRESH_START) === -1 && node.indexOf(FRESH_END) === -1) {
            return node;
        }
        const parts = node.split(MARKER_PATTERN);
        const out: React.ReactNode[] = [];
        for (const part of parts) {
            if (part === FRESH_END) {
                state.open = null;
            } else if (part.length === 2 && part.charAt(0) === FRESH_START) {
                state.open = freshIdFromMarker(part);
            } else if (part !== '') {
                if (state.open === null) {
                    out.push(part);
                } else {
                    const id = state.open;
                    const segment = (state.segments.get(id) ?? 0) + 1;
                    state.segments.set(id, segment);
                    out.push(
                        <span
                            key={`fresh-${id}-${segment}`}
                            data-fresh-text={id}

                            // data-fade marks text revealed moments ago, still inside its fade window.
                            data-fade={state.fading.has(id) ? true : undefined} // eslint-disable-line no-undefined
                        >
                            {part}
                        </span>,
                    );
                }
            }
        }
        if (out.length === 0) {
            return null;
        }
        return out.length === 1 ? out[0] : out;
    }

    if (Array.isArray(node)) {
        let changedAny = false;
        const mapped = node.map((child) => {
            const result = wrapFreshText(child, state);
            changedAny = changedAny || result !== child;
            return result;
        });
        return changedAny ? mapped : node;
    }

    if (React.isValidElement(node)) {
        const children = (node.props as {children?: React.ReactNode}).children;
        if (children === undefined || children === null) { // eslint-disable-line no-undefined
            return node;
        }
        const result = wrapFreshText(children, state);
        if (result === children) {
            return node;
        }

        // Children are passed flat: a text run that gains a span would otherwise
        // turn from one child into a nested array, which React treats as a new
        // child and re-creates (its earlier spans with it).
        return React.cloneElement(node, undefined, ...flattenNodes(result)); // eslint-disable-line no-undefined
    }

    return node;
}

function flattenNodes(node: React.ReactNode): React.ReactNode[] {
    if (!Array.isArray(node)) {
        return [node];
    }
    return node.flatMap((child) => flattenNodes(child));
}

type FormatText = (text: string, options: unknown) => string;
type HtmlToComponent = (html: string, options: unknown) => React.ReactNode;

interface BlockProps {
    source: string;
    fading: ReadonlySet<number>;
    formatText: FormatText;
    messageHtmlToComponent: HtmlToComponent;
    markdownOptions: unknown;
    htmlOptions: unknown;
}

// Memoized on the marked source (not on which ranges are still fading): a
// settled block is not formatted again while the block after it grows.
const MarkdownBlockView = React.memo(({source, fading, formatText, messageHtmlToComponent, markdownOptions, htmlOptions}: BlockProps) => {
    const rendered = messageHtmlToComponent(formatText(source, markdownOptions), htmlOptions);
    if (source.indexOf(FRESH_START) === -1 && source.indexOf(FRESH_END) === -1) {
        return <>{rendered}</>;
    }
    return <>{wrapFreshText(rendered, {open: null, fading, segments: new Map()})}</>;
}, (prev, next) => prev.source === next.source &&
    prev.formatText === next.formatText &&
    prev.messageHtmlToComponent === next.messageHtmlToComponent &&
    prev.markdownOptions === next.markdownOptions &&
    prev.htmlOptions === next.htmlOptions);
MarkdownBlockView.displayName = 'MarkdownBlockView';

interface StreamingMarkdownProps {
    message: string;
    fresh: readonly FreshRange[];

    /** Ranges revealed at or before this have finished fading; all fade when omitted. */
    fadeAfter?: number;
    formatText: FormatText;
    messageHtmlToComponent: HtmlToComponent;
    markdownOptions: unknown;
    htmlOptions: unknown;
}

/**
 * Renders streaming markdown block by block, so only the still-growing block
 * re-renders per reveal step, with revealed text wrapped in spans that fade in
 * once and then keep their DOM nodes for the rest of the stream.
 * Renders fragments only: the blocks stay direct children of the container.
 */
export const StreamingMarkdown = (props: StreamingMarkdownProps) => {
    const blocks = useMemo(() => splitMarkdownBlocks(props.message), [props.message]);
    const fadeAfter = props.fadeAfter ?? -Infinity;
    const fading = useMemo(
        () => new Set(props.fresh.filter((range) => range.at > fadeAfter).map((range) => range.id % FRESH_ID_SPAN)),
        [props.fresh, fadeAfter],
    );
    return (
        <>
            {blocks.map((block, index) => (
                <MarkdownBlockView
                    key={index} // eslint-disable-line react/no-array-index-key
                    source={markFreshText(block, props.fresh)}
                    fading={fading}
                    formatText={props.formatText}
                    messageHtmlToComponent={props.messageHtmlToComponent}
                    markdownOptions={props.markdownOptions}
                    htmlOptions={props.htmlOptions}
                />
            ))}
        </>
    );
};
