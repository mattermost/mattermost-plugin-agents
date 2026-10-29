// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';

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

type FormatText = (text: string, options: unknown) => string;
type HtmlToComponent = (html: string, options: unknown) => React.ReactNode;

interface BlockProps {
    source: string;
    formatText: FormatText;
    messageHtmlToComponent: HtmlToComponent;
    markdownOptions: unknown;
    htmlOptions: unknown;
}

// Memoized on the block's source: a settled block is not formatted again while the block after it grows.
const MarkdownBlockView = React.memo(({source, formatText, messageHtmlToComponent, markdownOptions, htmlOptions}: BlockProps) => (
    <>{messageHtmlToComponent(formatText(source, markdownOptions), htmlOptions)}</>
), (prev, next) => prev.source === next.source &&
    prev.formatText === next.formatText &&
    prev.messageHtmlToComponent === next.messageHtmlToComponent &&
    prev.markdownOptions === next.markdownOptions &&
    prev.htmlOptions === next.htmlOptions);
MarkdownBlockView.displayName = 'MarkdownBlockView';

interface StreamingMarkdownProps {
    message: string;
    formatText: FormatText;
    messageHtmlToComponent: HtmlToComponent;
    markdownOptions: unknown;
    htmlOptions: unknown;
}

/**
 * Renders streaming markdown block by block, so only the still-growing block
 * re-renders per reveal step.
 * Renders fragments only: the blocks stay direct children of the container.
 */
export const StreamingMarkdown = (props: StreamingMarkdownProps) => {
    const blocks = useMemo(() => splitMarkdownBlocks(props.message), [props.message]);
    return (
        <>
            {blocks.map((block, index) => (
                <MarkdownBlockView
                    key={index} // eslint-disable-line react/no-array-index-key
                    source={block.text}
                    formatText={props.formatText}
                    messageHtmlToComponent={props.messageHtmlToComponent}
                    markdownOptions={props.markdownOptions}
                    htmlOptions={props.htmlOptions}
                />
            ))}
        </>
    );
};
