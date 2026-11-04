// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {renderHook} from '@testing-library/react';

import {useUnreadThreadRoots} from './use_agent_thread_read';

jest.mock('@/client', () => ({updateRead: jest.fn()}));

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
