// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ConversationResponse, Turn} from '@/types/conversation';

import {ToolCallStatus} from '../tool_types';

import {
    statusStringToEnum,
    extractToolCallsForPost,
    extractReasoningFromTurn,
    extractAnnotationsFromTurn,
    deriveApprovalStageForPost,
    hasAutoApprovedToolsForPost,
    buildRoundsFromTurns,
} from './turn_content_utils';

function makeTurn(overrides: Partial<Turn> = {}): Turn {
    return {
        id: 'turn_1',
        conversation_id: 'conv_1',
        post_id: 'post_1',
        role: 'assistant',
        content: [],
        tokens_in: 0,
        tokens_out: 0,
        sequence: 1,
        created_at: 1000,
        ...overrides,
    };
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
    };
}

describe('statusStringToEnum', () => {
    test.each([
        ['pending', ToolCallStatus.Pending],
        ['accepted', ToolCallStatus.Accepted],
        ['rejected', ToolCallStatus.Rejected],
        ['error', ToolCallStatus.Error],
        ['success', ToolCallStatus.Success],
        ['auto_approved', ToolCallStatus.AutoApproved],
    ] as const)('maps %s to %i', (input, expected) => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        expect(statusStringToEnum(input as any)).toBe(expected);
    });

    test('maps undefined to Pending', () => {
        // eslint-disable-next-line no-undefined, @typescript-eslint/no-explicit-any
        expect(statusStringToEnum(undefined as any)).toBe(ToolCallStatus.Pending);
    });
});
describe('extractToolCallsForPost', () => {
    test('returns empty array when the anchor turn has no tool_use blocks and no follow-ups', () => {
        const turn = makeTurn({post_id: 'post_1', content: [{type: 'text', text: 'hello'}]});
        const conv = makeConversation([turn]);
        expect(extractToolCallsForPost(conv, 'post_1')).toEqual([]);
    });

    test('maps tool_use blocks to ToolCall[] with matching results', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: {city: 'NYC'}, status: 'success', shared: true},
                {type: 'tool_use', id: 'tc_2', name: 'search', input: {q: 'test'}, status: 'error', shared: false},
            ],
        });

        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: '72F sunny', status: 'success'},
                {type: 'tool_result', tool_use_id: 'tc_2', content: 'not found', status: 'error'},
            ],
        });

        const conv = makeConversation([assistantTurn, resultTurn]);
        const result = extractToolCallsForPost(conv, 'post_1');

        expect(result).toHaveLength(2);
        expect(result[0]).toEqual({
            id: 'tc_1',
            name: 'get_weather',
            description: '',
            arguments: {city: 'NYC'},
            result: '72F sunny',
            status: ToolCallStatus.Success,
        });
        expect(result[1]).toEqual({
            id: 'tc_2',
            name: 'search',
            description: '',
            arguments: {q: 'test'},
            result: 'not found',
            status: ToolCallStatus.Error,
        });
    });

    test('handles tool_use with null input (redacted)', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: null, status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        const result = extractToolCallsForPost(conv, 'post_1');

        expect(result).toHaveLength(1);
        expect(result[0].arguments).toBeUndefined();
    });

    test('handles missing tool_result turn', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: {city: 'NYC'}, status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        const result = extractToolCallsForPost(conv, 'post_1');

        expect(result).toHaveLength(1);
        expect(result[0].result).toBeUndefined();
        expect(result[0].status).toBe(ToolCallStatus.Pending);
    });

    // Regression: after an approval flow the conversation has two anchors (post A
    // with pending tools, post B with the continuation). Post B's backward walk
    // must stop at post A's anchor so A's tool_use doesn't appear under B as a
    // duplicate.
    test('does not leak tool calls from a preceding post into this post', () => {
        const user = makeTurn({
            id: 'u',
            post_id: 'post-user',
            sequence: 1,
            role: 'user',
            content: [{type: 'text', text: 'x'}],
        });
        const anchorA = makeTurn({
            id: 'aA',
            post_id: 'post-A',
            sequence: 2,
            role: 'assistant',
            content: [{type: 'tool_use', id: 'tc_a', name: 'search', input: {}, status: 'success', shared: true}],
        });
        const approvedResult = makeTurn({
            id: 'tr',
            post_id: null,
            sequence: 3,
            role: 'tool_result',
            content: [{type: 'tool_result', tool_use_id: 'tc_a', content: 'A done', status: 'success', shared: true}],
        });
        const anchorB = makeTurn({
            id: 'aB',
            post_id: 'post-B',
            sequence: 4,
            role: 'assistant',
            content: [{type: 'text', text: 'continuation'}],
        });
        const conv = makeConversation([user, anchorA, approvedResult, anchorB]);

        const a = extractToolCallsForPost(conv, 'post-A');
        expect(a).toHaveLength(1);
        expect(a[0]).toMatchObject({id: 'tc_a', result: 'A done'});

        const b = extractToolCallsForPost(conv, 'post-B');
        expect(b).toEqual([]);
    });

    // Regression: the streaming refactor creates the anchor assistant turn at
    // the END of the stream (highest sequence), AFTER the tool-round turns
    // persisted during the stream. Aggregation must therefore walk BACKWARDS
    // from the anchor to pick up those preceding rounds. Matches the shape of
    // the reported bug (six turns: user, a1/tr1, a2/tr2, final anchor).
    test('aggregates tool calls from preceding rounds when anchor has only final text', () => {
        const userTurn = makeTurn({
            id: 'u1',
            post_id: 'post-user',
            sequence: 1,
            role: 'user',
            content: [{type: 'text', text: 'use tools'}],
        });
        const round1Assistant = makeTurn({
            id: 'r1a',
            post_id: null,
            sequence: 2,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_a', name: 'get_channel_info', input: {name: 'a'}, status: 'success', shared: true},
                {type: 'tool_use', id: 'tc_b', name: 'get_channel_info', input: {name: 'b'}, status: 'success', shared: true},
            ],
        });
        const round1Result = makeTurn({
            id: 'r1r',
            post_id: null,
            sequence: 3,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_a', content: 'result A', status: 'success', shared: true},
                {type: 'tool_result', tool_use_id: 'tc_b', content: 'result B', status: 'success', shared: true},
            ],
        });
        const round2Assistant = makeTurn({
            id: 'r2a',
            post_id: null,
            sequence: 4,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_c', name: 'read_channel', input: {}, status: 'success', shared: true},
            ],
        });
        const round2Result = makeTurn({
            id: 'r2r',
            post_id: null,
            sequence: 5,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_c', content: 'result C', status: 'success', shared: true},
            ],
        });
        const anchor = makeTurn({
            id: 'anchor',
            post_id: 'post-final',
            sequence: 6,
            role: 'assistant',
            content: [{type: 'text', text: 'summary'}],
        });

        const conv = makeConversation([
            userTurn,
            round1Assistant,
            round1Result,
            round2Assistant,
            round2Result,
            anchor,
        ]);
        const result = extractToolCallsForPost(conv, 'post-final');

        expect(result).toHaveLength(3);
        expect(result[0]).toMatchObject({id: 'tc_a', result: 'result A'});
        expect(result[1]).toMatchObject({id: 'tc_b', result: 'result B'});
        expect(result[2]).toMatchObject({id: 'tc_c', result: 'result C'});
    });

    test('stops aggregation at the preceding user turn', () => {
        // An earlier response's tool_use should not leak into this post's
        // display. The walk backwards from the anchor must stop at the user
        // turn that introduced this response.
        const earlierAssistant = makeTurn({
            id: 'earliera',
            post_id: null,
            sequence: 1,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_earlier', name: 'search', input: {}, status: 'pending'},
            ],
        });
        const earlierUser = makeTurn({
            id: 'earlieru',
            post_id: 'post_earlier_user',
            sequence: 2,
            role: 'user',
            content: [{type: 'text', text: 'earlier question'}],
        });
        const anchor = makeTurn({
            id: 'anchor',
            post_id: 'post_1',
            sequence: 3,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_here', name: 'search', input: {}, status: 'success', shared: true},
            ],
        });

        const conv = makeConversation([earlierAssistant, earlierUser, anchor]);
        const result = extractToolCallsForPost(conv, 'post_1');

        expect(result).toHaveLength(1);
        expect(result[0].id).toBe('tc_here');
    });
});
describe('extractReasoningFromTurn', () => {
    test('returns empty strings when no thinking blocks', () => {
        const turn = makeTurn({content: [{type: 'text', text: 'hello'}]});
        expect(extractReasoningFromTurn(turn)).toEqual({summary: '', signature: ''});
    });

    test('extracts reasoning and signature from thinking block', () => {
        const turn = makeTurn({
            content: [
                {type: 'thinking', text: 'Let me think...', signature: 'sig123'},
            ],
        });
        expect(extractReasoningFromTurn(turn)).toEqual({
            summary: 'Let me think...',
            signature: 'sig123',
        });
    });

    test('concatenates multiple thinking blocks', () => {
        const turn = makeTurn({
            content: [
                {type: 'thinking', text: 'Part 1', signature: 'sig1'},
                {type: 'thinking', text: 'Part 2', signature: 'sig2'},
            ],
        });
        const result = extractReasoningFromTurn(turn);
        expect(result.summary).toBe('Part 1\nPart 2');
        expect(result.signature).toBe('sig2'); // last block's signature
    });
});

