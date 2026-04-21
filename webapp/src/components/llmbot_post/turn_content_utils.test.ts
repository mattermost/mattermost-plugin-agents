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

describe('deriveApprovalStageForPost', () => {
    test('returns call when no tool_result turn follows', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('call');
    });

    test('returns result when tool_result turn follows (not shared)', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'success', shared: false},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'found it', status: 'success', shared: false},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('result');
    });

    test('returns call when every matching result has decided_at (auto_run_everywhere / DM)', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'auto_approved', shared: true},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'found it', status: 'auto_approved', shared: true, decided_at: 1000},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('call');
    });

    test('returns call when post has no tool_use blocks', () => {
        const anchor = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [{type: 'text', text: 'hello'}],
        });
        const conv = makeConversation([anchor]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('call');
    });

    // ToolApprovalSet auto-submits doToolResult([]) once every tool on a post
    // is Rejected and the stage is 'result'. In channels that kicks off a new
    // LLM round, which creates another pending post, which the UI flags as
    // all-rejected on the next conversation refetch — a loop that generates
    // dozens of Claude replies until the test times out. Returning 'call'
    // here short-circuits the auto-submit effect.
    test('returns call when every tool_use block is rejected', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_channel_info', status: 'rejected', shared: false},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'Tool call rejected by user', status: 'error', shared: false},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('call');
    });

    // "Keep Private" leaves Shared=false but sets decided_at, so the stage
    // transitions out of 'result' without needing a follow-up post heuristic.
    test('returns call after Keep Private records decided_at even when Shared stays false', () => {
        const assistantTurn = makeTurn({
            id: 'turn_1',
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'create_post', status: 'success', shared: false},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'posted', status: 'success', shared: false, decided_at: 2000},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('call');
    });

    test('returns result when some tool_use blocks executed even with rejected siblings', () => {
        const assistantTurn = makeTurn({
            post_id: 'post_1',
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'read', status: 'rejected', shared: false},
                {type: 'tool_use', id: 'tc_2', name: 'write', status: 'success', shared: false},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            post_id: null,
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'rejected', status: 'error', shared: false},
                {type: 'tool_result', tool_use_id: 'tc_2', content: 'ok', status: 'success', shared: false},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStageForPost(conv, 'post_1')).toBe('result');
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

