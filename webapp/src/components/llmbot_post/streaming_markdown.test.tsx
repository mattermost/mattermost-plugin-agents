// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';

import {StreamingMarkdown, splitMarkdownBlocks} from './streaming_markdown';

// A stand-in renderer: `**x**` becomes <strong>, everything else is text in a <p>.
const formatText = jest.fn((text: string) => text);
const messageHtmlToComponent = (html: string) => {
    const parts = html.split('**');
    return React.createElement('p', null, ...parts.map((part, i) => (i % 2 === 1 ? React.createElement('strong', {key: i}, part) : part)));
};

const markdownOptions = {};
const htmlOptions = {};

function renderMarkdown(message: string) {
    const element = (msg: string) => (
        <StreamingMarkdown
            message={msg}
            formatText={formatText}
            messageHtmlToComponent={messageHtmlToComponent}
            markdownOptions={markdownOptions}
            htmlOptions={htmlOptions}
        />
    );
    const result = render(<div data-testid='root'>{element(message)}</div>);
    return {
        ...result,
        update: (msg: string) => result.rerender(<div data-testid='root'>{element(msg)}</div>),
    };
}

beforeEach(() => {
    formatText.mockClear();
});

describe('splitMarkdownBlocks', () => {
    test.each([
        {name: 'paragraphs split at blank lines', message: 'One\n\nTwo\n\nThree', blocks: ['One', 'Two', 'Three']},
        {name: 'a code fence with blank lines stays whole', message: 'Intro\n\n```\na\n\nb\n```\n\nAfter', blocks: ['Intro', '```\na\n\nb\n```', 'After']},
        {name: 'an unclosed fence keeps the tail together', message: 'Intro\n\n```\na\n\nb', blocks: ['Intro', '```\na\n\nb']},
        {name: 'loose list items stay in one list', message: '- a\n\n- b\n\n1. c\n\n2. d', blocks: ['- a\n\n- b\n\n1. c\n\n2. d']},
        {name: 'indented continuation stays with its item', message: '1. a\n\n   more\n\nNext', blocks: ['1. a\n\n   more', 'Next']},
        {name: 'reference definitions keep the message whole', message: 'See [x]\n\n[x]: http://example.com', blocks: ['See [x]\n\n[x]: http://example.com']},
        {name: 'a single block', message: 'Just one line', blocks: ['Just one line']},
    ])('$name', ({message, blocks}) => {
        const result = splitMarkdownBlocks(message);
        expect(result.map((block) => block.text)).toEqual(blocks);
        for (const block of result) {
            expect(message.slice(block.start, block.start + block.text.length)).toBe(block.text);
        }
    });
});

describe('StreamingMarkdown', () => {
    test('completed blocks are not re-rendered while the tail grows', () => {
        const {update} = renderMarkdown('First block\n\nSecond block\n\nTail');
        expect(formatText.mock.calls.map(([text]) => text)).toEqual(['First block', 'Second block', 'Tail']);

        formatText.mockClear();
        for (const tail of ['Tail grows', 'Tail grows more', 'Tail grows more still']) {
            update(`First block\n\nSecond block\n\n${tail}`);
        }
        expect(formatText.mock.calls.map(([text]) => text)).toEqual(['Tail grows', 'Tail grows more', 'Tail grows more still']);
    });

    test('renders blocks flat, as direct children of the container', () => {
        const {getByTestId} = renderMarkdown('One\n\nTwo');
        const root = getByTestId('root');
        expect(Array.from(root.children).map((child) => child.tagName)).toEqual(['P', 'P']);
        expect(root.textContent).toBe('OneTwo');
    });
});
