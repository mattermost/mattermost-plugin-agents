// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {renderHook, act, waitFor} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import manifest from '@/manifest';
import {aiThreads} from '@/redux';
import type {AIThread} from '@/types/ai_thread';

import {
    THREADS_REFRESH_DEBOUNCE_MS,
    createAgentThreadsSync,
    fetchAIThreads,
    groupDMThreadsByBot,
    renameAIThread,
    selectAIThreads,
    useAgentThreads,
} from './use_agent_threads';

jest.mock('@/client', () => ({
    getAIThreads: jest.fn(),
    renameConversation: jest.fn(),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const {getAIThreads, renameConversation} = require('@/client');
const mockGetAIThreads = getAIThreads as jest.Mock;
const mockRenameConversation = renameConversation as jest.Mock;

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
            name: 'keeps the server order (latest turn first) even when update_at disagrees',
            threads: [
                makeThread({id: 'latest-turn', update_at: 1}),
                makeThread({id: 'renamed-later', update_at: 3}),
                makeThread({id: 'oldest', update_at: 2}),
            ],
            expected: {a: ['latest-turn', 'renamed-later', 'oldest']},
        },
    ];

    test.each(cases)('$name', ({threads, expected}) => {
        const result = groupDMThreadsByBot(threads, bots);
        const ids = Object.fromEntries(Object.entries(result).map(([k, v]) => [k, v.map((t) => t.id)]));
        expect(ids).toEqual(expected);
    });
});

interface TestPost {
    id: string;
    channel_id: string;
    user_id: string;
    props?: Record<string, unknown>;
}

// The plugin's real thread-list reducer under its plugin key, plus the host entities the sync reads.
const makeStore = ({bots = [] as LLMBot[], posts = {} as Record<string, TestPost>} = {}) => {
    const plugin = combineReducers({
        aiThreads,
        bots: (state: LLMBot[] = bots) => state,
    });
    const entities = (state = {users: {currentUserId: 'me'}, posts: {posts}}) => state;
    return createStore(combineReducers({[`plugins-${manifest.id}`]: plugin, entities} as any));
};

type TestStore = ReturnType<typeof makeStore>;

const titles = (store: TestStore) => selectAIThreads(store.getState())?.map((t) => `${t.id}:${t.title}`);

const wrapperFor = (store: TestStore) => ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;

