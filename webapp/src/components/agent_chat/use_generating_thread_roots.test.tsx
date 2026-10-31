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
    resetGeneratingPosts,
    setGeneratingStateSource,
    useGeneratingThreadRoots,
} from './use_thread_generating';

const post = (id: string, rootId: string, requester: string) => ({
    id,
    channel_id: 'dm1',
    root_id: rootId,
    user_id: 'bot1',
    props: {llm_requester_user_id: requester},
});

const store = createStore(() => ({
    entities: {
        users: {currentUserId: 'me'},
        posts: {
            posts: {
                reply1: post('reply1', 'rootA', 'me'),
                reply2: post('reply2', 'rootB', 'someone-else'),
                rootPostWithReply: post('rootPostWithReply', '', 'me'),
            },
        },
    },
}));

const wrapper = ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>;
const update = (postId: string, control: string) => recordPostUpdate({event: 'postupdate', data: {post_id: postId, control}} as never);

describe('useGeneratingThreadRoots', () => {
    beforeEach(() => {
        act(() => resetGeneratingPosts());
    });

    afterEach(() => {
        setGeneratingStateSource(null);
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
        const busy = createStore(() => ({
            entities: {users: {currentUserId: 'me'}, posts: {posts: {...others, reply1: post('reply1', 'rootA', 'me')}}},
        }));
        setGeneratingStateSource(busy.getState as never);
        const {result} = renderHook(() => useGeneratingThreadRoots(), {wrapper: ({children}: {children: React.ReactNode}) => <Provider store={busy}>{children}</Provider>});

        act(() => {
            update('reply1', 'start');
            Object.keys(others).forEach((id) => update(id, 'start'));
        });
        expect(Array.from(result.current)).toEqual(['rootA']);
    });
});
