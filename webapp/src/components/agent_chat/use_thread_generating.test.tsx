// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, renderHook} from '@testing-library/react';

import {
    MAX_TRACKED_POSTS,
    STALE_GENERATING_MS,
    clearGeneratingPosts,
    recordPostUpdate,
    useGeneratingThreadRoots,
    useIsThreadGenerating,
} from './use_thread_generating';

const post = (id: string, rootId: string, requester: string) => ({
    id,
    channel_id: 'dm1',
    root_id: rootId,
    user_id: 'bot1',
    props: {llm_requester_user_id: requester},
});

const makeStore = (posts: Record<string, ReturnType<typeof post>>) => createStore(() => ({
    entities: {users: {currentUserId: 'me'}, posts: {posts}},
}));

const store = makeStore({
    reply1: post('reply1', 'rootA', 'me'),
    reply2: post('reply2', 'rootB', 'someone-else'),
    rootPostWithReply: post('rootPostWithReply', '', 'me'),
});

const wrapperFor = (s: typeof store) => ({children}: {children: React.ReactNode}) => <Provider store={s}>{children}</Provider>;
const wrapper = wrapperFor(store);
const update = (postId: string, control: string, s = store) => recordPostUpdate(s.getState() as never, {event: 'postupdate', data: {post_id: postId, control}} as never);

describe('useGeneratingThreadRoots', () => {
    beforeEach(() => {
        act(() => clearGeneratingPosts());
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('lists the roots of my chats while their agent generates, and drops them at the end', () => {
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});
        expect(Array.from(result.current)).toEqual([]);

        act(() => update('reply1', 'start'));
        expect(Array.from(result.current)).toEqual(['rootA']);

        act(() => update('reply1', 'end'));
        expect(Array.from(result.current)).toEqual([]);
    });

    test('a cancel (the requester pressed Stop) ends the generation', () => {
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});
        act(() => update('reply1', 'start'));

        act(() => update('reply1', 'cancel'));
        expect(Array.from(result.current)).toEqual([]);
    });

    test('other people\'s generations and posts the store does not have are ignored', () => {
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});

        act(() => {
            update('reply2', 'start');
            update('unknown', 'start');
        });

        expect(Array.from(result.current)).toEqual([]);
    });

    test('a root post that is itself generating counts as its own root', () => {
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});

        act(() => update('rootPostWithReply', 'start'));

        expect(Array.from(result.current)).toEqual(['rootPostWithReply']);
    });

    test('a websocket reconnect clears generations whose end may have been missed', () => {
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});
        act(() => update('reply1', 'start'));
        expect(Array.from(result.current)).toEqual(['rootA']);

        act(() => clearGeneratingPosts());
        expect(Array.from(result.current)).toEqual([]);

        // A stream still running is recorded again by its next chunk.
        act(() => update('reply1', 'progress'));
        expect(Array.from(result.current)).toEqual(['rootA']);
    });

    test('a generation with no events for too long is dropped when the next event arrives', () => {
        const now = jest.spyOn(Date, 'now').mockReturnValue(1_000_000);
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper});
        act(() => update('reply1', 'start'));

        now.mockReturnValue(1_000_000 + STALE_GENERATING_MS + 1);
        act(() => update('rootPostWithReply', 'start'));
        expect(Array.from(result.current)).toEqual(['rootPostWithReply']);
    });

    test('other people\'s posts are not recorded, so a busy channel cannot crowd out the user\'s own generation', () => {
        const others = Object.fromEntries(Array.from({length: MAX_TRACKED_POSTS + 5}, (_, i) => [`other${i}`, post(`other${i}`, `root${i}`, 'someone-else')]));
        const busy = makeStore({...others, reply1: post('reply1', 'rootA', 'me')});
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper: wrapperFor(busy)});

        act(() => {
            update('reply1', 'start', busy);
            Object.keys(others).forEach((id) => update(id, 'start', busy));
        });
        expect(Array.from(result.current)).toEqual(['rootA']);
    });
});

describe('useIsThreadGenerating', () => {
    beforeEach(() => {
        act(() => clearGeneratingPosts());
    });

    test.each([
        {name: 'the generating thread', rootId: 'rootA', expected: true},
        {name: 'another thread', rootId: 'rootB', expected: false},
        {name: 'a new chat with no root', rootId: '', expected: false},
    ])('$name', ({rootId, expected}) => {
        const {result} = renderHook(() => useIsThreadGenerating(rootId), {wrapper});
        act(() => update('reply1', 'start'));
        expect(result.current).toBe(expected);
    });
});
