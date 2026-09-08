// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ConversationResponse, ContentBlock, Turn} from '@/types/conversation';

import {buildRoundsFromTurns} from './turn_content_utils';

function makeTurn(content: ContentBlock[], overrides: Partial<Turn> = {}): Turn {
    return {
        id: 'turn_1',
        post_id: 'post_1',
        role: 'assistant',
        content,
        tokens_in: 0,
        tokens_out: 0,
        sequence: 1,
        ...overrides,
    } as Turn;
}

function makeConversation(turns: Turn[]): ConversationResponse {
    return {
        id: 'conv_1',
        user_id: 'user_1',
        bot_id: 'bot_1',
        channel_id: 'chan_1',
        root_post_id: 'post_root',
        title: '',
        operation: 'conversation',
        turns,
    } as ConversationResponse;
}

function activity(id: string, overrides: Record<string, unknown> = {}): ContentBlock {
    return {
        type: 'server_tool_use',
        server_tool: {id, tool: 'code_interpreter', status: 'success', ...overrides},
    } as unknown as ContentBlock;
}

function text(value: string): ContentBlock {
    return {type: 'text', text: value} as ContentBlock;
}

describe('round splitting at provider activity boundaries', () => {
    // RoundView renders activity above text, so activity after text starts a new
    // round. Otherwise narration between two sandbox runs collapses above both.
    test('activity after text starts a new round', () => {
        const turn = makeTurn([
            text("I'll write the script."),
            activity('srv1', {sub_tool: 'bash', command: 'cat > f.py'}),
            text('That produced no file. Retrying.'),
            activity('srv2', {sub_tool: 'python', command: 'open("f.py")'}),
            text('Done.'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(3);

        expect(rounds[0].text).toBe("I'll write the script.");
        expect(rounds[0].serverTools).toHaveLength(0);

        expect(rounds[1].serverTools.map((s) => s.id)).toEqual(['srv1']);
        expect(rounds[1].text).toBe('That produced no file. Retrying.');

        expect(rounds[2].serverTools.map((s) => s.id)).toEqual(['srv2']);
        expect(rounds[2].text).toBe('Done.');
    });

    test('round ids stay unique so they work as React keys', () => {
        const turn = makeTurn([
            text('one'),
            activity('srv1'),
            text('two'),
            activity('srv2'),
            text('three'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');
        const ids = rounds.map((r) => r.id);

        expect(new Set(ids).size).toBe(ids.length);
        expect(ids[0]).toBe('turn_1');
    });

    test('consecutive activity stays in one round so it renders as one group', () => {
        const turn = makeTurn([
            activity('srv1'),
            activity('srv2'),
            activity('srv3'),
            text('Found it.'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(1);
        expect(rounds[0].serverTools.map((s) => s.id)).toEqual(['srv1', 'srv2', 'srv3']);
        expect(rounds[0].text).toBe('Found it.');
    });

    test('thinking after text or activity starts a new round', () => {
        const turn = makeTurn([
            {type: 'thinking', text: 'first thought'} as ContentBlock,
            text('Working on it.'),
            {type: 'thinking', text: 'second thought'} as ContentBlock,
            activity('srv1'),
            text('Done.'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(2);
        expect(rounds[0].reasoning.summary).toBe('first thought');
        expect(rounds[0].text).toBe('Working on it.');
        expect(rounds[1].reasoning.summary).toBe('second thought');
        expect(rounds[1].serverTools.map((s) => s.id)).toEqual(['srv1']);
        expect(rounds[1].text).toBe('Done.');
    });

    test('consecutive thinking blocks merge into one round', () => {
        const turn = makeTurn([
            {type: 'thinking', text: 'part one'} as ContentBlock,
            {type: 'thinking', text: 'part two', signature: 'sig'} as ContentBlock,
            text('Answer.'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(1);
        expect(rounds[0].reasoning).toEqual({summary: 'part one\npart two', signature: 'sig'});
    });

    test('tool calls land on the final round of a split turn', () => {
        const turn = makeTurn([
            text('Looking into it.'),
            activity('srv1'),
            text('Now saving that.'),
            {type: 'tool_use', id: 'tc_1', name: 'CreateFile', status: 'auto_approved'} as ContentBlock,
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(2);
        expect(rounds[0].toolCalls).toHaveLength(0);
        expect(rounds[1].toolCalls.map((tc) => tc.id)).toEqual(['tc_1']);
    });

    test('a turn of only tool calls still renders a round', () => {
        const turn = makeTurn([
            {type: 'tool_use', id: 'tc_1', name: 'lookup', status: 'success'} as ContentBlock,
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(1);
        expect(rounds[0].toolCalls.map((tc) => tc.id)).toEqual(['tc_1']);
    });

    test('trailing activity with no text after it still renders', () => {
        const turn = makeTurn([
            text('Running it now.'),
            activity('srv1'),
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(2);
        expect(rounds[0].text).toBe('Running it now.');
        expect(rounds[1].serverTools.map((s) => s.id)).toEqual(['srv1']);
        expect(rounds[1].text).toBe('');
    });
});

describe('citation offsets across a split turn', () => {
    // Server offsets are against the whole message; citation_processor slices
    // by end_index, so unrebased markers land in the wrong round.
    test('annotations are assigned to the round holding their text, rebased', () => {
        const turn = makeTurn([
            text('0123456789'),
            activity('srv1', {tool: 'web_search', query: 'q'}),
            text('abcdefghij'),
            {
                type: 'annotations',
                web_search_context: {
                    count: 2,
                    results: [
                        {type: 'url_citation', start_index: 0, end_index: 4, url: 'https://a', index: 0},
                        {type: 'url_citation', start_index: 12, end_index: 15, url: 'https://b', index: 1},
                    ],
                },
            } as unknown as ContentBlock,
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(2);

        expect(rounds[0].annotations).toHaveLength(1);
        expect(rounds[0].annotations[0].url).toBe('https://a');
        expect(rounds[0].annotations[0].end_index).toBe(4);

        expect(rounds[1].annotations).toHaveLength(1);
        expect(rounds[1].annotations[0].url).toBe('https://b');
        expect(rounds[1].annotations[0].start_index).toBe(2);
        expect(rounds[1].annotations[0].end_index).toBe(5);
    });

    test('a turn with one text run keeps its offsets untouched', () => {
        const turn = makeTurn([
            activity('srv1', {tool: 'web_search', query: 'q'}),
            text('0123456789'),
            {
                type: 'annotations',
                web_search_context: {
                    count: 1,
                    results: [
                        {type: 'url_citation', start_index: 3, end_index: 7, url: 'https://a', index: 0},
                    ],
                },
            } as unknown as ContentBlock,
        ]);

        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_1');

        expect(rounds).toHaveLength(1);
        expect(rounds[0].annotations[0].start_index).toBe(3);
        expect(rounds[0].annotations[0].end_index).toBe(7);
    });
});
