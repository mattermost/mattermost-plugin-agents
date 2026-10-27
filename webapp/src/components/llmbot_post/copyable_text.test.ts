// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getCopyableText} from './copyable_text';
import type {Round} from './turn_content_utils';

const round = (text: string, extra: Partial<Round> = {}): Round => ({
    id: 'r',
    text,
    toolCalls: [],
    reasoning: {summary: '', signature: ''},
    annotations: [],
    serverTools: [],
    ...extra,
});

describe('getCopyableText', () => {
    test.each([
        {name: 'a single round', rounds: [round('Hello there')], want: 'Hello there'},
        {name: 'several rounds in order, joined by a blank line', rounds: [round('One'), round('Two'), round('Three')], want: 'One\n\nTwo\n\nThree'},
        {
            name: 'markdown as stored',
            rounds: [round('# Title\n\n- a\n- b\n\n```go\nfmt.Println("x")\n```\n\n**bold** [1]')],
            want: '# Title\n\n- a\n- b\n\n```go\nfmt.Println("x")\n```\n\n**bold** [1]',
        },
        {
            name: 'no reasoning, tool calls or server tools',
            rounds: [round('Answer', {
                reasoning: {summary: 'private thoughts', signature: 's'},
                toolCalls: [{id: 't', name: 'search', description: '', arguments: {}, status: 0}] as Round['toolCalls'],
                serverTools: [{id: 's', name: 'web_search'}] as unknown as Round['serverTools'],
            })],
            want: 'Answer',
        },
        {name: 'skips rounds without text', rounds: [round(''), round('Kept'), round('  \n')], want: 'Kept'},
        {name: 'nothing to copy', rounds: [round('')], want: ''},
        {name: 'no rounds', rounds: [], want: ''},
        {name: 'drops the smooth-streaming fade markers', rounds: [round('Hello world')], want: 'Hello world'},
    ])('$name', ({rounds, want}) => {
        expect(getCopyableText(rounds)).toBe(want);
    });
});
