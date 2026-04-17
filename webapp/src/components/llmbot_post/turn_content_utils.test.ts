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

    // Regression test for the multi-round display bug: tool rounds are
    // persisted as separate turns (no post_id) following the anchor
    // placeholder. The UI must aggregate tool calls from all of those
    // rounds, not only the anchor, so every call is visible to the user.
    test('aggregates tool calls across subsequent tool-round turns', () => {
        const anchorPlaceholder = makeTurn({
            id: 'anchor',
            post_id: 'post_1',
            sequence: 10,
            role: 'assistant',
            content: [], // final text would go here; in a mid-stream error it may be empty
        });
        const round1Assistant = makeTurn({
            id: 'r1a',
            post_id: null,
            sequence: 11,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_r1', name: 'search', input: {q: 'a'}, status: 'success', shared: true},
            ],
        });
        const round1Result = makeTurn({
            id: 'r1r',
            post_id: null,
            sequence: 12,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_r1', content: 'first', status: 'success', shared: true},
            ],
        });
        const round2Assistant = makeTurn({
            id: 'r2a',
            post_id: null,
            sequence: 13,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_r2', name: 'search', input: {q: 'b'}, status: 'success', shared: true},
            ],
        });
        const round2Result = makeTurn({
            id: 'r2r',
            post_id: null,
            sequence: 14,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_r2', content: 'second', status: 'success', shared: true},
            ],
        });

        const conv = makeConversation([
            anchorPlaceholder,
            round1Assistant,
            round1Result,
            round2Assistant,
            round2Result,
        ]);
        const result = extractToolCallsForPost(conv, 'post_1');

        expect(result).toHaveLength(2);
        expect(result[0]).toMatchObject({id: 'tc_r1', result: 'first'});
        expect(result[1]).toMatchObject({id: 'tc_r2', result: 'second'});
    });

    test('stops aggregation at the next user turn', () => {
        const anchor = makeTurn({
            id: 'anchor',
            post_id: 'post_1',
            sequence: 5,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_here', name: 'search', input: {}, status: 'success', shared: true},
            ],
        });
        const followResult = makeTurn({
            id: 'follow',
            post_id: null,
            sequence: 6,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_here', content: 'ok', status: 'success', shared: true},
            ],
        });
        const nextUser = makeTurn({
            id: 'nextu',
            post_id: 'post_user2',
            sequence: 7,
            role: 'user',
            content: [{type: 'text', text: 'follow-up question'}],
        });
        const laterAssistant = makeTurn({
            id: 'latera',
            post_id: null,
            sequence: 8,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_next_response', name: 'search', input: {}, status: 'pending'},
            ],
        });

        const conv = makeConversation([anchor, followResult, nextUser, laterAssistant]);
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

    test('returns call when every matching result is already shared', () => {
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
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'found it', status: 'auto_approved', shared: true},
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

    test('returns true when a later round contains an auto_approved tool_use', () => {
        const anchor = makeTurn({
            id: 'anchor',
            post_id: 'post_1',
            sequence: 1,
            content: [],
        });
        const round = makeTurn({
            id: 'round',
            post_id: null,
            sequence: 2,
            role: 'assistant',
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'auto_approved'},
            ],
        });
        const conv = makeConversation([anchor, round]);
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

