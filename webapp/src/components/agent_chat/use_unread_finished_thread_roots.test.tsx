// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, renderHook} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {recordPostUpdate, resetGeneratingPosts, useGeneratingThreadRoots, useUnreadFinishedThreadRoots} from './use_thread_generating';

jest.mock('@/websocket', () => ({subscribeToAllPostUpdates: jest.fn()}));

const post = (id: string, rootId: string, requester = 'me') => ({id, channel_id: 'dm1', root_id: rootId, user_id: 'bot1', props: {llm_requester_user_id: requester}});

const makeStore = (posts: Record<string, unknown>) => createStore(() => ({
    entities: {users: {currentUserId: 'me'}, posts: {posts}},
}));

const update = (postId: string, control: string) => recordPostUpdate({data: {post_id: postId, control}} as never);

const renderRoots = (posts: Record<string, unknown>, openRootId = '') => renderHook(
    () => ({generating: useGeneratingThreadRoots(), unread: useUnreadFinishedThreadRoots(openRootId)}),
    {wrapper: ({children}: {children: React.ReactNode}) => <Provider store={makeStore(posts)}>{children}</Provider>},
);

describe('unread finished chats', () => {
    beforeEach(() => {
        act(() => resetGeneratingPosts());
    });

    test('a reply that ends is unread, in place of generating', () => {
        const {result} = renderRoots({p1: post('p1', 'root1')});

        act(() => update('p1', 'start'));
        expect(Array.from(result.current.generating)).toEqual(['root1']);
        expect(Array.from(result.current.unread)).toEqual([]);

        act(() => update('p1', 'end'));
        expect(Array.from(result.current.generating)).toEqual([]);
        expect(Array.from(result.current.unread)).toEqual(['root1']);
    });

    test.each([
        {name: 'a cancelled reply', control: 'cancel'},
        {name: 'an end that arrives with no generation (a replay)', control: 'end', skipStart: true},
    ])('$name is not unread', ({control, skipStart}) => {
        const {result} = renderRoots({p1: post('p1', 'root1')});

        if (!skipStart) {
            act(() => update('p1', 'start'));
        }
        act(() => update('p1', control));
        expect(Array.from(result.current.unread)).toEqual([]);
    });

    test('opening the chat reads it, and it stays read', () => {
        const posts = {p1: post('p1', 'root1')};
        const first = renderRoots(posts);
        act(() => update('p1', 'start'));
        act(() => update('p1', 'end'));
        expect(Array.from(first.result.current.unread)).toEqual(['root1']);
        first.unmount();

        const open = renderRoots(posts, 'root1');
        expect(Array.from(open.result.current.unread)).toEqual([]);
        open.unmount();

        const later = renderRoots(posts);
        expect(Array.from(later.result.current.unread)).toEqual([]);
    });

    test('a reply to someone else\'s request is not the user\'s unread', () => {
        const {result} = renderRoots({p1: post('p1', 'root1', 'someone-else')});

        act(() => update('p1', 'start'));
        act(() => update('p1', 'end'));
        expect(Array.from(result.current.unread)).toEqual([]);
    });
});
