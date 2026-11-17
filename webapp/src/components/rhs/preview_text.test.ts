// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {stripMarkdownPreview} from './preview_text';

describe('stripMarkdownPreview', () => {
    test.each([
        {
            name: 'plain text',
            source: 'Key selling points: self-hosted and air-gapped deployment',
            expected: 'Key selling points: self-hosted and air-gapped deployment',
        },
        {
            name: 'emphasis',
            source: '**bold** *italic* _em_ ~~gone~~ ***both***',
            expected: 'bold italic em gone both',
        },
        {
            name: 'emphasis wrapped across a line',
            source: '**bold\ntext**',
            expected: 'bold text',
        },
        {
            name: 'nested emphasis',
            source: '**bold *nested* text**',
            expected: 'bold nested text',
        },
        {
            name: 'asterisks attached to a word',
            source: 'hello**there**',
            expected: 'hellothere',
        },
        {
            name: 'one-line fence',
            source: 'See ```const n = 1``` please',
            expected: 'See const n = 1 please',
        },
        {
            name: 'heading',
            source: '## Status\n\nAll good',
            expected: 'Status All good',
        },
        {
            name: 'heading with closing hashes',
            source: '## Status ##',
            expected: 'Status',
        },
        {
            name: 'a heading that ends in a hash keeps it',
            source: '## C#',
            expected: 'C#',
        },
        {
            name: 'a hash without a space is not a heading',
            source: 'See #release',
            expected: 'See #release',
        },
        {
            name: 'setext heading and a rule',
            source: 'Title\n=====\n\nHello\n\n---\n\nworld',
            expected: 'Title Hello world',
        },
        {
            name: 'link keeps its label',
            source: 'See [the docs](https://example.com/docs) today',
            expected: 'See the docs today',
        },
        {
            name: 'link with parentheses in the url',
            source: '[wiki](https://en.wikipedia.org/wiki/Mattermost_(software))',
            expected: 'wiki',
        },
        {
            name: 'image keeps its alt text',
            source: 'Chart: ![latency](https://example.com/a.png)',
            expected: 'Chart: latency',
        },
        {
            name: 'image with no alt text',
            source: 'Before ![](https://example.com/a.png) after',
            expected: 'Before after',
        },
        {
            name: 'autolink keeps the url',
            source: 'Go to <https://example.com>',
            expected: 'Go to https://example.com',
        },
        {
            name: 'reference link drops the definition',
            source: 'See [docs][1]\n\n[1]: https://example.com',
            expected: 'See docs',
        },
        {
            name: 'fenced code',
            source: 'Run\n\n```ts\nconst n = 1;\n```\nnow',
            expected: 'Run const n = 1; now',
        },
        {
            name: 'fenced code keeps a dollar sign',
            source: '```\nprice $2\n```',
            expected: 'price $2',
        },
        {
            name: 'inline code',
            source: 'Set `config` now',
            expected: 'Set config now',
        },
        {
            name: 'quote, lists, and a task',
            source: '> note\n- one\n* two\n1. three\n- [x] four',
            expected: 'note one two three four',
        },
        {
            name: 'table',
            source: '| A | B |\n| --- | --- |\n| 1 | 2 |',
            expected: 'A B 1 2',
        },
        {
            name: 'a single pipe in a sentence stays',
            source: 'use a | b here',
            expected: 'use a | b here',
        },
        {
            name: 'identifiers and multiplication stay',
            source: 'set user_id on obj.__init__ and 2*3*4',
            expected: 'set user_id on obj.__init__ and 2*3*4',
        },
        {
            name: 'escaped asterisk stays literal',
            source: 'use \\*pointer',
            expected: 'use *pointer',
        },
        {
            name: 'escaped emphasis stays literal',
            source: '\\*literal\\*',
            expected: '*literal*',
        },
        {
            name: 'ordinary pipes stay',
            source: 'Choose a || b',
            expected: 'Choose a || b',
        },
        {
            name: 'fenced code keeps a type argument',
            source: '```ts\nArray<string>\n```',
            expected: 'Array<string>',
        },
        {
            name: 'inline code keeps a type argument',
            source: 'Use `Array<string>` here',
            expected: 'Use Array<string> here',
        },
        {
            name: 'a generic in prose stays',
            source: 'Array<string> values',
            expected: 'Array<string> values',
        },
        {
            name: 'an email autolink stays',
            source: 'Mail <dev@example.com> today',
            expected: 'Mail <dev@example.com> today',
        },
        {
            name: 'prose up to a later angle bracket stays',
            source: 'see <b the note> then > next',
            expected: 'see <b the note> then > next',
        },
        {
            name: 'html tags',
            source: 'Hello<br>there <strong>friend</strong>',
            expected: 'Hello there friend',
        },
        {
            name: 'blank',
            source: '   ',
            expected: '',
        },
        {
            name: 'only a fence',
            source: '```\n```',
            expected: '',
        },
    ])('$name', ({source, expected}) => {
        expect(stripMarkdownPreview(source)).toBe(expected);
    });
});
