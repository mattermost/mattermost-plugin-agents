// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ConversationResponse, Turn} from '@/types/conversation';

import {ToolCallStatus} from '../tool_types';

import {
    statusStringToEnum,
    extractToolCallsFromTurn,
    extractReasoningFromTurn,
    extractAnnotationsFromTurn,
    deriveApprovalStage,
    hasAutoApprovedTools,
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

describe('extractToolCallsFromTurn', () => {
    test('returns empty array when turn has no tool_use blocks', () => {
        const turn = makeTurn({content: [{type: 'text', text: 'hello'}]});
        const conv = makeConversation([turn]);
        expect(extractToolCallsFromTurn(turn, conv)).toEqual([]);
    });

    test('maps tool_use blocks to ToolCall[] with matching results', () => {
        const assistantTurn = makeTurn({
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: {city: 'NYC'}, status: 'success', shared: true},
                {type: 'tool_use', id: 'tc_2', name: 'search', input: {q: 'test'}, status: 'error', shared: false},
            ],
        });

        const resultTurn = makeTurn({
            id: 'turn_2',
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: '72F sunny', status: 'success'},
                {type: 'tool_result', tool_use_id: 'tc_2', content: 'not found', status: 'error'},
            ],
        });

        const conv = makeConversation([assistantTurn, resultTurn]);
        const result = extractToolCallsFromTurn(assistantTurn, conv);

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
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: null, status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        const result = extractToolCallsFromTurn(assistantTurn, conv);

        expect(result).toHaveLength(1);
        expect(result[0].arguments).toBeUndefined();
    });

    test('handles missing tool_result turn', () => {
        const assistantTurn = makeTurn({
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'get_weather', input: {city: 'NYC'}, status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        const result = extractToolCallsFromTurn(assistantTurn, conv);

        expect(result).toHaveLength(1);
        expect(result[0].result).toBeUndefined();
        expect(result[0].status).toBe(ToolCallStatus.Pending);
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

describe('deriveApprovalStage', () => {
    test('returns call when no tool_result turn follows', () => {
        const assistantTurn = makeTurn({
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'pending'},
            ],
        });
        const conv = makeConversation([assistantTurn]);
        expect(deriveApprovalStage(assistantTurn, conv)).toBe('call');
    });

    test('returns result when tool_result turn follows', () => {
        const assistantTurn = makeTurn({
            sequence: 1,
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'success'},
            ],
        });
        const resultTurn = makeTurn({
            id: 'turn_2',
            sequence: 2,
            role: 'tool_result',
            content: [
                {type: 'tool_result', tool_use_id: 'tc_1', content: 'found it', status: 'success'},
            ],
        });
        const conv = makeConversation([assistantTurn, resultTurn]);
        expect(deriveApprovalStage(assistantTurn, conv)).toBe('result');
    });

    test('returns call when turn has no tool_use blocks', () => {
        const assistantTurn = makeTurn({
            sequence: 1,
            content: [{type: 'text', text: 'hello'}],
        });
        const conv = makeConversation([assistantTurn]);
        expect(deriveApprovalStage(assistantTurn, conv)).toBe('call');
    });
});

describe('hasAutoApprovedTools', () => {
    test('returns false when no tool_use blocks have auto_approved status', () => {
        const turn = makeTurn({
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'pending'},
            ],
        });
        expect(hasAutoApprovedTools(turn)).toBe(false);
    });

    test('returns true when a tool_use block has auto_approved status', () => {
        const turn = makeTurn({
            content: [
                {type: 'tool_use', id: 'tc_1', name: 'search', status: 'auto_approved'},
            ],
        });
        expect(hasAutoApprovedTools(turn)).toBe(true);
    });

    test('returns false for empty content', () => {
        const turn = makeTurn({content: []});
        expect(hasAutoApprovedTools(turn)).toBe(false);
    });
});
