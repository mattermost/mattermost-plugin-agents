// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render} from '@testing-library/react';

import {StreamingMarkdown, markFreshText, splitMarkdownBlocks} from './streaming_markdown';
import type {FreshRange} from './use_smooth_text';

// A stand-in renderer: `**x**` becomes <strong>, everything else is text in a <p>.
const formatText = jest.fn((text: string) => text);
const messageHtmlToComponent = (html: string) => {
    const parts = html.split('**');
    return React.createElement('p', null, ...parts.map((part, i) => (i % 2 === 1 ? React.createElement('strong', {key: i}, part) : part)));
};

const markdownOptions = {};
const htmlOptions = {};

function renderMarkdown(message: string, fresh: readonly FreshRange[] = [], onFreshEnd = jest.fn()) {
    const element = (msg: string, f: readonly FreshRange[]) => (
        <StreamingMarkdown
            message={msg}
            fresh={f}
            onFreshEnd={onFreshEnd}
            formatText={formatText}
            messageHtmlToComponent={messageHtmlToComponent}
            markdownOptions={markdownOptions}
            htmlOptions={htmlOptions}
        />
    );
    const result = render(<div data-testid='root'>{element(message, fresh)}</div>);
    return {
        ...result,
        update: (msg: string, f: readonly FreshRange[] = []) => result.rerender(<div data-testid='root'>{element(msg, f)}</div>),
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

describe('markFreshText', () => {
    const range = (start: number, end: number, id = 1): FreshRange => ({id, start, end});
    const strip = (text: string) => text.replace(/[-]/g, '');

    test.each([
        {name: 'wraps a word after whitespace', text: 'hello world', fresh: [range(6, 11)], marked: 'hello world'},
        {name: 'skips a list marker at line start', text: '- item one', fresh: [range(0, 10)], marked: '- item one'},
        {name: 'skips a heading marker', text: '## Title', fresh: [range(0, 8)], marked: '## Title'},
        {name: 'ends before the next word', text: 'one two three', fresh: [range(0, 4)], marked: 'one two three'},
        {name: 'never marks inside inline code', text: 'run `npm test` now', fresh: [range(9, 18)], marked: 'run `npm test` now'},
        {name: 'never marks a code fence block', text: '```\ncode here\n```', fresh: [range(4, 13)], marked: '```\ncode here\n```'},
        {name: 'never marks a table row start', text: '| a | b |', fresh: [range(0, 9)], marked: '| a | b |'},
        {name: 'never marks a rule', text: '- - -', fresh: [range(0, 5)], marked: '- - -'},
        {name: 'leaves text alone without fresh ranges', text: 'plain', fresh: [], marked: 'plain'},
    ])('$name', ({text, fresh, marked}) => {
        const result = markFreshText({start: 0, text}, fresh);
        expect(result).toBe(marked);
        expect(strip(result)).toBe(text);
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

    test('wraps only fresh text, across inline elements, without marker characters', () => {
        const message = 'Settled text **bold words** after';
        const {getByTestId, container} = renderMarkdown(message, [{id: 7, start: 13, end: message.length}]);
        const fresh = Array.from(container.querySelectorAll('[data-fresh-text]'));
        expect(fresh.map((node) => node.textContent)).toEqual(['bold words', ' after']);
        expect(fresh.every((node) => node.getAttribute('data-fresh-text') === '7')).toBe(true);
        expect(getByTestId('root').textContent).toBe('Settled text bold words after');
        expect(getByTestId('root').querySelector('strong')?.textContent).toBe('bold words');
    });

    test('reports the end of the fade and renders plain text once retired', () => {
        const onFreshEnd = jest.fn();
        const {container, update, getByTestId} = renderMarkdown('Hello world', [{id: 3, start: 6, end: 11}], onFreshEnd);
        const fresh = container.querySelector('[data-fresh-text]')!;
        expect(fresh.textContent).toBe('world');

        fireEvent.animationEnd(fresh);
        expect(onFreshEnd).toHaveBeenCalledWith(3);

        update('Hello world', []);
        expect(container.querySelector('[data-fresh-text]')).toBeNull();
        expect(getByTestId('root').textContent).toBe('Hello world');
    });

    test('settled text keeps its DOM when later text arrives', () => {
        const {container, update} = renderMarkdown('Hello ', [{id: 1, start: 0, end: 6}]);
        const first = container.querySelector('[data-fresh-text="1"]');
        update('Hello world', [{id: 1, start: 0, end: 6}, {id: 2, start: 6, end: 11}]);
        expect(container.querySelector('[data-fresh-text="1"]')).toBe(first);
        expect(container.querySelector('[data-fresh-text="2"]')?.textContent).toBe('world');
    });
});