describe('extractAnnotationsFromTurn', () => {
    test('returns empty array when no annotations or citations', () => {
        const turn = makeTurn({content: [{type: 'text', text: 'hello'}]});
        expect(extractAnnotationsFromTurn(turn)).toEqual([]);
    });

    test('extracts citations from text blocks', () => {
        const turn = makeTurn({
            content: [{
                type: 'text',
                text: 'The answer is 42.',
                citations: [
                    {type: 'url_citation', url: 'https://example.com', title: 'Source', start_index: 0, end_index: 17},
                ],
            }],
        });
        const result = extractAnnotationsFromTurn(turn);
        expect(result).toHaveLength(1);
        expect(result[0]).toEqual({
            type: 'url_citation',
            start_index: 0,
            end_index: 17,
            url: 'https://example.com',
            title: 'Source',
            index: 0,
        });
    });

    // Mirrors what streaming.go persists into a BlockTypeAnnotations block
    // via WebSearchContext.Results. Without surfacing these, web-search
    // citations disappear when the conversation reloads after stream end.
    test('extracts annotations from BlockTypeAnnotations web_search_context', () => {
        const turn = makeTurn({
            content: [
                {type: 'text', text: 'Answer citing a source.'},
                {
                    type: 'annotations',
                    web_search_context: {
                        results: [
                            {
                                type: 'url_citation',
                                start_index: 7,
                                end_index: 13,
                                url: 'https://example.com/a',
                                title: 'Source A',
                                index: 1,
                            },
                            {
                                type: 'url_citation',
                                start_index: 14,
                                end_index: 20,
                                url: 'https://example.com/b',
                                title: 'Source B',
                                index: 2,
                            },
                        ],
                        executed_queries: null,
                        count: 2,
                    },
                },
            ],
        });

        const result = extractAnnotationsFromTurn(turn);
        expect(result).toHaveLength(2);
        expect(result[0]).toEqual(expect.objectContaining({
            type: 'url_citation',
            url: 'https://example.com/a',
            title: 'Source A',
            start_index: 7,
            end_index: 13,
        }));
        expect(result[1]).toEqual(expect.objectContaining({
            type: 'url_citation',
            url: 'https://example.com/b',
            title: 'Source B',
            start_index: 14,
            end_index: 20,
        }));
    });
});

