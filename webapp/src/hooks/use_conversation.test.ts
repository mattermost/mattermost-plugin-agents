// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, act, waitFor} from '@testing-library/react';

import {ConversationResponse} from '@/types/conversation';

import {useConversation, useTurnForPost, invalidateConversation, clearConversationCache} from './use_conversation';

// Mock the client module
jest.mock('@/client', () => ({
    getConversation: jest.fn(),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const {getConversation} = require('@/client');

function makeConversation(overrides: Partial<ConversationResponse> = {}): ConversationResponse {
    return {
        id: 'conv_123',
        user_id: 'user_456',
        bot_id: 'bot_789',
        channel_id: 'chan_abc',
        root_post_id: 'post_root',
        title: 'Test conversation',
        operation: 'conversation',
        turns: [
            {
                id: 'turn_1',
                conversation_id: 'conv_123',
                post_id: 'post_001',
                role: 'user',
                content: [{type: 'text', text: 'Hello'}],
                tokens_in: 0,
                tokens_out: 0,
                sequence: 1,
                created_at: 1000,
            },
            {
                id: 'turn_2',
                conversation_id: 'conv_123',
                post_id: 'post_002',
                role: 'assistant',
                content: [{type: 'text', text: 'Hi there'}],
                tokens_in: 100,
                tokens_out: 50,
                sequence: 2,
                created_at: 2000,
            },
            {
                id: 'turn_3',
                conversation_id: 'conv_123',
                post_id: 'post_003',
                role: 'assistant',
                content: [{type: 'text', text: 'Anything else?'}],
                tokens_in: 150,
                tokens_out: 30,
                sequence: 3,
                created_at: 3000,
            },
        ],
        ...overrides,
    };
}

beforeEach(() => {
    clearConversationCache();
    getConversation.mockReset();
});

describe('useConversation', () => {
    test('returns null without fetching when conversationId is undefined', () => {
        const noId: string | undefined = void 0; // eslint-disable-line no-void
        const {result} = renderHook(() => useConversation(noId));

        expect(result.current.conversation).toBeNull();
        expect(result.current.loading).toBe(false);
        expect(result.current.error).toBeNull();
        expect(getConversation).not.toHaveBeenCalled();
    });

    test('fetches and returns conversation data', async () => {
        const fixture = makeConversation();
        getConversation.mockResolvedValue(fixture);

        const {result} = renderHook(() => useConversation('conv_123'));

        // Initially loading
        expect(result.current.loading).toBe(true);
        expect(result.current.conversation).toBeNull();

        await waitFor(() => {
            expect(result.current.loading).toBe(false);
        });

        expect(result.current.conversation).toEqual(fixture);
        expect(result.current.error).toBeNull();
        expect(getConversation).toHaveBeenCalledWith('conv_123');
    });

    test('returns cached data without duplicate fetch for same ID', async () => {
        const fixture = makeConversation();
        getConversation.mockResolvedValue(fixture);

        // First hook fetches and caches
        const {result: resultA} = renderHook(() => useConversation('conv_123'));
        await waitFor(() => {
            expect(resultA.current.conversation).toEqual(fixture);
        });

        // Second hook with same ID gets cached data immediately
        const {result: resultB} = renderHook(() => useConversation('conv_123'));
        expect(resultB.current.conversation).toEqual(fixture);
        expect(resultB.current.loading).toBe(false);
        expect(getConversation).toHaveBeenCalledTimes(1);
    });

    test('fetches again when conversationId changes', async () => {
        const fixture1 = makeConversation({id: 'conv_123', title: 'First'});
        const fixture2 = makeConversation({id: 'conv_456', title: 'Second'});
        getConversation.mockImplementation((id: string) => {
            if (id === 'conv_123') {
                return Promise.resolve(fixture1);
            }
            return Promise.resolve(fixture2);
        });

        const {result, rerender} = renderHook(
            ({id}: {id: string}) => useConversation(id),
            {initialProps: {id: 'conv_123'}},
        );

        await waitFor(() => {
            expect(result.current.conversation).toEqual(fixture1);
        });

        rerender({id: 'conv_456'});

        await waitFor(() => {
            expect(result.current.conversation).toEqual(fixture2);
        });

        expect(getConversation).toHaveBeenCalledTimes(2);
        expect(getConversation).toHaveBeenCalledWith('conv_123');
        expect(getConversation).toHaveBeenCalledWith('conv_456');
    });

    test('re-fetches after invalidateConversation is called', async () => {
        const fixtureV1 = makeConversation({title: 'Version 1'});
        const fixtureV2 = makeConversation({title: 'Version 2'});
        getConversation.
            mockResolvedValueOnce(fixtureV1).
            mockResolvedValueOnce(fixtureV2);

        const {result} = renderHook(() => useConversation('conv_123'));

        await waitFor(() => {
            expect(result.current.conversation).toEqual(fixtureV1);
        });

        act(() => {
            invalidateConversation('conv_123');
        });

        await waitFor(() => {
            expect(result.current.conversation).toEqual(fixtureV2);
        });

        expect(getConversation).toHaveBeenCalledTimes(2);
    });

    test('sets error state when fetch fails', async () => {
        const fetchError = new Error('Network failure');
        getConversation.mockRejectedValue(fetchError);

        const {result} = renderHook(() => useConversation('conv_123'));

        await waitFor(() => {
            expect(result.current.error).toBe(fetchError);
        });

        expect(result.current.conversation).toBeNull();
        expect(result.current.loading).toBe(false);
    });

    test('deduplicates concurrent fetches for the same ID', async () => {
        const fixture = makeConversation();
        let resolvePromise: (value: ConversationResponse) => void;
        getConversation.mockReturnValue(
            new Promise<ConversationResponse>((resolve) => {
                resolvePromise = resolve;
            }),
        );

        const {result: resultA} = renderHook(() => useConversation('conv_123'));
        const {result: resultB} = renderHook(() => useConversation('conv_123'));

        expect(resultA.current.loading).toBe(true);
        expect(resultB.current.loading).toBe(true);

        await act(async () => {
            resolvePromise!(fixture);
        });

        await waitFor(() => {
            expect(resultA.current.conversation).toEqual(fixture);
        });
        await waitFor(() => {
            expect(resultB.current.conversation).toEqual(fixture);
        });

        expect(getConversation).toHaveBeenCalledTimes(1);
    });

    test('unblocks sibling hooks when a concurrent fetch fails', async () => {
        const fetchError = new Error('Network failure');
        let rejectPromise: (reason: Error) => void;
        getConversation.mockReturnValueOnce(
            new Promise<ConversationResponse>((_, reject) => {
                rejectPromise = reject;
            }),
        );

        const {result: resultA} = renderHook(() => useConversation('conv_123'));
        const {result: resultB} = renderHook(() => useConversation('conv_123'));

        expect(resultA.current.loading).toBe(true);
        expect(resultB.current.loading).toBe(true);

        await act(async () => {
            rejectPromise!(fetchError);
        });

        // Sibling must not stay stuck in loading; both should adopt the error.
        await waitFor(() => {
            expect(resultA.current.error).toBe(fetchError);
        });
        await waitFor(() => {
            expect(resultB.current.error).toBe(fetchError);
        });

        expect(resultA.current.loading).toBe(false);
        expect(resultB.current.loading).toBe(false);

        // No retry storm: neither hook re-fetches after the failure.
        expect(getConversation).toHaveBeenCalledTimes(1);
    });

    test('invalidateConversation clears cached errors and retries', async () => {
        const fetchError = new Error('Network failure');
        getConversation.mockRejectedValueOnce(fetchError);

        const {result} = renderHook(() => useConversation('conv_123'));

        await waitFor(() => {
            expect(result.current.error).toBe(fetchError);
        });

        const fixture = makeConversation();
        getConversation.mockResolvedValueOnce(fixture);

        act(() => {
            invalidateConversation('conv_123');
        });

        await waitFor(() => {
            expect(result.current.conversation).toEqual(fixture);
        });
        expect(result.current.error).toBeNull();
    });
});

describe('useTurnForPost', () => {
    test('returns the matching turn by post_id', () => {
        const conversation = makeConversation();
        const {result} = renderHook(() => useTurnForPost(conversation, 'post_002'));

        expect(result.current).not.toBeNull();
        expect(result.current!.post_id).toBe('post_002');
        expect(result.current!.id).toBe('turn_2');
    });

    test('returns null when no turn matches the post_id', () => {
        const conversation = makeConversation();
        const {result} = renderHook(() => useTurnForPost(conversation, 'nonexistent_post'));

        expect(result.current).toBeNull();
    });

    test('returns null when conversation is null', () => {
        const {result} = renderHook(() => useTurnForPost(null, 'post_001'));

        expect(result.current).toBeNull();
    });
});
