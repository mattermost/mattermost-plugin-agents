// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, act, waitFor} from '@testing-library/react';

import {Composition} from '@/types/conversation';

import {invalidateConversation} from './use_conversation';

import {useConversationContext, invalidateConversationContext, clearConversationContextCache} from './use_conversation_context';

jest.mock('@/client', () => ({
    getConversationContext: jest.fn(),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const {getConversationContext} = require('@/client');

function makeComposition(overrides: Partial<Composition> = {}): Composition {
    return {
        components: [
            {source: 'system', proportion: 0.2, tokens: 200},
            {source: 'history', proportion: 0.5, tokens: 500},
            {source: 'image', proportion: 0.3, tokens: 300},
        ],
        total: 1000,
        total_source: 'provider',
        input_token_limit: 200000,
        model: 'claude-sonnet-4-5',
        ...overrides,
    };
}

beforeEach(() => {
    clearConversationContextCache();
    getConversationContext.mockReset();
});

describe('useConversationContext', () => {
    test('returns null without fetching when conversationId is undefined', () => {
        const noId: string | undefined = void 0; // eslint-disable-line no-void
        const {result} = renderHook(() => useConversationContext(noId));

        expect(result.current.composition).toBeNull();
        expect(result.current.loading).toBe(false);
        expect(result.current.error).toBeNull();
        expect(getConversationContext).not.toHaveBeenCalled();
    });

    test('fetches and returns composition', async () => {
        const fixture = makeComposition();
        getConversationContext.mockResolvedValue(fixture);

        const {result} = renderHook(() => useConversationContext('conv_123'));

        expect(result.current.loading).toBe(true);

        await waitFor(() => {
            expect(result.current.loading).toBe(false);
        });

        expect(result.current.composition).toEqual(fixture);
        expect(result.current.error).toBeNull();
        expect(getConversationContext).toHaveBeenCalledWith('conv_123');
    });

    test('returns cached data without duplicate fetch', async () => {
        const fixture = makeComposition();
        getConversationContext.mockResolvedValue(fixture);

        const {result: resultA} = renderHook(() => useConversationContext('conv_123'));
        await waitFor(() => {
            expect(resultA.current.composition).toEqual(fixture);
        });

        const {result: resultB} = renderHook(() => useConversationContext('conv_123'));
        expect(resultB.current.composition).toEqual(fixture);
        expect(resultB.current.loading).toBe(false);
        expect(getConversationContext).toHaveBeenCalledTimes(1);
    });

    test('re-fetches after invalidateConversationContext', async () => {
        const v1 = makeComposition({total: 1000});
        const v2 = makeComposition({total: 4000});
        getConversationContext.
            mockResolvedValueOnce(v1).
            mockResolvedValueOnce(v2);

        const {result} = renderHook(() => useConversationContext('conv_123'));
        await waitFor(() => {
            expect(result.current.composition).toEqual(v1);
        });

        act(() => {
            invalidateConversationContext('conv_123');
        });

        await waitFor(() => {
            expect(result.current.composition).toEqual(v2);
        });
        expect(getConversationContext).toHaveBeenCalledTimes(2);
    });

    test('sets error state when fetch fails', async () => {
        const err = new Error('boom');
        getConversationContext.mockRejectedValue(err);

        const {result} = renderHook(() => useConversationContext('conv_123'));

        await waitFor(() => {
            expect(result.current.error).toBe(err);
        });
        expect(result.current.composition).toBeNull();
        expect(result.current.loading).toBe(false);
    });

    test('invalidates context cache when invalidateConversation fires for same id', async () => {
        // The ring stayed stuck on the first fetch in production because
        // stream-end called invalidateConversation but the context cache
        // had no idea — locking this in so the auto-fanout can't regress.
        const v1 = makeComposition({total: 1000});
        const v2 = makeComposition({total: 4000});
        getConversationContext.
            mockResolvedValueOnce(v1).
            mockResolvedValueOnce(v2);

        const {result} = renderHook(() => useConversationContext('conv_123'));
        await waitFor(() => {
            expect(result.current.composition).toEqual(v1);
        });

        act(() => {
            invalidateConversation('conv_123');
        });

        await waitFor(() => {
            expect(result.current.composition).toEqual(v2);
        });
        expect(getConversationContext).toHaveBeenCalledTimes(2);
    });

    test('deduplicates concurrent fetches', async () => {
        let resolveIt: (c: Composition) => void;
        getConversationContext.mockReturnValue(new Promise<Composition>((r) => {
            resolveIt = r;
        }));
        const fixture = makeComposition();

        const {result: a} = renderHook(() => useConversationContext('conv_123'));
        const {result: b} = renderHook(() => useConversationContext('conv_123'));

        expect(a.current.loading).toBe(true);
        expect(b.current.loading).toBe(true);

        await act(async () => {
            resolveIt!(fixture);
        });

        await waitFor(() => {
            expect(a.current.composition).toEqual(fixture);
        });
        await waitFor(() => {
            expect(b.current.composition).toEqual(fixture);
        });
        expect(getConversationContext).toHaveBeenCalledTimes(1);
    });
});