// deriveApprovalStageForPost now reads the server-computed approval_state
// field on the post-anchor assistant turn. Server-side tests (see
// conversation/approval_state_test.go) cover the actual state machine; these
// tests guard the pass-through and the fail-safe default.
describe('deriveApprovalStageForPost', () => {
    test('returns the server-set approval_state on the post anchor', () => {
        const anchor = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            role: 'assistant',
            approval_state: 'result',
            content: [],
        });
        const conv = makeConversation([anchor]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('result');
    });

    test('defaults to done when the anchor or approval_state is missing', () => {
        const anchor = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            role: 'assistant',
            content: [],
        });
        const conv = makeConversation([anchor]);

        // Defaulting to 'done' renders no approval buttons — safer than
        // 'call' or 'result' which would trigger approval UI on a post
        // whose state the server chose not to report.
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('done');
    });

    test('returns done when the post is not in the conversation', () => {
        const conv = makeConversation([]);
        expect(deriveApprovalStageForPost(conv, 'missing')).toBe('done');
    });
});

describe('hasAutoApprovedToolsForPost', () => {
    test('returns false when no tool_use blocks have auto_approved status', () => {
        const anchor = makeTurn({
            post_id: 'post_1',
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'pending'},
            ],
        });
        const conv = makeConversation([anchor]);
        expect(hasAutoApprovedToolsForPost(conv, 'post_1')).toBe(false);
    });

    test('returns true when a preceding tool-round turn contains an auto_approved tool_use', () => {
        const round = makeTurn({
            id: 'round',
            post_id: null,
            sequence: 1,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'auto_approved'},
            ],
        });
        const anchor = makeTurn({
            id: 'anchor',
            post_id: 'post_1',
            sequence: 2,
            content: [{type: 'text', text: 'done'}],
        });
        const conv = makeConversation([round, anchor]);
        expect(hasAutoApprovedToolsForPost(conv, 'post_1')).toBe(true);
    });

    test('returns false when the post is not in the conversation', () => {
        const anchor = makeTurn({
            post_id: 'post_other',
            content: [],
        });
        const conv = makeConversation([anchor]);
        expect(hasAutoApprovedToolsForPost(conv, 'post_missing')).toBe(false);
    });
});