describe('stored AI threads', () => {
    beforeEach(() => {
        mockGetAIThreads.mockReset();
        mockRenameConversation.mockReset();
    });

    test('views mounted together share one request and read the same list', async () => {
        const pending = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValue(pending.promise);
        const store = makeStore();
        const bots = [makeBot('a', 'dm_a')];
        const a = renderHook(() => useAgentThreads(bots), {wrapper: wrapperFor(store)});
        const b = renderHook(() => useAgentThreads(bots), {wrapper: wrapperFor(store)});
        expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
        expect(a.result.current.loading).toBe(true);

        await act(async () => {
            pending.resolve([makeThread({id: 'shared'})]);
        });
        expect(a.result.current.threadsByBot.a.map((t) => t.id)).toEqual(['shared']);
        expect(b.result.current.threadsByBot.a.map((t) => t.id)).toEqual(['shared']);
        expect(a.result.current.loading).toBe(false);
    });

    test('a failed load is an error, not an empty list', async () => {
        jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetAIThreads.mockRejectedValue(new Error('boom'));
        const store = makeStore();
        const {result} = renderHook(() => useAgentThreads([makeBot('a', 'dm_a')]), {wrapper: wrapperFor(store)});
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.error).toBe(true);
        expect(result.current.threads).toBeNull();
        jest.restoreAllMocks();
    });

    test.each([
        {name: 'a superseded response', outcome: 'resolve' as const},
        {name: 'a superseded failure', outcome: 'reject' as const},
    ])('$name does not overwrite newer data', async ({outcome}) => {
        jest.spyOn(console, 'error').mockImplementation(() => null);
        const first = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValueOnce(first.promise).mockResolvedValueOnce([makeThread({id: 'new'})]);
        const store = makeStore();

        const firstFetch = fetchAIThreads(store.dispatch);
        await fetchAIThreads(store.dispatch);
        if (outcome === 'resolve') {
            first.resolve([makeThread({id: 'stale'})]);
        } else {
            first.reject(new Error('stale'));
        }
        await firstFetch;

        expect(titles(store)).toEqual(['new:']);
        expect((store.getState() as any)[`plugins-${manifest.id}`].aiThreads.error).toBe(false);
        jest.restoreAllMocks();
    });

    test('a title set while a fetch is in flight survives its older response', async () => {
        const pending = deferred<AIThread[]>();
        mockGetAIThreads.mockReturnValue(pending.promise);
        const store = makeStore();
        const sync = createAgentThreadsSync(store as never);

        const fetching = fetchAIThreads(store.dispatch);
        sync.handleConversationUpdated({conversation_id: 'c1', title: 'Generated title'});
        pending.resolve([makeThread({id: 'c1', title: ''})]);
        await fetching;

        expect(titles(store)).toEqual(['c1:Generated title']);
        sync.dispose();
    });

    describe('rename', () => {
        const loaded = async () => {
            mockGetAIThreads.mockResolvedValue([makeThread({id: 'c1', title: 'Old'}), makeThread({id: 'c2', title: 'Other'})]);
            const store = makeStore();
            await fetchAIThreads(store.dispatch);
            return store;
        };

        test('shows the new title before the server answers', async () => {
            const store = await loaded();
            const pending = deferred<void>();
            mockRenameConversation.mockReturnValue(pending.promise);

            const renaming = renameAIThread(store, 'c1', 'New');
            expect(titles(store)).toEqual(['c1:New', 'c2:Other']);

            pending.resolve();
            await renaming;
            expect(mockRenameConversation).toHaveBeenCalledWith('c1', 'New');
            expect(titles(store)).toEqual(['c1:New', 'c2:Other']);
        });

        test('a failed rename restores the old title and rejects', async () => {
            const store = await loaded();
            mockRenameConversation.mockRejectedValue(new Error('nope'));

            await expect(renameAIThread(store, 'c1', 'New')).rejects.toThrow('nope');
            expect(titles(store)).toEqual(['c1:Old', 'c2:Other']);
        });

        test('a failed rename keeps a newer title that arrived meanwhile', async () => {
            const store = await loaded();
            const pending = deferred<void>();
            mockRenameConversation.mockReturnValue(pending.promise);
            const sync = createAgentThreadsSync(store as never);

            const renaming = renameAIThread(store, 'c1', 'New');
            sync.handleConversationUpdated({conversation_id: 'c1', title: 'From elsewhere'});
            pending.reject(new Error('nope'));

            await expect(renaming).rejects.toThrow('nope');
            expect(titles(store)).toEqual(['c1:From elsewhere', 'c2:Other']);
            sync.dispose();
        });
    });

    describe('websocket sync', () => {
        beforeEach(() => {
            jest.useFakeTimers();
        });
        afterEach(() => {
            jest.useRealTimers();
        });

        const bots = [makeBot('a', 'dm_a')];
        const posts: Record<string, TestPost> = {
            mine: {id: 'mine', channel_id: 'dm_a', user_id: 'a'},
            requested: {id: 'requested', channel_id: 'town', user_id: 'a', props: {llm_requester_user_id: 'me'}},
            others: {id: 'others', channel_id: 'town', user_id: 'a', props: {llm_requester_user_id: 'someone'}},
        };

        const setup = async () => {
            mockGetAIThreads.mockResolvedValue([makeThread({id: 'c1'})]);
            const store = makeStore({bots, posts});
            await act(async () => {
                await fetchAIThreads(store.dispatch);
            });
            mockGetAIThreads.mockClear();
            return {store, sync: createAgentThreadsSync(store as never)};
        };
        const end = (postId: string) => ({data: {post_id: postId, control: 'end'}} as never);
        const settle = async () => {
            await act(async () => {
                jest.advanceTimersByTime(THREADS_REFRESH_DEBOUNCE_MS);
            });
        };

        test.each([
            {name: 'a reply in the user\'s agent DM', postId: 'mine', refetch: true},
            {name: 'a reply to the user\'s request in a channel', postId: 'requested', refetch: true},
            {name: 'a reply to someone else in a shared channel', postId: 'others', refetch: false},
            {name: 'a post the store does not have', postId: 'unknown', refetch: false},
        ])('a finished turn: $name refetches: $refetch', async ({postId, refetch}) => {
            const {sync} = await setup();
            sync.handlePostUpdate(end(postId));
            await settle();
            expect(mockGetAIThreads).toHaveBeenCalledTimes(refetch ? 1 : 0);
            sync.dispose();
        });

        test('a burst of finished turns costs one request', async () => {
            const {sync} = await setup();
            sync.handlePostUpdate(end('mine'));
            sync.handlePostUpdate(end('requested'));
            sync.handlePostUpdate(end('mine'));
            await settle();
            expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
            sync.dispose();
        });

        test('nothing is fetched for a list no view has loaded', async () => {
            const store = makeStore({bots, posts});
            const sync = createAgentThreadsSync(store as never);
            sync.handlePostUpdate(end('mine'));
            sync.handleReconnect();
            await settle();
            expect(mockGetAIThreads).not.toHaveBeenCalled();
            sync.dispose();
        });

        test('a title update renames the listed chat without a refetch', async () => {
            const {store, sync} = await setup();
            sync.handleConversationUpdated({conversation_id: 'c1', title: 'Generated'});
            await settle();
            expect(titles(store)).toEqual(['c1:Generated']);
            expect(mockGetAIThreads).not.toHaveBeenCalled();
            sync.dispose();
        });

        test('a title for a chat the list does not have yet fetches it', async () => {
            const {sync} = await setup();
            sync.handleConversationUpdated({conversation_id: 'new-chat', title: 'Generated'});
            await settle();
            expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
            sync.dispose();
        });

        test('a reconnect refetches a loaded list', async () => {
            const {sync} = await setup();
            sync.handleReconnect();
            await settle();
            expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
            sync.dispose();
        });
    });
});
