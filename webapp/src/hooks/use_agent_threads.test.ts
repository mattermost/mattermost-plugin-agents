// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook, act, waitFor} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import type {AIThread} from '@/types/ai_thread';

import {groupDMThreadsByBot, useAgentThreads, TITLE_RETRY_DELAY_MS} from './use_agent_threads';

jest.mock('@/client', () => ({
    getAIThreads: jest.fn(),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const {getAIThreads} = require('@/client');
const mockGetAIThreads = getAIThreads as jest.Mock;

const makeBot = (id: string, dmChannelID: string): LLMBot => ({id, dmChannelID} as LLMBot);

const makeThread = (overrides: Partial<AIThread>): AIThread => ({
    id: 't',
    channel_id: 'dm_a',
    bot_id: 'a',
    root_post_id: 'root',
    title: '',
    turn_count: 1,
    update_at: 1,
    ...overrides,
});

function deferred<T>() {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
}

describe('groupDMThreadsByBot', () => {
    const bots = [makeBot('a', 'dm_a'), makeBot('b', 'dm_b'), makeBot('nodm', '')];

    const cases: Array<{name: string; threads: AIThread[]; expected: Record<string, string[]>}> = [
        {
            name: 'groups by bot',
            threads: [
                makeThread({id: '1', bot_id: 'a', channel_id: 'dm_a'}),
                makeThread({id: '2', bot_id: 'b', channel_id: 'dm_b'}),
            ],
            expected: {a: ['1'], b: ['2']},
        },
        {
            name: 'drops mention threads outside the DM channel',
            threads: [
                makeThread({id: '1', channel_id: 'dm_a'}),
                makeThread({id: '2', channel_id: 'other'}),
                makeThread({id: '3', channel_id: null}),
            ],
            expected: {a: ['1']},
        },
        {
            name: 'fails closed when the bot has no dmChannelID',
            threads: [
                makeThread({id: '1', bot_id: 'nodm', channel_id: ''}),
                makeThread({id: '2', bot_id: 'nodm', channel_id: null}),
                makeThread({id: '3', bot_id: 'nodm', channel_id: 'anything'}),
            ],
            expected: {},
        },
        {
            name: 'drops threads for unknown bots',
            threads: [makeThread({id: '1', bot_id: 'ghost', channel_id: 'dm_a'})],
            expected: {},
        },
        {
            name: 'drops threads without root_post_id',
            threads: [
                makeThread({id: '1', root_post_id: null}),
                makeThread({id: '2', root_post_id: ''}),
                makeThread({id: '3'}),
            ],
            expected: {a: ['3']},
        },
        {
            name: 'sorts by update_at descending',
            threads: [
                makeThread({id: 'old', update_at: 1}),
                makeThread({id: 'new', update_at: 3}),
                makeThread({id: 'mid', update_at: 2}),
            ],
            expected: {a: ['new', 'mid', 'old']},
        },
    ];

    test.each(cases)('$name', ({threads, expected}) => {
        const result = groupDMThreadsByBot(threads, bots);
        const ids = Object.fromEntries(Object.entries(result).map(([k, v]) => [k, v.map((t) => t.id)]));
        expect(ids).toEqual(expected);
    });
});

describe('useAgentThreads', () => {
    const bots = [makeBot('a', 'dm_a')];

    beforeEach(() => {
        mockGetAIThreads.mockReset();
    });

    test('loads and groups threads', async () => {
        mockGetAIThreads.mockResolvedValue([makeThread({id: '1'})]);
        const {result} = renderHook(() => useAgentThreads(bots));
        expect(result.current.loading).toBe(true);
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.threadsByBot.a.map((t) => t.id)).toEqual(['1']);
        expect(result.current.error).toBeNull();
    });

    test('simultaneous refreshes from two instances issue one request and both update', async () => {
        const pending = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValue(pending.promise);
        const a = renderHook(() => useAgentThreads(bots));
        const b = renderHook(() => useAgentThreads(bots));
        await act(async () => null);
        mockGetAIThreads.mockClear();

        // Same synchronous turn, as one websocket event notifying both subscribers.
        act(() => {
            a.result.current.refresh();
            b.result.current.refresh();
        });
        expect(mockGetAIThreads).toHaveBeenCalledTimes(1);

        await act(async () => {
            pending.resolve([makeThread({id: 'shared'})]);
        });
        expect(a.result.current.threadsByBot.a.map((t) => t.id)).toEqual(['shared']);
        expect(b.result.current.threadsByBot.a.map((t) => t.id)).toEqual(['shared']);
    });

    test('propagates errors instead of returning an empty list', async () => {
        mockGetAIThreads.mockRejectedValue(new Error('boom'));
        const {result} = renderHook(() => useAgentThreads(bots));
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.error?.message).toBe('boom');
    });

    test('ignores a superseded response', async () => {
        const first = deferred<AIThread[]>();
        const second = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

        const {result} = renderHook(() => useAgentThreads(bots));
        act(() => {
            result.current.refresh();
        });

        await act(async () => {
            second.resolve([makeThread({id: 'new'})]);
        });
        await act(async () => {
            first.resolve([makeThread({id: 'stale'})]);
        });

        expect(result.current.threadsByBot.a.map((t) => t.id)).toEqual(['new']);
    });

    test('a stale error does not overwrite newer data', async () => {
        const first = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValueOnce(first.promise).mockResolvedValueOnce([makeThread({id: 'new'})]);

        const {result} = renderHook(() => useAgentThreads(bots));
        await act(async () => {
            await result.current.refresh();
        });
        await act(async () => {
            first.reject(new Error('stale'));
        });

        expect(result.current.error).toBeNull();
        expect(result.current.threadsByBot.a).toHaveLength(1);
    });

    test('does not set state after unmount', async () => {
        const pending = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValue(pending.promise);
        const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {
            // silence
        });

        const {unmount} = renderHook(() => useAgentThreads(bots));
        unmount();
        await act(async () => {
            pending.resolve([makeThread({id: '1'})]);
        });

        expect(errorSpy).not.toHaveBeenCalled();
        errorSpy.mockRestore();
    });

    describe('refreshWithTitleRetry', () => {
        beforeEach(() => {
            jest.useFakeTimers();
        });
        afterEach(() => {
            jest.useRealTimers();
        });

        test('refetches immediately and once more after the delay', async () => {
            mockGetAIThreads.mockResolvedValue([]);
            const {result} = renderHook(() => useAgentThreads(bots));
            await act(async () => {
                await Promise.resolve();
            });
            expect(mockGetAIThreads).toHaveBeenCalledTimes(1);

            await act(async () => {
                await result.current.refreshWithTitleRetry();
            });
            expect(mockGetAIThreads).toHaveBeenCalledTimes(2);

            await act(async () => {
                jest.advanceTimersByTime(TITLE_RETRY_DELAY_MS - 1);
            });
            expect(mockGetAIThreads).toHaveBeenCalledTimes(2);

            await act(async () => {
                jest.advanceTimersByTime(1);
            });
            expect(mockGetAIThreads).toHaveBeenCalledTimes(3);

            await act(async () => {
                jest.advanceTimersByTime(TITLE_RETRY_DELAY_MS * 3);
            });
            expect(mockGetAIThreads).toHaveBeenCalledTimes(3);
        });

        test('clears the delayed retry on unmount', async () => {
            mockGetAIThreads.mockResolvedValue([]);
            const {result, unmount} = renderHook(() => useAgentThreads(bots));
            await act(async () => {
                await result.current.refreshWithTitleRetry();
            });
            const calls = mockGetAIThreads.mock.calls.length;
            unmount();
            jest.advanceTimersByTime(TITLE_RETRY_DELAY_MS * 2);
            expect(mockGetAIThreads).toHaveBeenCalledTimes(calls);
        });
    });
});