describe('buildRoundsFromTurns', () => {
    test('returns one round per assistant turn in the response, preserving sequence', () => {
        const userTurn = makeTurn({id: 'u1', role: 'user', sequence: 1, post_id: 'user_post', content: []});
        const round1 = makeTurn({
            id: 'r1',
            post_id: null,
            sequence: 2,
            content: [
                {type: 'text', text: 'Let me search.'},
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'auto_approved'},
            ],
        });
        const toolResult1 = makeTurn({
            id: 'tr1',
            role: 'tool_result',
            post_id: null,
            sequence: 3,
            content: [{type: 'tool_result', tool_use_id: 'tc_1', content: 'channel data'}],
        });
        const round2 = makeTurn({
            id: 'r2',
            post_id: 'post_1',
            sequence: 4,
            content: [{type: 'text', text: 'Found 5 channels.'}],
        });
        const conv = makeConversation([userTurn, round1, toolResult1, round2]);

        const rounds = buildRoundsFromTurns(conv, 'post_1');
        expect(rounds).toHaveLength(2);
        expect(rounds[0].id).toBe('r1');
        expect(rounds[0].text).toBe('Let me search.');
        expect(rounds[0].toolCalls).toHaveLength(1);
        expect(rounds[0].toolCalls[0].id).toBe('tc_1');
        expect(rounds[0].toolCalls[0].result).toBe('channel data');
        expect(rounds[0].toolCalls[0].status).toBe(ToolCallStatus.AutoApproved);
        expect(rounds[1].id).toBe('r2');
        expect(rounds[1].text).toBe('Found 5 channels.');
        expect(rounds[1].toolCalls).toHaveLength(0);
    });

    test('omits tool_result turns from the round list — they pair to tool_use blocks by id', () => {
        const round1 = makeTurn({
            id: 'r1',
            post_id: 'post_1',
            sequence: 1,
            content: [{type: 'tool_use', id: 'tc_x', name: 'lookup', status: 'success'}],
        });
        const result = makeTurn({
            id: 'tr1',
            role: 'tool_result',
            post_id: null,
            sequence: 2,
            content: [{type: 'tool_result', tool_use_id: 'tc_x', content: 'ok'}],
        });
        const rounds = buildRoundsFromTurns(makeConversation([round1, result]), 'post_1');
        expect(rounds).toHaveLength(1);
        expect(rounds[0].toolCalls[0].result).toBe('ok');
    });

    test('returns empty when the post has no anchor turn', () => {
        const turn = makeTurn({post_id: 'post_other', content: []});
        const rounds = buildRoundsFromTurns(makeConversation([turn]), 'post_missing');
        expect(rounds).toEqual([]);
    });

    test('extracts reasoning per round, not aggregated across the response', () => {
        const round1 = makeTurn({
            id: 'r1',
            post_id: null,
            sequence: 1,
            content: [
                {type: 'thinking', text: 'thinking about round 1'},
                {type: 'text', text: 'preamble'},
            ],
        });
        const round2 = makeTurn({
            id: 'r2',
            post_id: 'post_1',
            sequence: 2,
            content: [
                {type: 'thinking', text: 'thinking about round 2'},
                {type: 'text', text: 'final answer'},
            ],
        });
        const rounds = buildRoundsFromTurns(makeConversation([round1, round2]), 'post_1');
        expect(rounds[0].reasoning.summary).toBe('thinking about round 1');
        expect(rounds[1].reasoning.summary).toBe('thinking about round 2');
    });

    // User-approval flow: tool_result is written after the anchor turn.
    test('pairs tool_result that lives at a sequence GREATER than the anchor', () => {
        const userTurn = makeTurn({id: 'u1', role: 'user', sequence: 1, post_id: 'user_post', content: []});
        const anchor = makeTurn({
            id: 'a1',
            post_id: 'post_1',
            sequence: 2,
            content: [
                {type: 'text', text: 'I need to call this.'},
                {type: 'tool_use', id: 'tc_after', name: 'lookup', status: 'success'},
            ],
        });

        const lateResult = makeTurn({
            id: 'tr1',
            role: 'tool_result',
            post_id: null,
            sequence: 3,
            content: [{type: 'tool_result', tool_use_id: 'tc_after', content: 'late result'}],
        });

        const rounds = buildRoundsFromTurns(
            makeConversation([userTurn, anchor, lateResult]),
            'post_1',
        );
        expect(rounds).toHaveLength(1);
        expect(rounds[0].toolCalls).toHaveLength(1);
        expect(rounds[0].toolCalls[0].id).toBe('tc_after');
        expect(rounds[0].toolCalls[0].result).toBe('late result');
    });

    test('renders a tool_use with no matching tool_result and undefined result', () => {
        const anchor = makeTurn({
            id: 'a1',
            post_id: 'post_1',
            sequence: 1,
            content: [{type: 'tool_use', id: 'tc_lonely', name: 'search', status: 'pending'}],
        });
        const rounds = buildRoundsFromTurns(makeConversation([anchor]), 'post_1');
        expect(rounds).toHaveLength(1);
        expect(rounds[0].toolCalls).toHaveLength(1);
        expect(rounds[0].toolCalls[0].id).toBe('tc_lonely');
        expect(rounds[0].toolCalls[0].result).toBeUndefined();
    });

    // Continuation: demoted prior anchor (post_id cleared) must still render
    // as a prior round.
    test('includes a demoted prior anchor turn between the user turn and the new anchor', () => {
        const userTurn = makeTurn({id: 'u1', role: 'user', sequence: 1, post_id: 'user_post', content: []});

        const demoted = makeTurn({
            id: 'a_old',
            post_id: null,
            sequence: 2,
            content: [
                {type: 'text', text: 'Let me look that up.'},
                {type: 'tool_use', id: 'tc1', name: 'search', status: 'success'},
            ],
        });
        const result = makeTurn({
            id: 'tr1',
            role: 'tool_result',
            post_id: null,
            sequence: 3,
            content: [{type: 'tool_result', tool_use_id: 'tc1', content: 'channel data'}],
        });
        const newAnchor = makeTurn({
            id: 'a_new',
            post_id: 'post_1',
            sequence: 4,
            content: [{type: 'text', text: 'Found 5 channels.'}],
        });

        const rounds = buildRoundsFromTurns(
            makeConversation([userTurn, demoted, result, newAnchor]),
            'post_1',
        );
        expect(rounds).toHaveLength(2);
        expect(rounds[0].id).toBe('a_old');
        expect(rounds[0].text).toBe('Let me look that up.');
        expect(rounds[0].toolCalls).toHaveLength(1);
        expect(rounds[0].toolCalls[0].result).toBe('channel data');
        expect(rounds[1].id).toBe('a_new');
        expect(rounds[1].text).toBe('Found 5 channels.');
    });

    // Sibling posts in the same conversation must not contribute rounds.
    test('does not include rounds belonging to a sibling post anchor', () => {
        const userA = makeTurn({id: 'uA', role: 'user', sequence: 1, post_id: 'user_a', content: []});
        const anchorA = makeTurn({
            id: 'aA',
            post_id: 'post_a',
            sequence: 2,
            content: [{type: 'text', text: 'Response A.'}],
        });
        const userB = makeTurn({id: 'uB', role: 'user', sequence: 3, post_id: 'user_b', content: []});
        const anchorB = makeTurn({
            id: 'aB',
            post_id: 'post_b',
            sequence: 4,
            content: [{type: 'text', text: 'Response B.'}],
        });

        const conv = makeConversation([userA, anchorA, userB, anchorB]);
        const roundsA = buildRoundsFromTurns(conv, 'post_a');
        const roundsB = buildRoundsFromTurns(conv, 'post_b');
        expect(roundsA).toHaveLength(1);
        expect(roundsA[0].text).toBe('Response A.');
        expect(roundsB).toHaveLength(1);
        expect(roundsB[0].text).toBe('Response B.');
    });
});
