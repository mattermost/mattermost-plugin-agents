// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, renderHook} from '@testing-library/react';

import {updateRead} from '@/client';
import {handlePostedWebsocket} from '@/posted_events';

import {useMarkAgentThreadRead, useUnreadThreadRoots} from './use_agent_thread_read';

jest.mock('@/client', () => ({updateRead: jest.fn(() => Promise.resolve())}));

const mockViewedPosts = new Set<string>();
jest.mock('@/viewing', () => ({
    getViewingTracker: () => ({isViewed: (postId: string) => mockViewedPosts.has(postId)}),
}));

const mockUpdateRead = updateRead as jest.Mock;

const makeStore = (collapsedThreads: string, preference: string) => createStore(() => ({
    entities: {
        general: {config: collapsedThreads ? {CollapsedThreads: collapsedThreads} : {}},
        preferences: {myPreferences: preference ? {'display_settings--collapsed_reply_threads': {value: preference}} : {}},
        threads: {threads: {unread: {unread_replies: 1}, read: {unread_replies: 0}, open: {unread_replies: 3}}},
    },
}));

describe('useUnreadThreadRoots', () => {
    test.each([
        {name: 'always on', setting: 'always_on', preference: 'off', expected: ['unread']},
        {name: 'default on, no preference', setting: 'default_on', preference: '', expected: ['unread']},
        {name: 'default off, user turned it on', setting: 'default_off', preference: 'on', expected: ['unread']},
        {name: 'default off, no preference', setting: 'default_off', preference: '', expected: []},
        {name: 'default on, user turned it off', setting: 'default_on', preference: 'off', expected: []},
        {name: 'disabled', setting: 'disabled', preference: 'on', expected: []},
        {name: 'setting missing', setting: '', preference: 'on', expected: []},
    ])('collapsed reply threads $name: $expected', ({setting, preference, expected}) => {
        const store = makeStore(setting, preference);
        const roots = ['unread', 'read', 'open', 'not-loaded'];
        const {result} = renderHook(() => useUnreadThreadRoots(roots, 'open'), {
            wrapper: ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>,
        });
        expect(Array.from(result.current)).toEqual(expected);
    });
});

describe('useMarkAgentThreadRead and delivered replies', () => {
    const now = 1_000_000;

    const makeReadStore = (deferredNotifications: boolean | null) => createStore(() => ({
        'plugins-mattermost-ai': {deferredNotifications},
        entities: {
            users: {currentUserId: 'user-id'},
            teams: {currentTeamId: 'team-id', myMembers: {}},
            posts: {posts: {root: {id: 'root', reply_count: 1, last_reply_at: now - 1000}}, postsInThread: {}},
            threads: {threads: {}},
        },
    }));

    const deliver = (post: Record<string, unknown>) => act(() => {
        handlePostedWebsocket({data: {post: JSON.stringify(post)}});
    });

    beforeEach(() => {
        jest.spyOn(Date, 'now').mockReturnValue(now);
        mockViewedPosts.clear();
        mockUpdateRead.mockClear();
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each([
        {name: 'a delivered reply on screen marks the thread read as of its delivery', deferred: true, post: {id: 'reply', root_id: 'root', update_at: now + 500}, viewed: ['reply'], readAt: now + 500},
        {name: 'a delivered reply on screen uses this clock when it is later', deferred: true, post: {id: 'reply', root_id: 'root', update_at: now - 500}, viewed: ['reply'], readAt: now},
        {name: 'a delivered reply that is not on screen stays unread', deferred: true, post: {id: 'reply', root_id: 'root', update_at: now + 500}, viewed: [], readAt: null},
        {name: 'a reply in another thread is ignored', deferred: true, post: {id: 'reply', root_id: 'other-root', update_at: now + 500}, viewed: ['reply'], readAt: null},
        {name: 'a server without deferred notifications ignores repeated posted events', deferred: false, post: {id: 'reply', root_id: 'root', update_at: now + 500}, viewed: ['reply'], readAt: null},
    ])('$name', ({deferred, post, viewed, readAt}) => {
        viewed.forEach((id) => mockViewedPosts.add(id));
        const store = makeReadStore(deferred);
        const {unmount} = renderHook(() => useMarkAgentThreadRead('root', true), {
            wrapper: ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>,
        });
        expect(mockUpdateRead).toHaveBeenCalledTimes(1);

        deliver(post);

        if (readAt === null) {
            expect(mockUpdateRead).toHaveBeenCalledTimes(1);
        } else {
            expect(mockUpdateRead).toHaveBeenCalledTimes(2);
            expect(mockUpdateRead).toHaveBeenLastCalledWith('user-id', 'team-id', 'root', readAt);
        }
        unmount();
    });
});
