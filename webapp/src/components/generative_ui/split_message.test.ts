// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {splitGenerativeUI} from './split_message';

// Shared with the server's genui.ClosedBlocks, which checks submissions
// against the block index this splitter assigns.
const sharedCases: Array<{name: string; message: string; blocks: string[]}> = JSON.parse(
    fs.readFileSync(path.resolve(__dirname, '../../../../genui/testdata/split_cases.json'), 'utf8'),
);

describe('splitGenerativeUI', () => {
    const cases = [
        {
            name: 'plain markdown stays one segment',
            message: 'Hello **there**\n\n- a\n- b',
            expected: [{type: 'markdown', text: 'Hello **there**\n\n- a\n- b'}],
        },
        {
            name: 'prose around a closed block',
            message: 'Lead-in.\n\n```openui-lang\nroot = Stack([t])\nt = Text("hi")\n```\n\nAfter.',
            expected: [
                {type: 'markdown', text: 'Lead-in.\n'},
                {type: 'ui', source: 'root = Stack([t])\nt = Text("hi")', closed: true},
                {type: 'markdown', text: '\nAfter.'},
            ],
        },
        {
            name: 'unterminated block while streaming',
            message: 'Lead-in.\n```openui-lang\nroot = Stack([t])\nt = Text("hi',
            expected: [
                {type: 'markdown', text: 'Lead-in.'},
                {type: 'ui', source: 'root = Stack([t])\nt = Text("hi', closed: false},
            ],
        },
        {
            name: 'other code fences are left as markdown',
            message: '```go\nfmt.Println("x")\n```',
            expected: [{type: 'markdown', text: '```go\nfmt.Println("x")\n```'}],
        },
        {
            name: 'ui fence nested inside another fence is not rendered',
            message: '````markdown\n```openui-lang\nroot = Stack([])\n```\n````',
            expected: [{type: 'markdown', text: '````markdown\n```openui-lang\nroot = Stack([])\n```\n````'}],
        },
        {
            name: 'two blocks',
            message: '```openui-lang\nroot = Stack([a])\n```\n```openui\nroot = Stack([b])\n```',
            expected: [
                {type: 'ui', source: 'root = Stack([a])', closed: true},
                {type: 'ui', source: 'root = Stack([b])', closed: true},
            ],
        },
    ];

    test.each(cases)('$name', ({message, expected}) => {
        expect(splitGenerativeUI(message)).toEqual(expected);
    });
});

describe('splitGenerativeUI agrees with the server on closed blocks', () => {
    test.each(sharedCases)('$name', ({message, blocks}) => {
        const closed = splitGenerativeUI(message).
            filter((segment) => segment.type === 'ui' && segment.closed).
            map((segment) => (segment.type === 'ui' ? segment.source : ''));
        expect(closed).toEqual(blocks);
    });
});
